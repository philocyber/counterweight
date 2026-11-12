// Package guard implements the safety boundary layer (docs §11): an audit log,
// user-configured intercept-rule evaluation, and Observer/G5 failure attribution.
// Every tool call passes through the PreToolUse hook before executing.
// (The RoE authorization-scope mechanism was removed; a replacement may be added
// later.) Destructive/exfil gating is no longer hard-coded here — it lives in the
// DB intercept rules (seeded as ordinary [Built-in] rules, so users can disable or
// delete them), evaluated via applyIntercept.
package guard

import (
	"context"
	"encoding/json"
	"github.com/Philocyber/counterweight/locale"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Autumn-27/norma/hook"
	"github.com/Philocyber/counterweight/intercept"
)

// AuditEntry records one gated tool call.
type AuditEntry struct {
	TS      int64  `json:"ts"`
	Tool    string `json:"tool"`
	Action  string `json:"action"` // allow|block
	Reason  string `json:"reason,omitempty"`
	Command string `json:"command,omitempty"`
}

// Guard enforces the side-effect policy via agent-core hooks.
type Guard struct {
	mu          sync.Mutex
	audit       []AuditEntry
	attrib      map[string]int // failure attribution counts (Observer / G5)
	reg         *hook.Registry
	interceptor *intercept.Interceptor // optional; nil disables user-configured rules
}

// New creates a Guard without user-configured intercept rules (used for pentest
// tasks where the Interceptor is not yet available).
func New() *Guard { return newGuard(nil) }

// NewWithInterceptor creates a Guard with user-configured intercept rules.
func NewWithInterceptor(ic *intercept.Interceptor) *Guard { return newGuard(ic) }

func newGuard(ic *intercept.Interceptor) *Guard {
	g := &Guard{attrib: map[string]int{}, interceptor: ic}
	g.reg = hook.NewRegistry().
		On(hook.PreToolUse, g.preToolUse).
		On(hook.PostToolUse, g.postToolUse)
	return g
}

// Hooks returns the hook registry to attach to an agent session.
func (g *Guard) Hooks() *hook.Registry { return g.reg }

func (g *Guard) preToolUse(ctx context.Context, ev hook.Event) hook.Result {
	if ctx.Err() != nil {
		return g.block(ev.ToolName, "Work was stopped before tool execution", "")
	}
	if g.interceptor != nil {
		held, err := g.interceptor.TaskHeld(ctx)
		if err != nil {
			return g.block(ev.ToolName, systemBlockMessage(locale.Text(locale.FromContext(ctx), "Could not verify whether the task is paused for tool permission")), "")
		}
		if held {
			return g.block(ev.ToolName, intercept.ApprovalDeferredMessage(locale.FromContext(ctx)), "")
		}
	}
	// A shell_send call is recorded, matched against the rules and put to a
	// person as the line it ends or extends: what earlier calls left pending on
	// the session's line, followed by what this call sends. The tool itself
	// still receives the call as it was made.
	if ev.ToolName == "shell_send" && g.interceptor != nil {
		ctx, ev.Input = g.interceptor.ShellSendOnLine(ctx, ev.Input)
	}

	// Extract the shell-command surface for the audit log: Bash + the interactive-shell
	// tools (shell_open's command, shell_send's text). Destructive/exfil gating is no
	// longer hard-coded here — it now lives in the DB intercept rules, evaluated by
	// applyIntercept below. Other tools record an empty command.
	var cmd string
	switch ev.ToolName {
	case "Bash", "shell_open", "Monitor":
		var in struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal(ev.Input, &in)
		cmd = in.Command
	case "shell_send":
		// Record everything the call writes to the session, not only the text.
		var in struct {
			Text  string   `json:"text"`
			Paste string   `json:"paste"`
			Keys  []string `json:"keys"`
			Hex   []string `json:"hex"`
			// Added by the gate for a call that continues a line: the text
			// earlier calls left pending on it, and a mark when that line is
			// not inspectable.
			Before  string `json:"line_before"`
			Unknown bool   `json:"line_not_inspectable"`
		}
		_ = json.Unmarshal(ev.Input, &in)
		cmd = in.Before + in.Text
		if in.Unknown {
			cmd = "[line not inspectable] " + cmd
		}
		if len(in.Keys) > 0 {
			cmd += " [keys: " + strings.Join(in.Keys, " ") + "]"
		}
		if len(in.Hex) > 0 {
			cmd += " [hex: " + strings.Join(in.Hex, " ") + "]"
		}
		if in.Paste != "" {
			cmd += " [paste: " + in.Paste + "]"
		}
	}
	// Monitor starts a shell command on the host with the server's own
	// environment and outside the rules, approvals and records that apply to
	// Bash. It never runs; Bash covers the same use, also in the background.
	if ev.ToolName == "Monitor" {
		return g.block(ev.ToolName, systemBlockMessage(locale.Text(locale.FromContext(ctx), "Monitor is not available; run the command with Bash and run_in_background set to true, then read it with TaskOutput")), cmd)
	}
	g.record(ev.ToolName, "allow", "", cmd)
	return g.applyIntercept(ctx, ev)
}

// applyIntercept evaluates user-configured intercept rules against the tool call.
// Both rules and the fallback judge receive the complete tool input.
func (g *Guard) applyIntercept(ctx context.Context, ev hook.Event) hook.Result {
	if g.interceptor == nil {
		return hook.Result{}
	}
	names := []string{}
	if g.interceptor.IsToolEnabled(ev.ToolName) {
		names = append(names, ev.ToolName)
	}
	if executor := intercept.OperationExecutor(ctx); executor != "" && executor != ev.ToolName && g.interceptor.IsToolEnabled(executor) {
		names = append(names, executor)
	}
	if len(names) == 0 {
		return hook.Result{}
	}
	ctx = intercept.WithCall(ctx, ev.ToolName, ev.Input)
	dec, matched := g.interceptor.MatchNames(names, ev.Input, locale.FromContext(ctx))
	if intercept.OperatorDecidedTool(ev.ToolName) && !(matched && dec.Action == "deny") {
		return hook.Result{}
	}
	if !matched || dec.Action != "deny" {
		if allowed, err := g.interceptor.ReplayApproved(ctx, ev.ToolName, ev.Input, dec); err != nil {
			return g.block(ev.ToolName, "Could not verify the saved tool permission", "")
		} else if allowed {
			return hook.Result{}
		}
	}
	// A concrete user request is already the authorization for its task. Route
	// generic shell uncertainty through the normal effect reviewer instead of
	// asking the same permission for every command. Explicit ask/deny rules win.
	if matched && dec.Action == "ask" && dec.RuleID == 0 && !dec.ModelFallback && !dec.HumanRequired {
		authorization, err := g.interceptor.TaskOperatorAuthorization(ctx)
		if err != nil {
			return g.block(ev.ToolName, "Could not load the task's user authorization", "")
		}
		if authorization.Approved() {
			if verdict, reviewed := g.interceptor.Judge(ctx, ev.ToolName, ev.Input); reviewed {
				dec = verdict
			} else {
				dec = intercept.Decision{Action: "allow", RuleName: "Prior user authorization", Message: "Authorized by the user's task request and scope"}
			}
		}
	}
	// A task grant covers the synthetic "ask for every shell" prompt and tools
	// with no matching rule and legacy whole-tool prompts. Explicit denials and
	// input-specific or structured human-approval rules still win.
	if !matched || (dec.Action == "ask" && (dec.RuleID == 0 || dec.RememberAllowed)) {
		if standing, ok := g.interceptor.MatchingTaskGrant(ctx, names, ev.Input); ok {
			name, message := intercept.GrantRecord(locale.FromContext(ctx), standing)
			grant := intercept.Decision{
				Action:     "allow",
				RuleName:   name,
				GrantScope: standing.Scope,
				Message:    message,
			}
			g.interceptor.Log(ctx, intercept.ConvIDFromContext(ctx), grant, ev.ToolName, ev.Input, "allowed")
			return hook.Result{}
		}
	}
	if !matched {
		// No rule matched. Ask the LLM fallback judge (if enabled); when it is off
		// or unwired, keep current behavior and allow.
		d, judged := g.interceptor.Judge(ctx, ev.ToolName, ev.Input)
		if !judged {
			return hook.Result{}
		}
		dec = d
	}
	switch dec.Action {
	case "deny":
		// A deny match records a denied observation without waiting for approval.
		// It remains visible in history and the task interception view.
		g.interceptor.Log(ctx, intercept.ConvIDFromContext(ctx), dec, ev.ToolName, ev.Input, "denied")
		return g.block(ev.ToolName, systemBlockMessage(dec.Message), "")
	case "allow":
		// Record explicit rule and model approvals so review details remain auditable.
		g.interceptor.Log(ctx, intercept.ConvIDFromContext(ctx), dec, ev.ToolName, ev.Input, "allowed")
		return hook.Result{}
	case "ask":
		// If the worker context is already cancelled (task stopped / killed), block
		// immediately without creating a pending record — avoids orphaned DB entries
		// and makes execOne complete fast, reducing the race against drainSynthetic.
		if ctx.Err() != nil {
			return g.block(ev.ToolName, systemBlockMessage(locale.Text(locale.FromContext(ctx), "Work was cancelled; platform security controls blocked execution")), "")
		}
		convID := intercept.ConvIDFromContext(ctx)
		if !g.interceptor.HandleAsk(ctx, convID, dec, ev.ToolName, ev.Input) {
			held, err := g.interceptor.TaskHeld(ctx)
			if err == nil && held {
				return g.block(ev.ToolName, intercept.ApprovalDeferredMessage(locale.FromContext(ctx)), "")
			}
			return g.block(ev.ToolName, systemBlockMessage(locale.Text(locale.FromContext(ctx), "Tool execution is waiting for human permission or was denied. If the task is paused, resolve its pending requests and click Resume. Do not retry or bypass this control")), "")
		}
		return hook.Result{}
	}
	return hook.Result{}
}

// systemBlockMessage frames an intercept block as a Counterweight platform-governance
// decision so the agent does not mistake it for a target-side defense.
//
// The bare reasons ("tool execution prohibited" / "user denied") read exactly like a WAF/403 on
// the target, so a pentest agent's instinct is to bypass them — rewrite the
// command, swap the payload, re-encode, retry. That is both futile (the platform
// blocks the class of action, not one string) and wrong (it's a policy decision,
// not an obstacle to defeat). This prefix states plainly that the block comes
// from the platform, is not the target's protection, and that the operation is
// forbidden — so the agent pivots to another approach instead of evading it.
// Audit/history rows keep the raw reason (see Interceptor.Log); only the
// model-facing tool_result carries this framing.
func systemBlockMessage(reason string) string {
	return locale.Text(locale.ServerDefault(), "[Counterweight platform control, not target defense] This call was blocked by the platform. ") +
		locale.Text(locale.ServerDefault(), "Reason: ") + reason + locale.Text(locale.ServerDefault(), ". This operation is prohibited.")
}

var reBlocked = regexp.MustCompile(`(?i)\b(403|forbidden|waf|blocked|rate.?limit|429|captcha|denied)\b`)

// postToolUse is the Observer failure-attribution hook (G5): it classifies tool
// results into blocked / error / ok so the planner can change strategy instead
// of giving up at a WAF.
func (g *Guard) postToolUse(ctx context.Context, ev hook.Event) hook.Result {
	// What an interactive shell call sent is counted once it has run, so the
	// pending line of its session follows what the terminal received.
	if g.interceptor != nil && !ev.IsError {
		g.interceptor.ShellToolRan(ctx, ev.ToolName, ev.Input, ev.Result)
	}
	if ev.ToolName != "Bash" {
		return hook.Result{}
	}
	class := "ok"
	switch {
	case reBlocked.Match(ev.Result):
		class = "blocked"
	case ev.IsError:
		class = "error"
	}
	g.mu.Lock()
	g.attrib[class]++
	g.mu.Unlock()
	return hook.Result{}
}

// Attributions returns failure-attribution counts (Observer / G5).
func (g *Guard) Attributions() map[string]int {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make(map[string]int, len(g.attrib))
	for k, v := range g.attrib {
		out[k] = v
	}
	return out
}

func (g *Guard) block(tool, reason, cmd string) hook.Result {
	g.record(tool, "block", reason, cmd)
	return hook.Result{Decision: "block", Message: reason}
}

func (g *Guard) record(tool, action, reason, cmd string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.audit = append(g.audit, AuditEntry{TS: time.Now().Unix(), Tool: tool, Action: action, Reason: reason, Command: cmd})
	if len(g.audit) > 2000 {
		g.audit = g.audit[len(g.audit)-2000:]
	}
}

// Audit returns a snapshot of recent gated calls (most recent last).
func (g *Guard) Audit() []AuditEntry {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]AuditEntry, len(g.audit))
	copy(out, g.audit)
	return out
}
