package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Philocyber/counterweight/locale"
	"log"
	"runtime/debug"

	actool "github.com/Autumn-27/norma/tool"
)

// DeferredInfo carries the deferred-tools wiring an agent needs to build its
// Options: the MCP tool names whose schemas are withheld, the subset listed in the
// global system-prompt block (non-skill-gated), and the shared session unlock set.
// UnlockSkill unlocks a named skill's MCPs — hosts call it to rebuild the unlock set
// from history on a resumed session (design doc C2).
type DeferredInfo struct {
	FindingGuidance string            // derived from the final permitted tools, including DB overrides
	Deferred        []string          // all MCP tool names (schema withheld)
	GlobalNames     []string          // MCP names to list in the system-prompt block
	Unlock          *actool.UnlockSet // shared call-gate; nil when no MCP tools
	UnlockSkill     func(skillName string)
}

// ToolAugment, if set, returns the EXTRA tools an agent should see beyond its
// built-in base set — the agent's visible skills (packed into one Skill meta-tool)
// and visible MCP servers (expanded to mcp__server__tool). It also returns the
// DeferredInfo describing how those MCP tools are deferred/gated. The server wires
// it to the PG agent_visibility table. cleanup releases any spawned MCP clients.
//
// When nil, agents run with only their built-in tools — behavior is unchanged
// until a user assigns a skill/MCP to the agent in the UI.
var ToolAugment func(ctx context.Context, agentKey string) (extra []actool.CoreTool, def DeferredInfo, cleanup func())

// ReviewExtraTools, if set, returns tools added to a source-review planner or
// worker run for the given task, after the review tool filter. The host returns
// none until the task has something for them to act on.
var ReviewExtraTools func(ctx context.Context, taskID int64) []actool.CoreTool

// AugmentTools returns base plus the agent's visible skill/MCP tools, the
// DeferredInfo, and a cleanup func the caller must defer (closes MCP clients).
// Built-in base tools remain in code and are not filtered for visibility.
func AugmentTools(ctx context.Context, agentKey string, base []actool.CoreTool) ([]actool.CoreTool, DeferredInfo, func()) {
	var (
		def     DeferredInfo
		cleanup = func() {}
		out     = base
	)
	if ToolAugment != nil {
		var extra []actool.CoreTool
		var cl func()
		extra, def, cl = ToolAugment(ctx, agentKey)
		if cl != nil {
			cleanup = cl
		}
		if len(extra) > 0 {
			out = append(append([]actool.CoreTool{}, base...), extra...)
		}
	}
	// DB tools table has the final say on the built-in tools: drop the ones this
	// agent isn't bound to (or that are disabled) and swap in overridden
	// descriptions/schemas + default injection. MCP/skill/host tools have no row
	// and pass through untouched, so deferred/unlock wiring stays consistent.
	var fileCleanup func()
	out, fileCleanup = confineFileTools(ctx, agentKey, out)
	priorCleanup := cleanup
	cleanup = func() { fileCleanup(); priorCleanup() }
	if fileAdmissionError(out) != nil {
		return out, def, cleanup
	}
	if ToolResolve != nil {
		out = ToolResolve(ctx, agentKey, out)
	}
	if ContainerExecution(ctx, agentKey) {
		filtered := make([]actool.CoreTool, 0, len(out))
		for _, t := range out {
			if containerProcessTool(t.Name()) {
				continue
			}
			// A resolver must never restore a host Bash implementation.
			if t.Name() == "Bash" && !isContainerTool(t) {
				continue
			}
			if !containerAllowed(t) {
				continue
			}
			filtered = append(filtered, &containerCatalogTool{t})
		}
		out = filtered
	}
	if CodeReviewExecution(ctx, agentKey) {
		out = codeReviewToolFilter(out)
		for i, tool := range out {
			if tool.Name() == "add_hint" || tool.Name() == "add_task_hint" || tool.Name() == "add_intent" || tool.Name() == "record_fact" || tool.Name() == "report_finding" {
				schema := tool.InputSchema()
				raw, _ := json.Marshal(schema)
				var reviewSchema map[string]any
				_ = json.Unmarshal(raw, &reviewSchema)
				if reviewSchema == nil {
					reviewSchema = obj(nil)
				}
				props, _ := reviewSchema["properties"].(map[string]any)
				if props == nil {
					props = map[string]any{}
					reviewSchema["properties"] = props
				}
				delete(props, "asset_ids")
				stripTrafficParameters(reviewSchema)
				desc := tool.Description()
				switch tool.Name() {
				case "add_intent":
					props["summary"] = str(locale.Text(locale.FromContext(ctx), "One sentence naming repository files or directories, the review action and its reason. Network asset IDs do not apply."))
					desc = locale.Text(locale.FromContext(ctx), "Create repository review directions. Submit independent directions together in intents; each summary names files or directories and a security question. Use existing fact/finding parent_ids for lineage. Do not supply network asset IDs.")
				case "record_fact":
					desc = locale.Text(locale.FromContext(ctx), "Record source-review observations, reviewed files, negative evidence and unresolved leads. Supply intent_id for lineage, summary, detail, file:line evidence and observed/inferred confidence. Batch distinct conclusions in facts, with the same fields and optional per-item intent_id.")
					props["facts"] = map[string]any{"type": "array", "items": map[string]any{"type": "object"}}
					props["evidence"] = str(locale.Text(locale.FromContext(ctx), "Source evidence: repository file:line citations and short code snippets, not command output."))
				case "report_finding":
					desc = locale.Text(locale.FromContext(ctx), "Record a confirmed source-code vulnerability after tracing and disproving alternatives. Supply intent_id, severity, summary and evidence with ordered source-to-sink file:line citations, snippets and repository ref/commit. No command execution or traffic evidence is required. Returned finding_id is the independent record ID; finding_node_id is the exploration node ID.")
					props["evidence"] = str(locale.Text(locale.FromContext(ctx), "Source evidence: repository file:line citations and short code snippets, not command output."))
				}
				out[i] = DecorateTool(tool, desc, reviewSchema)
			}
		}
		// Supplementary tools exist only for runs that the host says need them.
		if ReviewExtraTools != nil {
			out = append(out, ReviewExtraTools(ctx, RunInfoFrom(ctx).TaskID)...)
		}
		out = codeReviewToolFilter(out)
		// Reviews cannot discover or execute deferred MCP/custom tools.
		def = DeferredInfo{}
	} else {
		out, def.FindingGuidance = findingWorkflowTools(agentKey, out, locale.FromContext(ctx))
	}
	for i, t := range out {
		out[i] = guardPanic(t)
	}
	if out == nil {
		// An explicit empty resolved catalog must survive decorators and filters.
		// The SDK treats nil as permission to restore its default tool catalog.
		out = []actool.CoreTool{}
	}
	return out, def, cleanup
}

// guardPanic turns a panicking tool handler into an ordinary tool error. The
// harness runs each tool on its own goroutine, so a panic inside a handler can't
// be recovered by the caller that started the run — it takes the whole process
// down, and on restart the agent replays the same call and crashes again. Applied
// last, so it covers every tool the agent can reach: domain, SDK, MCP and skill.
func guardPanic(t actool.CoreTool) actool.CoreTool { return &guardedTool{CoreTool: t} }

type guardedTool struct{ actool.CoreTool }

func (g *guardedTool) Call(ctx context.Context, in json.RawMessage, tc *actool.ToolContext) (res actool.Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[tools] %s panic: %v\n%s", g.Name(), r, debug.Stack())
			res, err = actool.Errorf(fmt.Sprintf(locale.Text(locale.FromContext(ctx), "Tool %s internal error: %v (this call failed; try different arguments or another tool)"), g.Name(), r)), nil
		}
	}()
	return g.CoreTool.Call(ctx, in, tc)
}
