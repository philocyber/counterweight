package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Philocyber/counterweight/locale"
	"os"
	"path/filepath"
	"strings"

	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/intercept"
	actool "github.com/Autumn-27/norma/tool"
)

// Platform host tools for the built-in Auto agent: create/edit skills, custom tools, and MCP servers.
// Seeded into tools, bound to auto by default, and injected through hostTools; reuse existing DB/filesystem logic.

func (s *Server) platformTools(langs ...locale.Lang) []actool.CoreTool {
	return []actool.CoreTool{
		operatorApprovedTool{s.toolCreateSkill(langs...), s},
		operatorApprovedTool{s.toolUpdateSkillFile(langs...), s},
		operatorApprovedTool{s.toolCreateCustomTool(langs...), s},
		operatorApprovedTool{s.toolUpdateCustomTool(langs...), s},
		operatorApprovedTool{s.toolCreateMCP(langs...), s},
		operatorApprovedTool{s.toolUpdateMCP(langs...), s},
		s.toolDeleteAssetsByHost(langs...),
	}
}

// operatorApprovedTool makes a person decide every call of a tool that defines
// what agents run later: a skill's instructions, a custom tool's program, an
// MCP server's command. These definitions take effect outside the call that
// wrote them (a stored server is started at the next agent run, a rewritten
// tool keeps its standing approvals), so the decision does not depend on which
// tools are configured for interception, on a rule or on the model reviewer.
type operatorApprovedTool struct {
	actool.CoreTool
	s *Server
}

func (t operatorApprovedTool) Call(ctx context.Context, in json.RawMessage, tc *actool.ToolContext) (actool.Result, error) {
	if t.s.m == nil || t.s.m.interceptor == nil {
		return actool.Errorf(locale.Text(locale.FromContext(ctx), "This change needs human approval, and the approval gate is unavailable")), nil
	}
	gate := t.s.m.interceptor
	ctx = intercept.WithCall(ctx, t.Name(), in)
	ask := intercept.Decision{
		Action: "ask", RuleName: "Platform definition change", HumanRequired: true,
		Message:        locale.Text(locale.FromContext(ctx), "An agent asked to create or change a skill, custom tool or MCP server. Check the complete input before allowing it"),
		TimeoutEnabled: true, TimeoutSeconds: 300, TimeoutAction: "deny",
	}
	// A task waits for the decision while paused and repeats the call on resume.
	approved, err := gate.ReplayApproved(ctx, t.Name(), in, ask)
	if err == nil && !approved {
		approved = gate.HandleAsk(ctx, intercept.ConvIDFromContext(ctx), ask, t.Name(), in)
	}
	if err != nil || !approved {
		// A held task resumes from the marked result: without the marker the
		// worker is never told to repeat the call it was approved for.
		if held, heldErr := gate.TaskHeld(ctx); err == nil && heldErr == nil && held {
			return actool.Errorf(intercept.ApprovalDeferredMessage(locale.FromContext(ctx))), nil
		}
		return actool.Errorf(locale.Text(locale.FromContext(ctx), "This change waits for human approval or was denied. Do not retry or work around it")), nil
	}
	return t.CoreTool.Call(ctx, in, tc)
}

// platformToolKeys are the tool keys the Auto agent gets bound by default.
var platformToolKeys = []string{
	"create_skill", "update_skill_file",
	"create_custom_tool", "update_custom_tool",
	"create_mcp", "update_mcp",
	"delete_assets_by_host",
}

// ---- assets ----

// toolDeleteAssetsByHost hard-deletes every asset tied to one host (exact match).
// Platform-level tool operating on the global asset store across tasks.
func (s *Server) toolDeleteAssetsByHost(langs ...locale.Lang) actool.CoreTool {
	return wrTool("delete_assets_by_host",
		locale.Text(locale.First(langs), "Delete assets by exact host: its root/subdomain plus services and endpoints.\n")+
			locale.Text(locale.First(langs), "Host matching is exact after lowercasing/trimming, not fuzzy or wildcard.\n")+
			locale.Text(locale.First(langs), "A root domain such as example.com also deletes its subdomains/services/endpoints. A subdomain or IP deletes only that host and its services/endpoints.\n")+
			locale.Text(locale.First(langs), "Warning: irreversible hard deletion from the globally shared asset store across tasks."),
		objSchema(map[string]any{
			"host": strParam(locale.Text(locale.First(langs), "Exact host to delete: domain, subdomain, or IP such as example.com, a.example.com, or 1.2.3.4")),
		}, "host"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			as := s.assetStore()
			if as == nil {
				return actool.Errorf(locale.Text(locale.First(langs), "Asset store is not initialized")), nil
			}
			var a struct {
				Host string `json:"host"`
			}
			_ = json.Unmarshal(in, &a)
			if strings.TrimSpace(a.Host) == "" {
				return actool.Errorf(locale.Text(locale.First(langs), "host cannot be empty")), nil
			}
			counts, err := as.DeleteByHost(a.Host)
			if err != nil {
				return actool.Errorf(locale.Text(locale.First(langs), "Deletion failed: ") + locale.ErrorMessage(locale.FromContext(ctx), err)), nil
			}
			var total int64
			for _, n := range counts {
				total += n
			}
			return jsonResult(map[string]any{
				"host":            a.Host,
				"deleted":         total,
				"deleted_by_type": counts,
			})
		})
}

// ---- skills ----

func (s *Server) toolCreateSkill(langs ...locale.Lang) actool.CoreTool {
	return wrTool("create_skill",
		locale.Text(locale.First(langs), "Create a new skill by writing SKILL.md in agentskills.io format. name uses lowercase letters, digits, and hyphens."),
		objSchema(map[string]any{
			"name":         strParam(locale.Text(locale.First(langs), "Skill name: starts with a lowercase letter; letters/digits/hyphens")),
			"description":  strParam(locale.Text(locale.First(langs), "Required skill description: what it does and when to use it")),
			"instructions": strParam(locale.Text(locale.First(langs), "Optional Markdown instructions")),
		}, "name", "description"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct{ Name, Description, Instructions string }
			_ = json.Unmarshal(in, &a)
			if !validSkillName(a.Name) {
				return actool.Errorf(locale.Text(locale.First(langs), "Invalid skill name: lowercase letter first, only letters/digits/hyphens, at most 64 characters")), nil
			}
			if strings.TrimSpace(a.Description) == "" {
				return actool.Errorf(locale.Text(locale.First(langs), "description is required")), nil
			}
			path := filepath.Join(s.skillDir, a.Name)
			if _, err := os.Stat(path); err == nil {
				return actool.Errorf(locale.Text(locale.First(langs), "Skill already exists: ") + a.Name), nil
			}
			if err := os.MkdirAll(path, 0o755); err != nil {
				return actool.Errorf(locale.ErrorMessage(locale.FromContext(ctx), err)), nil
			}
			var b strings.Builder
			b.WriteString("---\n")
			fmt.Fprintf(&b, "name: %s\n", a.Name)
			fmt.Fprintf(&b, "description: %s\n", a.Description)
			b.WriteString("---\n")
			if strings.TrimSpace(a.Instructions) != "" {
				b.WriteString(a.Instructions)
			} else {
				fmt.Fprintf(&b, "## %s\n\n1. \n2. \n3. \n", a.Name)
			}
			if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte(b.String()), 0o644); err != nil {
				_ = os.RemoveAll(path)
				return actool.Errorf(locale.ErrorMessage(locale.FromContext(ctx), err)), nil
			}
			return actool.Text("skill created: " + a.Name), nil
		})
}

func (s *Server) toolUpdateSkillFile(langs ...locale.Lang) actool.CoreTool {
	return wrTool("update_skill_file",
		locale.Text(locale.First(langs), "Write or replace one file inside a skill, default SKILL.md; use to edit instructions or add scripts/references."),
		objSchema(map[string]any{
			"name":    strParam(locale.Text(locale.First(langs), "Skill name")),
			"file":    strParam(locale.Text(locale.First(langs), "Optional relative path, default SKILL.md; for example scripts/run.py")),
			"content": strParam(locale.Text(locale.First(langs), "Complete file content")),
		}, "name", "content"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct{ Name, File, Content string }
			_ = json.Unmarshal(in, &a)
			if !validSkillName(a.Name) {
				return actool.Errorf(locale.Text(locale.First(langs), "Invalid skill name")), nil
			}
			skillPath := filepath.Join(s.skillDir, a.Name)
			if _, err := os.Stat(skillPath); os.IsNotExist(err) {
				return actool.Errorf(locale.Text(locale.First(langs), "Skill not found: ") + a.Name), nil
			}
			rel := strings.TrimSpace(a.File)
			if rel == "" {
				rel = "SKILL.md"
			}
			clean, msg := skillRelPath(rel)
			if msg != "" {
				return actool.Errorf(locale.Text(locale.First(langs), "Invalid path: ") + msg), nil
			}
			full := filepath.Join(skillPath, clean)
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				return actool.Errorf(locale.ErrorMessage(locale.FromContext(ctx), err)), nil
			}
			if err := os.WriteFile(full, []byte(a.Content), 0o644); err != nil {
				return actool.Errorf(locale.ErrorMessage(locale.FromContext(ctx), err)), nil
			}
			return actool.Text("skill file written: " + a.Name + "/" + clean), nil
		})
}

// ---- custom tools ----

type customToolToolInput struct {
	Key         string          `json:"key"`
	Description string          `json:"description"`
	Kind        string          `json:"kind"`
	Exec        json.RawMessage `json:"exec"`
	Schema      json.RawMessage `json:"schema"`
	Agents      []string        `json:"agents"`
	Deferred    bool            `json:"deferred"`
	Enabled     *bool           `json:"enabled"`
}

func customToolSchema(keyDesc string, langs ...locale.Lang) map[string]any {
	return objSchema(map[string]any{
		"key":         strParam(keyDesc),
		"description": strParam(locale.Text(locale.First(langs), "Model-facing description")),
		"kind":        strParam(locale.Text(locale.First(langs), "shell, command, script (Python only), or http. shell declares a command available through Bash and needs no exec/schema; the other kinds require exec.")),
		"exec":        map[string]any{"type": "object", "description": locale.Text(locale.First(langs), "Execution specification (not needed for shell): command {command}; script {code}; http {method,url,headers,body,proxy,use_recording_proxy}")},
		"schema":      map[string]any{"type": "object", "description": locale.Text(locale.First(langs), "Parameter JSON Schema; optional for shell/command/script, required with properties for http")},
		"agents":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": locale.Text(locale.First(langs), "Optional bound agent keys")},
		"deferred":    map[string]any{"type": "boolean", "description": locale.Text(locale.First(langs), "Deferred visibility: only for infrequently used command/script/http tools; ignored for shell")},
		"enabled":     map[string]any{"type": "boolean", "description": locale.Text(locale.First(langs), "Enabled, default true")},
	}, "key", "kind")
}

func toDBTool(a customToolToolInput) *db.Tool {
	enabled := true
	if a.Enabled != nil {
		enabled = *a.Enabled
	}
	return &db.Tool{
		Key: a.Key, Description: a.Description, Schema: a.Schema, Agents: a.Agents,
		Enabled: enabled, Kind: a.Kind, Exec: a.Exec, Deferred: a.Deferred,
	}
}

func (s *Server) toolCreateCustomTool(langs ...locale.Lang) actool.CoreTool {
	return wrTool("create_custom_tool", locale.Text(locale.First(langs), "After installing a tool absent from the platform, register it here so agents can use it. Create a custom shell/command/script/http tool. A shell declaration needs only key/description/agents, no exec/schema."),
		customToolSchema(locale.Text(locale.First(langs), "Tool key: lowercase letter first, then letters/digits/underscores"), langs...),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a customToolToolInput
			_ = json.Unmarshal(in, &a)
			a.Key = strings.TrimSpace(a.Key)
			if !reToolKey.MatchString(a.Key) || reservedToolKey(a.Key) {
				return actool.Errorf(locale.Text(locale.First(langs), "key must start with a lowercase letter and contain only lowercase letters, digits, or underscores")), nil
			}
			if a.Kind != "shell" && a.Kind != "command" && a.Kind != "script" && a.Kind != "http" {
				return actool.Errorf(locale.Text(locale.First(langs), "kind must be shell, command, script, or http")), nil
			}
			if a.Kind == "http" && !hasSchemaProps(a.Schema) {
				return actool.Errorf(locale.Text(locale.First(langs), "HTTP tools require a nonempty parameter JSON Schema")), nil
			}
			if exist, _ := s.m.pg.GetTool(a.Key); exist != nil {
				return actool.Errorf(locale.Text(locale.First(langs), "Key already exists: ") + a.Key), nil
			}
			if err := s.m.pg.CreateCustomTool(toDBTool(a)); err != nil {
				return actool.Errorf(locale.ErrorMessage(locale.FromContext(ctx), err)), nil
			}
			return actool.Text("custom tool created: " + a.Key), nil
		})
}

func (s *Server) toolUpdateCustomTool(langs ...locale.Lang) actool.CoreTool {
	return wrTool("update_custom_tool", locale.Text(locale.First(langs), "Modify an existing custom tool by key."),
		customToolSchema(locale.Text(locale.First(langs), "Custom tool key to modify"), langs...),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a customToolToolInput
			_ = json.Unmarshal(in, &a)
			existing, _ := s.m.pg.GetTool(a.Key)
			if existing == nil || existing.System {
				return actool.Errorf(locale.Text(locale.First(langs), "Only custom tools can be modified: ") + a.Key), nil
			}
			if a.Kind != "shell" && a.Kind != "command" && a.Kind != "script" && a.Kind != "http" {
				return actool.Errorf(locale.Text(locale.First(langs), "kind must be shell, command, script, or http")), nil
			}
			if a.Kind == "http" && !hasSchemaProps(a.Schema) {
				return actool.Errorf(locale.Text(locale.First(langs), "HTTP tools require a nonempty parameter JSON Schema")), nil
			}
			if err := s.m.pg.UpdateCustomTool(toDBTool(a)); err != nil {
				return actool.Errorf(locale.ErrorMessage(locale.FromContext(ctx), err)), nil
			}
			return actool.Text("custom tool updated: " + a.Key), nil
		})
}

// ---- MCP ----

type mcpToolInput struct {
	ID        int64           `json:"id"`
	Name      string          `json:"name"`
	Transport string          `json:"transport"`
	Command   string          `json:"command"`
	Args      json.RawMessage `json:"args"`
	Env       json.RawMessage `json:"env"`
	URL       string          `json:"url"`
	Enabled   *bool           `json:"enabled"`
	Insecure  *bool           `json:"insecure"`
}

func mcpSchema(withID bool, langs ...locale.Lang) map[string]any {
	props := map[string]any{
		"name":      strParam(locale.Text(locale.First(langs), "MCP server name")),
		"transport": strParam("stdio | http / sse"),
		"command":   strParam(locale.Text(locale.First(langs), "stdio launch command, such as npx")),
		"args":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": locale.Text(locale.First(langs), "Command argument array")},
		"env":       map[string]any{"type": "object", "description": locale.Text(locale.First(langs), "Environment variables {KEY:VALUE}")},
		"url":       strParam(locale.Text(locale.First(langs), "HTTP/SSE URL")),
		"enabled":   map[string]any{"type": "boolean", "description": locale.Text(locale.First(langs), "Enabled, default true")},
		"insecure":  map[string]any{"type": "boolean", "description": locale.Text(locale.First(langs), "HTTP: skip TLS certificate verification for self-signed certificates; default false")},
	}
	required := []string{"name", "transport"}
	if withID {
		props["id"] = map[string]any{"type": "integer", "description": locale.Text(locale.First(langs), "MCP server ID to modify")}
		required = []string{"id", "name", "transport"}
	}
	return objSchema(props, required...)
}

func (a mcpToolInput) toDB() *db.MCPServer {
	enabled := true
	if a.Enabled != nil {
		enabled = *a.Enabled
	}
	insecure := false
	if a.Insecure != nil {
		insecure = *a.Insecure
	}
	return &db.MCPServer{
		ID: a.ID, Name: a.Name, Transport: a.Transport, Command: a.Command,
		Args: a.Args, Env: a.Env, URL: a.URL, Enabled: enabled, Insecure: insecure,
	}
}

func (s *Server) toolCreateMCP(langs ...locale.Lang) actool.CoreTool {
	return wrTool("create_mcp", locale.Text(locale.First(langs), "Create a stdio/http/sse MCP server. Grant tool visibility to agents after creation."),
		mcpSchema(false, langs...),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a mcpToolInput
			_ = json.Unmarshal(in, &a)
			a.ID = 0
			if strings.TrimSpace(a.Name) == "" || strings.TrimSpace(a.Transport) == "" {
				return actool.Errorf(locale.Text(locale.First(langs), "name and transport are required")), nil
			}
			id, err := s.m.pg.SaveMCP(a.toDB())
			if err != nil {
				return actool.Errorf(locale.ErrorMessage(locale.FromContext(ctx), err)), nil
			}
			return actool.Text(fmt.Sprintf("mcp created: id=%d name=%s", id, a.Name)), nil
		})
}

func (s *Server) toolUpdateMCP(langs ...locale.Lang) actool.CoreTool {
	return wrTool("update_mcp", locale.Text(locale.First(langs), "Modify an existing MCP server by ID."),
		mcpSchema(true, langs...),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a mcpToolInput
			_ = json.Unmarshal(in, &a)
			if a.ID == 0 {
				return actool.Errorf(locale.Text(locale.First(langs), "id is required")), nil
			}
			if _, err := s.m.pg.SaveMCP(a.toDB()); err != nil {
				return actool.Errorf(locale.ErrorMessage(locale.FromContext(ctx), err)), nil
			}
			return actool.Text(fmt.Sprintf("mcp updated: id=%d", a.ID)), nil
		})
}
