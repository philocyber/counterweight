package intercept

import (
	"testing"

	"github.com/Philocyber/counterweight/db"
)

func TestGrantAllowsExactCommandToolAndAll(t *testing.T) {
	input := []byte(`{"command":"ls -la"}`)
	other := []byte(`{"command":"whoami"}`)
	grants := []db.TaskToolGrant{{Scope: "command", ToolName: "Bash", Command: "ls -la"}}
	if !GrantAllows(grants, []string{"Bash"}, input) {
		t.Fatal("exact command was not allowed")
	}
	if GrantAllows(grants, []string{"Bash"}, other) {
		t.Fatal("a different command matched the exact grant")
	}
	if GrantAllows(grants, []string{"WebFetch"}, input) {
		t.Fatal("command grant matched another tool")
	}

	tool := []db.TaskToolGrant{{Scope: "tool", ToolName: "Bash"}}
	if !GrantAllows(tool, []string{"Bash"}, other) {
		t.Fatal("tool grant missed Bash")
	}
	if GrantAllows(tool, []string{"WebFetch"}, other) {
		t.Fatal("Bash grant matched WebFetch")
	}

	all := []db.TaskToolGrant{{Scope: "all"}, {Scope: "tool", ToolName: "Bash"}}
	if !GrantAllows(all, []string{"Write"}, []byte(`{}`)) {
		t.Fatal("all-tools grant missed Write")
	}
	matched, ok := MatchingGrant(all, []string{"Bash"}, []byte(`{"command":"pwd"}`))
	if !ok || matched.Scope != "all" {
		t.Fatalf("every-tool approval was hidden by the narrower grant: %+v %v", matched, ok)
	}
}

func TestCommandTextReadsShellFields(t *testing.T) {
	if got := CommandText("Bash", []byte(`{"command":" ls "}`)); got != "ls" {
		t.Fatalf("bash command: %q", got)
	}
	if got := CommandText("shell_send", []byte(`{"text":"dir"}`)); got != "dir" {
		t.Fatalf("shell_send text: %q", got)
	}
	if got := CommandText("WebFetch", []byte(`{"url":"https://example.test"}`)); got != "" {
		t.Fatalf("webfetch command: %q", got)
	}
}
