package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/harness"
	"github.com/Autumn-27/norma/llm"
	actool "github.com/Autumn-27/norma/tool"
	"github.com/Philocyber/counterweight/intercept"
	"github.com/Philocyber/counterweight/locale"
)

// OperationReviewTool identifies code-owned executors that review their resolved
// operation immediately before execution instead of reviewing abstract params.
type OperationReviewTool interface{ RequiresOperationReview() bool }
type toolUnwrapper interface{ UnwrapTool() actool.CoreTool }
type reviewBindingKey struct{}
type requiredReviewKey struct{}
type operationReviewBinding struct {
	name  string
	hooks harness.HookRunner
}

func RequireOperationReview(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, requiredReviewKey{}, name)
}

// ReviewOperation invokes the existing guard once with the final operation. It
// cannot rewrite an already resolved plan: a changed input requires a new review.
func ReviewOperation(ctx context.Context, executor string, operation any, tc *actool.ToolContext) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	binding, ok := ctx.Value(reviewBindingKey{}).(operationReviewBinding)
	if !ok {
		if name, _ := ctx.Value(requiredReviewKey{}).(string); name != "" {
			return locale.Errorf("Custom operation %s has no runtime review binding", name)
		}
		return nil // Standalone internal executor tests do not constitute agent calls.
	}
	if required, _ := ctx.Value(requiredReviewKey{}).(string); required != "" && required != binding.name {
		return locale.Errorf("Custom operation review binding does not match %s", required)
	}
	if binding.hooks == nil {
		return nil
	} // An explicitly unguarded session.
	// An operation that is already the exact call to review is used as written.
	input, encoded := operation.(json.RawMessage)
	if !encoded {
		var err error
		if input, err = json.Marshal(operation); err != nil {
			return err
		}
	}
	id := ""
	if tc != nil {
		id = tc.ToolUseID
	}
	ctx = intercept.WithResolvedOperation(ctx, id, binding.name, executor, input)
	block, message, updated := binding.hooks.PreToolUse(ctx, binding.name, input)
	if block {
		return fmt.Errorf("%s", message)
	}
	if len(updated) != 0 && string(updated) != string(input) {
		return locale.Errorf("Resolved operation was changed by a hook; execution requires a new review")
	}
	return ctx.Err()
}

// ConfigureOperationReview is applied before each production SDK session is
// created. Custom executors inherit the same hooks, including worker steering;
// the SDK's earlier raw-param hook is skipped only for those executors.
func ConfigureOperationReview(opts *agentcore.Options) {
	configureContainerOptions(opts)
	opts.BashEnv = ExecutorEnvironmentOverrides(opts.BashEnv)
	// The SDK's own WebFetch follows redirects without review and repeats a
	// failed proxied request without the proxy. It is never registered: a
	// session that asks for WebFetch gets the reviewed one, whose redirect
	// reviews run through that session's hooks and whose panics become tool
	// errors. NewRegistry keeps the last handler for a name, as it did for
	// the SDK's.
	if opts.EnableWebFetch {
		opts.EnableWebFetch = false
		tools := opts.Tools
		if tools == nil {
			tools = actool.DefaultTools()
		}
		opts.Tools = append(append([]actool.CoreTool{}, tools...), guardPanic(&operationBoundTool{CoreTool: newReviewedWebFetch(opts.WebFetchProxy, opts.WebFetchCACert, opts.WebFetchInsecureTLS), hooks: opts.Hooks}))
	}
	// The SDK's Monitor starts a host command with the server's own environment,
	// not the masked one above. The guard hook refuses it and points to Bash; a
	// session without hooks has no guard, so the SDK's deny list refuses it there.
	if opts.Hooks == nil {
		opts.DisallowedTools = append(opts.DisallowedTools, "Monitor")
	}
	if _, ok := opts.Hooks.(*operationHooks); ok {
		return
	}
	if opts.Tools != nil {
		opts.Tools = append([]actool.CoreTool{}, opts.Tools...)
	}
	byName := make(map[string]actool.CoreTool)
	custom := make(map[string]bool)
	for _, tool := range opts.Tools {
		byName[tool.Name()] = tool
	}
	// NewRegistry uses the last handler for each name. SDK-added handlers also
	// replace host tools, so their raw hooks must never inherit a custom exemption.
	for name := range sdkOperationOverrides(opts) {
		delete(byName, name)
	}
	for name, tool := range byName {
		custom[name] = requiresOperationReview(tool)
	}
	for i, tool := range opts.Tools {
		if requiresOperationReview(tool) {
			opts.Tools[i] = &operationBoundTool{CoreTool: tool, hooks: opts.Hooks}
		}
	}
	if opts.Hooks != nil {
		deferred := make(map[string]bool, len(opts.DeferredTools))
		for _, name := range opts.DeferredTools {
			deferred[name] = true
		}
		opts.Hooks = &operationHooks{inner: opts.Hooks, tools: byName, custom: custom, deferred: deferred, unlock: opts.UnlockSet}
	}
}

// extraToolCall reads an ExecuteExtraTool invocation the way the SDK does and
// returns the tool it runs and the parameters that tool receives. It reports
// false for an invocation the SDK rejects without running anything.
func extraToolCall(input []byte) (string, json.RawMessage, bool) {
	var invocation struct {
		Name   string          `json:"tool_name"`
		Params json.RawMessage `json:"params"`
	}
	if json.Unmarshal(input, &invocation) != nil || invocation.Name == "" {
		return "", nil, false
	}
	if len(invocation.Params) == 0 {
		invocation.Params = json.RawMessage("{}")
	}
	return invocation.Name, invocation.Params, true
}

func requiresOperationReview(tool actool.CoreTool) bool {
	for inner := tool; inner != nil; {
		if marker, ok := inner.(OperationReviewTool); ok && marker.RequiresOperationReview() {
			return true
		}
		unwrap, ok := inner.(toolUnwrapper)
		if !ok {
			break
		}
		inner = unwrap.UnwrapTool()
	}
	return false
}

// These registrations follow norma v0.4.3 NewSession. Keep this list aligned
// when upgrading the SDK; a replaced handler must retain its ordinary hook.
func sdkOperationOverrides(opts *agentcore.Options) map[string]bool {
	names := make(map[string]bool)
	add := func(name string) { names[name] = true }
	if opts.Plan != nil {
		add("EnterPlanMode")
		add("ExitPlanMode")
	}
	if opts.Memory != nil && opts.Memory.Store != nil {
		add("RecallMemory")
		add("RecordMemory")
	}
	if opts.Skills != nil {
		add(opts.Skills.Tool().Name())
	}
	// WebFetch is not listed: ConfigureOperationReview never lets the SDK add it.
	if opts.EnableWebSearch {
		if tool, err := actool.NewWebSearch(actool.WebSearchConfig{Backend: opts.WebSearchBackend, BraveAPIKey: opts.BraveSearchAPIKey, TavilyAPIKey: opts.TavilySearchAPIKey, DeepSeekBaseURL: opts.DeepSeekSearchBaseURL, DeepSeekAPIKey: opts.DeepSeekSearchAPIKey, DeepSeekModel: opts.DeepSeekSearchModel, Proxy: opts.WebSearchProxy, CACert: opts.WebSearchCACert, InsecureTLS: opts.WebSearchInsecureTLS}); err == nil {
			add(tool.Name())
		}
	}
	if opts.Todos != nil {
		add(opts.Todos.Tool().Name())
	}
	if opts.AskUser != nil {
		add("AskUserQuestion")
	}
	if !actool.BackgroundTasksDisabled() && !opts.DisableBackgroundTasks {
		// Manager creation can fail. In that case a same-name host tool keeps
		// both reviews, rather than risking an unreviewed SDK management tool.
		add("TaskOutput")
		add("TaskStop")
		add("TaskList")
		add("Monitor")
	}
	if len(opts.DeferredTools) > 0 {
		add(actool.ExecuteExtraToolName)
		add("SearchExtraTools")
	}
	return names
}

type operationBoundTool struct {
	actool.CoreTool
	hooks harness.HookRunner
}

func (t *operationBoundTool) Call(ctx context.Context, input json.RawMessage, tc *actool.ToolContext) (actool.Result, error) {
	if _, ok := ctx.Value(reviewBindingKey{}).(operationReviewBinding); !ok {
		ctx = context.WithValue(ctx, reviewBindingKey{}, operationReviewBinding{name: t.Name(), hooks: t.hooks})
	}
	return t.CoreTool.Call(ctx, input, tc)
}
func (t *operationBoundTool) UnwrapTool() actool.CoreTool { return t.CoreTool }

type operationHooks struct {
	inner  harness.HookRunner
	tools  map[string]actool.CoreTool
	custom map[string]bool
	// deferred names the tools whose schemas are withheld; unlock is the
	// session's set of those that may currently be called (nil: all of them).
	deferred map[string]bool
	unlock   *actool.UnlockSet
}

func (h *operationHooks) PreToolUse(ctx context.Context, name string, input []byte) (bool, string, []byte) {
	// A deferred tool runs only once it is unlocked, however the call names it:
	// withholding its schema does not stop a call that uses the name directly.
	if h.unlock != nil && h.deferred[name] && !h.unlock.Has(name) {
		return true, locale.Text(locale.FromContext(ctx), "Tool %s is not unlocked in this session; load the skill that provides it first", name), nil
	}
	if h.custom[name] {
		return false, "", nil
	}
	if name == actool.ExecuteExtraToolName {
		// The wrapper hands its params straight to the named tool, so that tool
		// and those params are reviewed exactly as a direct call would be: same
		// enabled-tool check, rules, reviewer, approvals and records.
		if inner, params, ok := extraToolCall(input); ok {
			ctx = intercept.WithEffectiveInput(ctx, name, input, params)
			block, message, updated := h.PreToolUse(ctx, inner, params)
			if block || len(updated) == 0 {
				return block, message, nil
			}
			wrapped, err := json.Marshal(map[string]any{"tool_name": inner, "params": json.RawMessage(updated)})
			if err != nil {
				return true, err.Error(), nil
			}
			return false, "", wrapped
		}
	}
	effective := json.RawMessage(input)
	for tool := h.tools[name]; tool != nil; {
		if decorator, ok := tool.(*overriddenTool); ok {
			effective = injectDefaults(effective, decorator.schema)
		}
		unwrap, ok := tool.(toolUnwrapper)
		if !ok {
			break
		}
		tool = unwrap.UnwrapTool()
	}
	if string(effective) != string(input) {
		ctx = intercept.WithEffectiveInput(ctx, name, input, effective)
	}
	block, message, updated := h.inner.PreToolUse(ctx, name, effective)
	if !block && len(updated) == 0 && string(effective) != string(input) {
		updated = effective
	}
	return block, message, updated
}
func (h *operationHooks) PostToolUse(ctx context.Context, name string, input, result []byte, isErr bool) {
	// The wrapper's result is the result of the tool it ran, so it is reported
	// under the tool and params that were reviewed before the call.
	if name == actool.ExecuteExtraToolName {
		if inner, params, ok := extraToolCall(input); ok {
			name, input = inner, params
		}
	}
	h.inner.PostToolUse(ctx, name, input, result, isErr)
}
func (h *operationHooks) Stop(ctx context.Context, messages []llm.Message) (bool, []string, string) {
	return h.inner.Stop(ctx, messages)
}

// Preserve decorators' discovery of the underlying custom executor/defaults.
func (g *guardedTool) UnwrapTool() actool.CoreTool    { return g.CoreTool }
func (t *overriddenTool) UnwrapTool() actool.CoreTool { return t.CoreTool }
