package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	actool "github.com/Autumn-27/norma/tool"
	"github.com/Philocyber/counterweight/agent"
	"github.com/Philocyber/counterweight/codesrc"
	"github.com/Philocyber/counterweight/config"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/intercept"
	"github.com/Philocyber/counterweight/locale"
	"github.com/Philocyber/counterweight/report"
)

// Mitigation of confirmed code-review findings.
// A human starts a fix; the codefixer edits a server-owned worktree; the server
// computes and guards the diff; the patchreviewer judges it automatically; only a
// human click publishes it as a pull request.

var (
	codeFixerTools     = []string{"get_finding_mitigation_context", "record_finding_patch"}
	patchReviewerTools = []string{"get_patch_review_context", "record_patch_review"}
)

// codeReviewTarget is the code-review task behind a finding: its recorded target
// and the absolute path of the review clone.
type codeReviewTarget struct {
	task      *Task
	review    report.CodeReview
	clonePath string
	change    *codesrc.ChangeSet // The reviewed change; nil for a whole-repository review.
}

func (s *Server) findingCodeReviewTarget(f *db.DBFinding) (*codeReviewTarget, error) {
	if f == nil || f.TaskID == nil {
		return nil, locale.NewError("Fixes are available only for code review findings")
	}
	metadata, err := s.m.pg.GetTaskCodeTarget(*f.TaskID)
	if err != nil {
		return nil, err
	}
	task, found := s.m.Task(strconv.FormatInt(*f.TaskID, 10))
	if metadata == nil || !found {
		return nil, locale.NewError("Fixes are available only for code review findings")
	}
	t := &codeReviewTarget{task: task, clonePath: metadata.LocalPath, review: report.CodeReview{Repository: metadata.Repository, URL: metadata.URL, Ref: metadata.Ref, Commit: metadata.CommitSHA, Mode: metadata.Mode}, change: metadata.Change}
	if t.clonePath == "" {
		t.clonePath = codeReviewClonePath(task.Goal)
	}
	if t.review.Repository == "" || t.clonePath == "" {
		return nil, locale.NewError("Fixes are available only for code review findings")
	}
	if _, err := os.Stat(t.clonePath); err != nil {
		return nil, locale.NewError("The review clone is no longer on disk; run the code review again")
	}
	return t, nil
}

// fixWorktreePath is the deterministic worktree of one fix run, next to the review clone.
func fixWorktreePath(clonePath string, runID int64) string {
	return filepath.Join(filepath.Dir(clonePath), "fix-"+strconv.FormatInt(runID, 10))
}

// startFindingMitigation is the "Generate fix" action.
func (s *Server) startFindingMitigation(ctx context.Context, findingID int64, unverifiedOK bool) (*db.FindingRetest, bool, error) {
	if err := s.findingBudgetCheck(findingID); err != nil {
		return nil, false, err
	}
	f, err := s.m.pg.GetFinding(findingID)
	if err != nil {
		return nil, false, err
	}
	target, err := s.findingCodeReviewTarget(f)
	if err != nil {
		return nil, false, err
	}
	if err := s.agentReady(ctx, db.CodeFixerAgentKey, codeFixerTools); err != nil {
		return nil, false, err
	}
	run, conv, created, err := s.m.pg.CreateFindingMitigation(ctx, findingID, unverifiedOK)
	if err != nil || !created {
		return run, false, err
	}
	branch := codesrc.FixBranchName(findingID, run.ID)
	base, err := codesrc.AddFixWorktree(ctx, target.clonePath, fixWorktreePath(target.clonePath, run.ID), branch)
	if err == nil {
		err = s.m.pg.SetMitigationWorkspace(ctx, run.ID, branch, base)
	}
	if err != nil {
		_ = s.m.pg.FinishFindingRetest(run.ID, "failed", locale.ErrorMessage(locale.FromContext(ctx), err))
		return run, false, err
	}
	lang := locale.FromContext(ctx)
	s.runFindingConversation(conv, run.MitigationMessage(lang), lang, func() { s.afterFindingFix(run.ID, lang) })
	return run, true, nil
}

// afterFindingFix computes the diff of a sealed fix, applies the guards and, when
// the diff passes, starts the independent patch review.
func (s *Server) afterFindingFix(runID int64, lang locale.Lang) {
	ctx := locale.WithLang(s.ctx, lang)
	m, err := s.m.pg.GetFindingMitigation(ctx, runID)
	if err != nil || m == nil || m.Run.Status != "completed" || m.Run.Verdict != "patched" {
		return
	}
	f, err := s.m.pg.GetFinding(m.Run.FindingID)
	if err != nil {
		return
	}
	target, err := s.findingCodeReviewTarget(f)
	if err != nil {
		_ = s.m.pg.SetMitigationDiff(ctx, runID, "", locale.ErrorMessage(lang, err))
		return
	}
	changes, diff, err := codesrc.CollectChanges(ctx, fixWorktreePath(target.clonePath, runID))
	blocked := ""
	switch {
	case err != nil:
		blocked = locale.ErrorMessage(lang, err)
	default:
		blocked = codesrc.GuardPatch(changes, codesrc.RelatedToEvidence(f.Summary+"\n"+f.Evidence+"\n"+f.Report))
		if blocked == "" && s.diffLeaksSecret(diff) {
			blocked = locale.Text(lang, "The fix contains a Counterweight secret (an API key, token or password)")
		}
	}
	if err := s.m.pg.SetMitigationDiff(ctx, runID, diff, blocked); err != nil {
		log.Printf("[mitigate] run %d store diff: %v", runID, err)
		return
	}
	if blocked != "" {
		return
	}
	if err := s.startPatchReview(ctx, runID); err != nil {
		log.Printf("[mitigate] run %d patch review: %v", runID, err)
	}
}

func (s *Server) startPatchReview(ctx context.Context, mitigateRunID int64) error {
	if err := s.agentReady(ctx, db.PatchReviewerAgentKey, patchReviewerTools); err != nil {
		return err
	}
	run, conv, created, err := s.m.pg.CreatePatchReview(ctx, mitigateRunID)
	if err != nil || !created {
		return err
	}
	lang := locale.FromContext(ctx)
	s.runFindingConversation(conv, run.PatchReviewMessage(lang), lang, nil)
	return nil
}

// fixWriteRoot is agent.WriteRoot: the worktree of the running fix bound to the
// conversation in ctx, or "" (no writes) for anything else.
func (s *Server) fixWriteRoot(ctx context.Context) string {
	m, run, err := s.m.pg.MitigationForConversation(ctx, intercept.ConvIDFromContext(ctx))
	if err != nil || m == nil || run == nil || run.Kind != db.RetestKindMitigate || run.Status != "running" {
		return ""
	}
	f, err := s.m.pg.GetFinding(run.FindingID)
	if err != nil {
		return ""
	}
	target, err := s.findingCodeReviewTarget(f)
	if err != nil {
		return ""
	}
	return fixWorktreePath(target.clonePath, run.ID)
}

// diffLeaksSecret reports whether a diff contains one of Counterweight's own
// secrets: the database password, the session signing key, an LLM API key or
// the GitHub token. A fixer that read them must not publish them.
func (s *Server) diffLeaksSecret(diff string) bool {
	var secrets []string
	if dsn, _, err := config.PostgresDSN(); err == nil {
		if u, err := url.Parse(dsn); err == nil {
			if p, ok := u.User.Password(); ok {
				secrets = append(secrets, p)
			}
		}
	}
	if len(s.jwtKey) > 0 {
		secrets = append(secrets, string(s.jwtKey))
	}
	if profiles, err := s.m.pg.ListProfiles(); err == nil {
		for _, p := range profiles {
			if full, err := s.m.pg.ProfileByID(p.ID); err == nil && full != nil {
				secrets = append(secrets, full.APIKey)
			}
		}
	}
	if tok, err := s.githubToken(); err == nil {
		secrets = append(secrets, tok)
	}
	for _, sec := range secrets {
		if sec = strings.TrimSpace(sec); len(sec) >= 8 && strings.Contains(diff, sec) {
			return true
		}
	}
	return false
}

// findingMitigationTools derive the run from the server-owned conversation
// binding; arguments cannot redirect a result to another finding.
func (s *Server) findingMitigationTools(langs ...locale.Lang) (fixer, reviewer []actool.CoreTool) {
	lang := findingToolLanguage(langs)
	result := func(verdicts []string, summary, evidence string) map[string]any {
		return objSchema(map[string]any{
			"verdict":  map[string]any{"type": "string", "enum": verdicts},
			"summary":  strParam(summary),
			"evidence": strParam(evidence),
		}, "verdict", "summary", "evidence")
	}
	ctxTool := func(kind string) func(ctx context.Context, _ json.RawMessage) (actool.Result, error) {
		return func(ctx context.Context, _ json.RawMessage) (actool.Result, error) {
			l := locale.FromContext(ctx)
			m, run, err := s.m.pg.MitigationForConversation(ctx, intercept.ConvIDFromContext(ctx))
			if err != nil {
				return actool.Errorf(locale.ErrorMessage(l, err)), nil
			}
			if m == nil || run == nil || run.Kind != kind {
				return actool.Errorf(locale.Text(l, "This conversation has no linked fix; fixes start from the Mitigation tab")), nil
			}
			f, err := s.m.pg.GetFinding(m.Run.FindingID)
			if err != nil {
				return actool.Errorf(locale.ErrorMessage(l, err)), nil
			}
			target, err := s.findingCodeReviewTarget(f)
			if err != nil {
				return actool.Errorf(locale.ErrorMessage(l, err)), nil
			}
			out := map[string]any{
				"finding":       map[string]any{"id": f.ID, "name": f.Name, "severity": f.Severity, "summary": f.Summary, "evidence": f.Evidence, "report": f.Report},
				"repository":    target.review.Repository,
				"base_commit":   m.BaseCommit,
				"worktree_path": fixWorktreePath(target.clonePath, m.Run.ID),
			}
			noteFindingRunEnded(ctx, out, run)
			if runs, err := s.m.pg.ListFindingRetests(f.ID); err == nil {
				for _, r := range runs {
					if r.Kind == db.RetestKindVerify && r.Status == "completed" {
						out["verification"] = map[string]any{"verdict": r.Verdict, "summary": r.Summary, "evidence": r.Evidence}
						break
					}
				}
			}
			if kind == db.RetestKindPatchReview {
				out["fix"] = map[string]any{"summary": m.Run.Summary, "explanation": m.Run.Evidence, "diff": m.Diff}
			}
			// A new attempt learns why the last reviewed one was not approved.
			if kind == db.RetestKindMitigate {
				if prev, err := s.m.pg.ListFindingMitigations(ctx, f.ID); err == nil {
					for _, p := range prev {
						if p.Run.ID != m.Run.ID && p.Review != nil && p.Review.Status == "completed" && p.Review.Verdict != "approve" {
							out["previous_attempt"] = map[string]any{"fix_summary": p.Run.Summary, "diff": p.Diff,
								"review_verdict": p.Review.Verdict, "review_summary": p.Review.Summary, "review_evidence": p.Review.Evidence}
							break
						}
					}
				}
			}
			return jsonResult(out)
		}
	}
	fixer = []actool.CoreTool{
		roTool("get_finding_mitigation_context", locale.Text(lang, "Read the confirmed code-review finding linked to this fix conversation, its independent verification, the repository and the absolute path of the fix worktree you may edit. Takes no arguments."),
			objSchema(map[string]any{}), ctxTool(db.RetestKindMitigate)),
		wrTool("record_finding_patch", locale.Text(lang, "Save the sole outcome of this fix. patched sends the server-computed diff to an independent review; cannot_fix leaves it for a human. Describe every changed file, why it closes the trace, and the regression test."),
			result([]string{"patched", "cannot_fix"},
				locale.Text(lang, "One-line description of the fix, or why it cannot be fixed here"),
				locale.Text(lang, "Markdown: each changed file and why it closes the trace, the regression test, and anything the maintainer should check; for cannot_fix, what a human must decide")),
			func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
				var a struct{ Verdict, Summary, Evidence string }
				if err := json.Unmarshal(in, &a); err != nil {
					return actool.Errorf(locale.ErrorMessage(locale.FromContext(ctx), err)), nil
				}
				if err := s.m.pg.RecordFindingPatch(ctx, intercept.ConvIDFromContext(ctx), a.Verdict, a.Summary, a.Evidence); err != nil {
					return actool.Errorf(locale.ErrorMessage(locale.FromContext(ctx), err)), nil
				}
				return jsonResult(map[string]any{"saved": true, "verdict": a.Verdict})
			}),
	}
	reviewer = []actool.CoreTool{
		roTool("get_patch_review_context", locale.Text(lang, "Read the finding, its verification, the fixer's explanation, the server-computed diff and the fix worktree path linked to this review conversation. Takes no arguments."),
			objSchema(map[string]any{}), ctxTool(db.RetestKindPatchReview)),
		wrTool("record_patch_review", locale.Text(lang, "Save the sole verdict of this patch review. approve offers a human to open a pull request; changes_needed and reject do not."),
			result([]string{"approve", "changes_needed", "reject"},
				locale.Text(lang, "One-line review conclusion"),
				locale.Text(lang, "Markdown: what you re-read, whether the trace is closed at its root, the regression test, and any unrelated or risky change")),
			func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
				var a struct{ Verdict, Summary, Evidence string }
				if err := json.Unmarshal(in, &a); err != nil {
					return actool.Errorf(locale.ErrorMessage(locale.FromContext(ctx), err)), nil
				}
				if err := s.m.pg.RecordPatchReview(ctx, intercept.ConvIDFromContext(ctx), a.Verdict, a.Summary, a.Evidence); err != nil {
					return actool.Errorf(locale.ErrorMessage(locale.FromContext(ctx), err)), nil
				}
				return jsonResult(map[string]any{"saved": true, "verdict": a.Verdict})
			}),
	}
	return fixer, reviewer
}

// seedCodeMitigation seeds the fixer and reviewer agents and their tools.
func (s *Server) seedCodeMitigation() error {
	fixer, reviewer := s.findingMitigationTools()
	if err := s.seedFindingAgent(findingAgentSeed{
		flag: "code_fixer_seed_v1", key: db.CodeFixerAgentKey, tools: fixer, prompt: agent.CodeFixerDefaultPrompt,
		name: "Code review fixer",
		desc: "Started from the Mitigation tab for a confirmed code review finding; writes a minimal fix and regression test in a dedicated worktree.",
	}); err != nil {
		return err
	}
	if err := s.seedFindingAgent(findingAgentSeed{
		flag: "patch_reviewer_seed_v1", key: db.PatchReviewerAgentKey, tools: reviewer, prompt: agent.PatchReviewerDefaultPrompt,
		name: "Code review patch reviewer",
		desc: "Started automatically after a fix; independently checks that the patch closes the finding without unrelated or risky changes.",
	}); err != nil {
		return err
	}
	if err := s.upgradeFindingAgentPrompt("code_fixer_prompt_v2", db.CodeFixerAgentKey, codeFixerPromptV1SHA256, agent.CodeFixerDefaultPrompt); err != nil {
		return err
	}
	return s.upgradeFindingAgentPrompt("patch_reviewer_prompt_v2", db.PatchReviewerAgentKey, patchReviewerPromptV1SHA256, agent.PatchReviewerDefaultPrompt)
}

// SHA-256 of the first default prompts of the fixer and the patch reviewer.
// Hashes identify the exact stock text without keeping it; any edited byte
// prevents the match.
const (
	codeFixerPromptV1SHA256     = "37680ad12a6ca3ab7171ecdb499e230cfd87d71f07d10b9f77917d2cab080818"
	patchReviewerPromptV1SHA256 = "ff053154f489ee4745d4a0d9c790a4d864a8fbb7553fe006f428572edafb92a4"
)

// upgradeFindingAgentPrompt gives an agent the current default prompt when its
// stored prompt is still exactly an earlier default; an edited prompt is kept.
// seedFindingAgent writes the prompt only when it creates the agent, so older
// databases would otherwise never get the new text. It appends a version like
// a restore, so the old text stays in history. The flag makes it run once: a
// later edit back to the old wording is not replaced again.
func (s *Server) upgradeFindingAgentPrompt(flag, key, oldSHA256, prompt string) error {
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return nil
	}
	a, err := s.m.pg.GetAgentByKey(key)
	if err != nil {
		return err
	}
	if a != nil {
		cur, err := s.m.pg.CurrentPrompt(a.ID)
		if err != nil {
			return err
		}
		if digest := sha256.Sum256([]byte(cur)); hex.EncodeToString(digest[:]) == oldSHA256 {
			if _, err := s.m.pg.ResetPromptToDefault(a.ID, prompt); err != nil {
				return err
			}
		}
	}
	return s.m.pg.SetSetting(flag, "true")
}
