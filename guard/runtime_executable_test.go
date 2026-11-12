package guard

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/intercept"
	"github.com/Philocyber/counterweight/locale"
)

func TestRuntimeExecutableRequiresHumanDecisionBeforeDispatch(t *testing.T) {
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
	if err := ic.SetEnabledTools([]string{"Bash", "shell_open", "shell_send"}); err != nil {
		t.Fatal(err)
	}
	if err := ic.SetJudgeConfig(intercept.JudgeConfig{Enabled: true, AskTimeoutSeconds: 1, AskTimeoutAction: "allow"}); err != nil {
		t.Fatal(err)
	}
	rule, err := d.CreateInterceptRule("runtime executable fixture", "tool_input", "string", "absent fixture restriction", "deny", "", 1000000, true, false, 0, "deny")
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteInterceptRule(rule.ID)
	ic.Invalidate()
	reviews := 0
	ic.SetReviewer(func(context.Context, int64, string, intercept.ReviewInput) (intercept.Decision, error) {
		reviews++
		return intercept.Decision{Action: "allow"}, nil
	})
	g := NewWithInterceptor(ic)
	for scenario, command := range []string{
		`python -c 'print("fixture")'`,
		`echo fixture`,
		`renamed-client fixture-input`,
		`nmap "$TARGET"`,
		`curl -q --request GET https://fixture.invalid/resource`,
		`make fixture`,
		`"$FIXTURE_PROGRAM" harmless-data`,
		`curl -X "$FIXTURE_METHOD" https://fixture.invalid/resource`,
		`curl -K fixture.conf https://fixture.invalid/resource`,
		`curl https://fixture.invalid/resource`,
		`curl -s -q https://fixture.invalid/resource`,
		`wget https://fixture.invalid/resource`,
		`wget --no-config --config=fixture.conf https://fixture.invalid/resource`,
		`curl -q --variable method=DELETE --expand-request '{{method}}' https://fixture.invalid/resource`,
		`wget --no-config --meth=DELETE https://fixture.invalid/resource`,
		`trap "$HANDLER" EXIT`,
		`trap 'echo "' EXIT`,
		`alias method='curl -q -X '`,
		`alias "$DEFINITION"`,
		`bash -c "$FIXTURE_PROGRAM"`,
		`printf '%s' "$FIXTURE_PROGRAM" | bash`,
		`bash fixture.sh`,
		`eval "$FIXTURE_PROGRAM"`,
		`source fixture.sh`,
		`for fixture in one; do`,
		`bash -c 'echo "'`,
	} {
		for _, lang := range []locale.Lang{locale.En} {
			for _, tool := range []string{"Bash", "shell_open", "shell_send"} {
				for _, response := range []string{"allow", "deny", "no-decision"} {
					t.Run(fmt.Sprintf("%d/%s/%s/%s", scenario, lang, tool, response), func(t *testing.T) {
						taskID := fmt.Sprintf("runtime-executable-%d", time.Now().UnixNano())
						defer d.Exec(`DELETE FROM intercept_pending WHERE task_id=$1`, taskID)
						base, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
						defer cancel()
						var pendingID int64
						ctx := intercept.WithTaskContext(locale.WithLang(base, lang), taskID, "fixture", func(activity db.Activity) {
							if activity.Kind != "intercept_request" {
								return
							}
							var detail struct {
								ID int64 `json:"pending_id"`
							}
							if err := json.Unmarshal([]byte(activity.Detail), &detail); err != nil {
								t.Error(err)
								return
							}
							pendingID = detail.ID
							if response != "no-decision" {
								if err := ic.Decide(pendingID, response == "allow"); err != nil {
									t.Error(err)
								}
							}
						})
						input, _ := json.Marshal(map[string]string{"command": command, "text": command})
						blocked, _, _ := g.Hooks().PreToolUse(ctx, tool, input)
						// A nonexecuting dispatcher stub represents the guarded sink.
						calls := 0
						if !blocked {
							calls++
						}
						want := 0
						if response == "allow" {
							want = 1
						}
						if calls != want || pendingID == 0 {
							t.Fatalf("dispatch without the required human decision: calls=%d pending=%d", calls, pendingID)
						}
						row, err := d.GetInterceptDetail(pendingID)
						if err != nil || row == nil || row.Audit == nil {
							t.Fatalf("approval audit missing: %v", err)
						}
						// PostgreSQL JSONB normalizes whitespace/key order; compare
						// both original field values rather than serialized bytes.
						var recorded map[string]string
						if err := json.Unmarshal(row.ToolInput, &recorded); err != nil {
							t.Fatal(err)
						}
						if row.Audit.InitialAction != "ask" || row.Audit.ModelFallback || row.Audit.ConfigDigest == "" || len(recorded) != 2 || recorded["command"] != command || recorded["text"] != command {
							t.Fatal("original input or platform-review provenance lost")
						}
						if response != "allow" && row.Status != "denied" {
							t.Fatalf("unapproved call retained a permissive status: %s", row.Status)
						}
					})
				}
			}
		}
	}
	if reviews != 0 {
		t.Fatal("fallback reviewer bypassed required human approval")
	}
	if err := ic.SetEnabledTools([]string{}); err != nil {
		t.Fatal(err)
	}
	input := []byte(`{"command":"\"$FIXTURE_PROGRAM\" harmless-data"}`)
	if blocked, _, _ := g.Hooks().PreToolUse(t.Context(), "Bash", input); blocked {
		t.Fatal("operator-disabled interception was re-enabled")
	}
}
