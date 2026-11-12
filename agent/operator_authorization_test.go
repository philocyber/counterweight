package agent

import (
	"strings"
	"testing"

	"github.com/Philocyber/counterweight/db"
)

func TestStandingApprovalNoteOverridesWaitingFacts(t *testing.T) {
	if standingApprovalNote("assess https://api.example.test", nil) != "" {
		t.Fatal("missing every-tool approval must not claim one")
	}
	if standingApprovalNote("assess https://api.example.test", []db.TaskToolGrant{{Scope: "tool", ToolName: "WebFetch"}}) != "" {
		t.Fatal("one tool approval must not be described as every tool")
	}
	note := standingApprovalNote("assess https://api.example.test", []db.TaskToolGrant{{Scope: "all"}})
	for _, part := range []string{
		"already approved every tool",
		"does not require a read grant",
		"do not record another fact",
		"asset allowlist still apply",
		"https://api.example.test",
	} {
		if !strings.Contains(note, part) {
			t.Fatalf("standing approval note missing %q", part)
		}
	}
}

func TestOperatorAuthorizationPromptPreservesHumanLimits(t *testing.T) {
	dsn, _, err := db.DSN()
	if err != nil {
		t.Skip("requires disposable PostgreSQL")
	}
	d, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	task, err := d.CreateTaskWithOptions("authorization prompt fixture", "test", db.TaskCreateOptions{OperatorRequest: "Assess https://api.example.test only; no production writes; max 2 requests/sec"})
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(task.ID)
	prompt := taskOperatorAuthorizationPrompt(d.Exploration(task.ExplorationID), task.ID)
	for _, part := range []string{"already authorized", "due diligence", "api.example.test", "no production writes", "max 2 requests/sec"} {
		if !strings.Contains(prompt, part) {
			t.Fatalf("lost original user instructions: %s", part)
		}
	}
	if strings.Contains(prompt, "fixture-token") {
		t.Fatal("authorization context leaked credentials")
	}
}
