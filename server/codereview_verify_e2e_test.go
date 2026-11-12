package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/norma/llm"
	actool "github.com/Autumn-27/norma/tool"
	"github.com/Philocyber/counterweight/agent"
	"github.com/Philocyber/counterweight/codesrc"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/locale"
	"github.com/Philocyber/counterweight/report"
)

// TestCodeReviewVerificationEndToEnd drives the whole verification pipeline with
// only the model replaced by a script: a code review task records two findings
// through the task recorder, each starts its own codeverifier conversation, the
// scripted model calls the real tools (reading the cited file from the clone path
// the context tool returns), and the verdicts set the finding status. A pentest
// task's finding must not be verified. Set COUNTERWEIGHT_CODEREVIEW_CLONE=owner/repo
// to review a real GitHub clone instead of the local fixture tree.
func TestCodeReviewVerificationEndToEnd(t *testing.T) {
	if os.Getenv("COUNTERWEIGHT_CODEREVIEW_E2E") != "1" {
		t.Skip("opt in with COUNTERWEIGHT_CODEREVIEW_E2E=1 against a disposable database")
	}
	dsn, _, err := db.DSN()
	if err != nil {
		t.Skip("test postgres not configured")
	}
	pg, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{ctx: ctx, m: &Manager{pg: pg, dir: t.TempDir(), tasks: map[string]*Task{}},
		chatBusy: map[string]bool{}, chatCancel: map[string]context.CancelCauseFunc{}}
	if err := s.seedCodeVerifier(); err != nil {
		t.Fatal(err)
	}

	// Source under review: a real clone when requested, else a local tree.
	repo, _ := codesrc.ParseRepo("acme/fixture")
	src, cited := filepath.Join(t.TempDir(), "src"), "app/handler.go"
	if slug := os.Getenv("COUNTERWEIGHT_CODEREVIEW_CLONE"); slug != "" {
		if repo, err = codesrc.ParseRepo(slug); err != nil {
			t.Fatal(err)
		}
		if src, err = codesrc.Clone(ctx, codesrc.CloneOptions{Repo: repo, Mode: codesrc.ModeLight, Dest: src}); err != nil {
			t.Fatal(err)
		}
		cited = "README"
		if _, err := os.Stat(filepath.Join(src, cited)); err != nil {
			cited = "README.md"
		}
	} else {
		if err := os.MkdirAll(filepath.Join(src, "app"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(src, cited), []byte("package app\n\nquery := \"SELECT * FROM users WHERE id=\" + r.URL.Query().Get(\"id\")\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	commit, _ := codesrc.HeadCommit(ctx, src)

	review, err := s.m.CreateTaskWithOptions("Code review: "+repo.Slug(), composeCodeReviewGoal(repo, codesrc.ModeLight, src, locale.En), db.TaskCreateOptions{Language: locale.En})
	if err != nil {
		t.Fatal(err)
	}
	pentest, err := s.m.CreateTaskWithOptions("pentest fixture", "test https://example.invalid", db.TaskCreateOptions{Language: locale.En})
	if err != nil {
		t.Fatal(err)
	}
	reviewID, _ := strconv.ParseInt(review.ID, 10, 64)
	pentestID, _ := strconv.ParseInt(pentest.ID, 10, 64)
	s.recordCodeReviewTarget(review.ID, report.CodeReview{Repository: repo.Slug(), Commit: commit, Mode: string(codesrc.ModeLight)})
	// Automatic verification is opt-in per task; the pentest task opts in too, to
	// prove only code review findings are ever verified.
	for _, id := range []int64{reviewID, pentestID} {
		if err := pg.SetTaskAutomation(id, db.TaskAutomation{Verify: true}); err != nil {
			t.Fatal(err)
		}
	}

	oldAugment, oldResolve, oldPrompt := agent.ToolAugment, agent.ToolResolve, agent.PromptOverride
	agent.ToolAugment = func(context.Context, string) ([]actool.CoreTool, agent.DeferredInfo, func()) {
		return s.findingVerificationTools(), agent.DeferredInfo{}, func() {}
	}
	agent.ToolResolve = nil
	agent.PromptOverride = func(string) (string, bool) { return agent.CodeVerifierDefaultPrompt, true }
	var findings []int64
	t.Cleanup(func() {
		cancel()
		waitRetestIdle(t, s)
		agent.ToolAugment, agent.ToolResolve, agent.PromptOverride = oldAugment, oldResolve, oldPrompt
		for _, fid := range findings {
			_, _ = pg.Exec(`DELETE FROM conversations WHERE id IN (SELECT conversation_id FROM finding_retests WHERE finding_id=$1)`, fid)
			_, _ = pg.DeleteFinding(fid)
		}
		_, _ = pg.Exec(`DELETE FROM settings WHERE key=$1`, db.TaskCodeReviewKey(reviewID))
		_ = pg.DeleteTask(reviewID)
		_ = pg.DeleteTask(pentestID)
		pg.Close()
	})

	verdictFor := map[int64]string{}
	findingRef := regexp.MustCompile(`finding #(\d+)`)
	setRetestProvider(s, retestProvider{complete: func(_ context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
		raw, _ := json.Marshal(req)
		if !strings.Contains(string(raw), "You did not write the finding") {
			return llm.Message{}, "", llm.Usage{}, fmt.Errorf("verifier prompt missing from the request")
		}
		var m []string
		for _, msg := range req.Messages {
			for _, b := range msg.Content {
				if m == nil && b.Type == llm.BlockText {
					m = findingRef.FindStringSubmatch(b.Text)
				}
			}
		}
		if m == nil {
			return llm.Message{}, "", llm.Usage{}, fmt.Errorf("first message names no finding")
		}
		fid, _ := strconv.ParseInt(m[1], 10, 64)
		last := req.Messages[len(req.Messages)-1]
		var result string
		for _, b := range last.Content {
			if b.Type == llm.BlockToolResult && len(b.Content) > 0 {
				result = b.Content[0].Text
			}
		}
		switch {
		case result == "":
			return toolUse("ctx", "get_finding_verification_context", `{}`), "tool_use", llm.Usage{}, nil
		case strings.Contains(result, `"clone_path"`):
			var c struct {
				ClonePath string `json:"clone_path"`
			}
			if err := json.Unmarshal([]byte(result), &c); err != nil || c.ClonePath != src {
				return llm.Message{}, "", llm.Usage{}, fmt.Errorf("clone_path=%q want %q (%v)", c.ClonePath, src, err)
			}
			body, err := os.ReadFile(filepath.Join(c.ClonePath, cited))
			if err != nil {
				return llm.Message{}, "", llm.Usage{}, err
			}
			in, _ := json.Marshal(map[string]string{"verdict": verdictFor[fid], "summary": "scripted " + verdictFor[fid],
				"evidence": fmt.Sprintf("Re-read %s:1 (%d bytes) under %s.", cited, len(body), c.ClonePath)})
			return toolUse("rec", "record_finding_verification_result", string(in)), "tool_use", llm.Usage{}, nil
		default:
			return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{llm.TextBlock("Verdict saved")}}, "end_turn", llm.Usage{}, nil
		}
	}})

	record := func(taskID, expID int64, name, verdict string) int64 {
		t.Helper()
		out, err := s.taskFindingRecorder().Record(t.Context(), db.RecordFindingInput{TaskID: taskID, ExplorationID: expID,
			Worker: "e2e", VulnClass: "TEST", Name: name, Severity: "high", Summary: name, Evidence: cited + ":1 tainted input reaches sink"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		findings = append(findings, out.FindingID)
		verdictFor[out.FindingID] = verdict
		return out.FindingID
	}
	confirmed := record(reviewID, review.ExpID, "E2E confirmed candidate", "confirmed")
	rejected := record(reviewID, review.ExpID, "E2E rejected candidate", "rejected")
	pentestFinding := record(pentestID, pentest.ExpID, "E2E pentest finding", "")

	deadline := time.Now().Add(20 * time.Second)
	for _, tc := range []struct {
		fid           int64
		verdict, want string
	}{{confirmed, "confirmed", db.FindingConfirmed}, {rejected, "rejected", db.FindingFalsePositive}} {
		var runs []*db.FindingRetest
		for time.Now().Before(deadline) {
			runs, _ = pg.ListFindingRetests(tc.fid)
			if len(runs) == 1 && runs[0].Status != "pending" && runs[0].Status != "running" {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if len(runs) != 1 || runs[0].Kind != db.RetestKindVerify || runs[0].Status != "completed" || runs[0].Verdict != tc.verdict {
			if len(runs) > 0 {
				t.Fatalf("finding %d run=%+v", tc.fid, *runs[0])
			}
			t.Fatalf("finding %d has no runs", tc.fid)
		}
		conv, err := pg.GetConversation(*runs[0].ConversationID)
		if err != nil || conv == nil || conv.AgentKey != db.CodeVerifierAgentKey {
			t.Fatalf("conversation=%+v err=%v", conv, err)
		}
		f, err := pg.GetFinding(tc.fid)
		if err != nil || f.Status != tc.want || !strings.Contains(f.Evidence, "tainted input") {
			t.Fatalf("finding=%+v err=%v want status %s", f, err, tc.want)
		}
		t.Logf("finding #%d → verify run #%d (conversation #%d, agent %s): %s, finding status %s; evidence: %s",
			tc.fid, runs[0].ID, conv.ID, conv.AgentKey, runs[0].Verdict, f.Status, runs[0].Evidence)
	}
	time.Sleep(300 * time.Millisecond)
	if runs, _ := pg.ListFindingRetests(pentestFinding); len(runs) != 0 {
		t.Fatalf("pentest finding was verified: %+v", runs)
	}
	t.Logf("pentest finding #%d: no verification (expected); reviewed %s at %s", pentestFinding, repo.Slug(), src)
}

func toolUse(id, name, input string) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.BlockToolUse, ID: id, Name: name, Input: json.RawMessage(input)}}}
}
