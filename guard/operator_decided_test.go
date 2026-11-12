package guard

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/intercept"
)

// A tool that asks a person about every call itself is left to that question even when
// it is configured for interception: the gate does not review it, ask for it or use up
// an approval for it. An explicit denial still applies.
func TestOperatorDecidedToolsAreLeftToTheirOwnQuestion(t *testing.T) {
	dsn, _, err := db.DSN()
	if err != nil {
		t.Skip("requires disposable PostgreSQL")
	}
	d, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ic := intercept.New(d)
	tools, err := ic.GetEnabledTools()
	if err != nil {
		t.Fatal(err)
	}
	config := ic.GetJudgeConfig()
	defer func() { _ = ic.SetEnabledTools(tools); _ = ic.SetJudgeConfig(config) }()
	if err := ic.SetEnabledTools([]string{"create_mcp"}); err != nil {
		t.Fatal(err)
	}
	if err := ic.SetJudgeConfig(intercept.JudgeConfig{Enabled: true, AskTimeoutSeconds: 1, AskTimeoutAction: "deny"}); err != nil {
		t.Fatal(err)
	}
	reviews := 0
	ic.SetReviewer(func(context.Context, int64, string, intercept.ReviewInput) (intercept.Decision, error) {
		reviews++
		return intercept.Decision{Action: "ask"}, nil
	})
	g := NewWithInterceptor(ic)
	taskID := fmt.Sprintf("operator-decided-%d", time.Now().UnixNano())
	defer d.Exec(`DELETE FROM intercept_pending WHERE task_id=$1`, taskID)
	ctx := intercept.WithTaskContext(t.Context(), taskID, "fixture", func(db.Activity) {})
	input := []byte(`{"name":"fixture","transport":"stdio","command":"operator-decided-fixture"}`)

	if blocked, _, _ := g.Hooks().PreToolUse(ctx, "create_mcp", input); blocked {
		t.Fatal("the gate decided a call that the tool itself puts to a person")
	}
	var pending int
	if err := d.QueryRow(`SELECT count(*) FROM intercept_pending WHERE task_id=$1`, taskID).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if reviews != 0 || pending != 0 {
		t.Fatalf("the gate handled the call as well: reviews=%d records=%d", reviews, pending)
	}

	rule, err := d.CreateInterceptRule("operator decided fixture", "tool_input", "string", "operator-decided-fixture", "deny", "", 1000000, true, false, 0, "deny")
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteInterceptRule(rule.ID)
	ic.Invalidate()
	if blocked, _, _ := g.Hooks().PreToolUse(ctx, "create_mcp", input); !blocked {
		t.Fatal("an explicit denial no longer applies")
	}
}
