package intercept

import (
	"encoding/json"
	"strings"

	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/locale"
)

// CommandText is the exact command a standing "this command" grant remembers.
// Tools without a single command string return empty.
func CommandText(tool string, input []byte) string {
	switch tool {
	case "Bash", "shell_open":
		var in struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(input, &in) != nil {
			return ""
		}
		return strings.TrimSpace(in.Command)
	case "shell_send":
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
		// The command is the whole line, never only the piece this call adds,
		// and only when nothing else is sent and all of the line was read.
		if json.Unmarshal(input, &in) != nil || in.Paste != "" || len(in.Keys) > 0 || len(in.Hex) > 0 || in.Unknown {
			return ""
		}
		return strings.TrimSpace(in.Before + in.Text)
	default:
		return ""
	}
}

// GrantAllows reports whether a task grant covers this call.
// An "all" grant covers every tool. A "tool" grant covers that tool name.
// An "input" grant covers the complete argument object for that tool.
// A legacy "command" grant covers one exact command string for that tool.
func GrantAllows(grants []db.TaskToolGrant, tools []string, input []byte) bool {
	_, ok := MatchingGrant(grants, tools, input)
	return ok
}

// MatchingGrant returns the standing approval that covers this call.
// A grant for every tool is reported before a narrower tool, parameter, or command grant.
func MatchingGrant(grants []db.TaskToolGrant, tools []string, input []byte) (db.TaskToolGrant, bool) {
	for _, g := range grants {
		if g.Scope == "all" {
			return g, true
		}
	}
	for _, tool := range tools {
		command := CommandText(tool, input)
		for _, g := range grants {
			switch g.Scope {
			case "tool":
				if g.ToolName == tool {
					return g, true
				}
			case "input":
				if g.ToolName == tool && SameArguments([]byte(g.Command), input) {
					return g, true
				}
			case "command":
				if g.ToolName == tool && command != "" && g.Command == command {
					return g, true
				}
			}
		}
	}
	return db.TaskToolGrant{}, false
}

// GrantRecord is the approval-history label for the standing approval that let a call run.
func GrantRecord(lang locale.Lang, grant db.TaskToolGrant) (name, message string) {
	switch grant.Scope {
	case "all":
		return locale.Text(lang, "Every tool in this task"),
			locale.Text(lang, "Allowed by the standing approval for every tool in this task. Regex interception rules and the asset allowlist still apply.")
	case "tool":
		return locale.Text(lang, "Tool %s for this task", grant.ToolName),
			locale.Text(lang, "Allowed by the standing approval for %s in this task. Regex interception rules and the asset allowlist still apply.", grant.ToolName)
	case "input":
		return locale.Text(lang, "These exact parameters"),
			locale.Text(lang, "Allowed by the standing approval for these exact parameters. Regex interception rules and the asset allowlist still apply.")
	case "command":
		return locale.Text(lang, "This exact command"),
			locale.Text(lang, "Allowed by the standing approval for this exact command. Regex interception rules and the asset allowlist still apply.")
	default:
		return locale.Text(lang, "Task approval grant"),
			locale.Text(lang, "Allowed by a task approval grant")
	}
}

// HumanGrantReason records the choice the operator made on one approval request.
func HumanGrantReason(lang locale.Lang, scope, tool string) string {
	switch scope {
	case "all":
		return locale.Text(lang, "Human allowed every tool for this task")
	case "tool":
		return locale.Text(lang, "Human allowed %s for this task", tool)
	case "input":
		return locale.Text(lang, "Human allowed these exact parameters for this task")
	case "command":
		return locale.Text(lang, "Human allowed this exact command for this task")
	default:
		return locale.Text(lang, "Human allowed this call once")
	}
}
