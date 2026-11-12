package server

import (
	"context"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/Philocyber/counterweight/codesrc"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/locale"
	"github.com/Philocyber/counterweight/report"
)

// Post-merge re-check. Once a fix's
// pull request is merged, the code verifier reads the merged code and says whether
// the finding is still there: confirmed keeps it confirmed, rejected marks it fixed.
// It runs on the operator's click, or on its own when the task verifies
// automatically; merges are noticed by "Check PR" and by a periodic watch.

// pullRequestWatchEvery is how often open pull requests are checked for a merge.
const pullRequestWatchEvery = 5 * time.Minute

// addMergedWorktree checks out the merge commit; replaceable in tests (no network).
var addMergedWorktree = codesrc.AddMergedWorktree

// recheckWorktreePath is the merged-code checkout of one re-check, next to the review clone.
func recheckWorktreePath(clonePath string, recheckRunID int64) string {
	return filepath.Join(filepath.Dir(clonePath), "recheck-"+strconv.FormatInt(recheckRunID, 10))
}

// startMitigationRecheck creates the re-check of a merged fix, checks out the merge
// commit and runs the verifier on it once a slot is free.
func (s *Server) startMitigationRecheck(ctx context.Context, mitigateRunID int64) (*db.FindingRetest, bool, error) {
	m, err := s.m.pg.GetFindingMitigation(ctx, mitigateRunID)
	if err != nil {
		return nil, false, err
	}
	if m == nil || m.PRState != "merged" {
		return nil, false, db.ErrMitigationNotMerged
	}
	f, err := s.m.pg.GetFinding(m.Run.FindingID)
	if err != nil {
		return nil, false, err
	}
	target, err := s.findingCodeReviewTarget(f)
	if err != nil {
		return nil, false, err
	}
	if err := s.agentReady(ctx, db.CodeVerifierAgentKey, codeVerifierTools); err != nil {
		return nil, false, err
	}
	path, ok := githubPullPath(m.PRURL)
	if !ok {
		return nil, false, locale.NewError("The pull request address is not a GitHub pull request")
	}
	tok, err := s.githubToken()
	if err != nil {
		return nil, false, err
	}
	var pr struct {
		MergeCommitSHA string `json:"merge_commit_sha"`
	}
	if _, err := githubDo(ctx, tok, http.MethodGet, path, nil, &pr); err != nil {
		return nil, false, err
	}
	run, conv, created, err := s.m.pg.CreateMitigationRecheck(ctx, mitigateRunID)
	if err != nil || !created {
		return run, false, err
	}
	repo, err := codesrc.ParseRepo(target.review.Repository)
	if err == nil {
		err = addMergedWorktree(ctx, target.clonePath, recheckWorktreePath(target.clonePath, run.ID), repo, pr.MergeCommitSHA, tok)
	}
	if err == nil {
		// The re-check row records the commit it reads, like a fix records its base.
		err = s.m.pg.SetRecheckCommit(ctx, run.ID, pr.MergeCommitSHA)
	}
	if err != nil {
		_ = s.m.pg.FinishFindingRetest(run.ID, "failed", locale.ErrorMessage(locale.FromContext(ctx), err))
		return run, false, err
	}
	lang := locale.FromContext(ctx)
	s.runFindingConversation(conv, run.RecheckMessage(lang), lang, nil)
	return run, true, nil
}

// recheckContext tells the verifier what changed: the merged commit, the pull
// request, the fix that was merged and the original verification.
func (s *Server) recheckContext(ctx context.Context, r *db.FindingRetest) map[string]any {
	out := map[string]any{"meaning": locale.Text(locale.FromContext(ctx), "clone_path is the code after the fix was merged. confirmed: the vulnerability is still present. rejected: the merged code no longer has it. needs_validation: you cannot tell.")}
	if commit, err := s.m.pg.RecheckCommit(ctx, r.ID); err == nil {
		out["merged_commit"] = commit
	}
	if r.ConversationID != nil {
		if m, _, err := s.m.pg.MitigationForConversation(ctx, *r.ConversationID); err == nil && m != nil {
			out["pull_request"] = m.PRURL
			out["merged_fix"] = map[string]any{"summary": m.Run.Summary, "explanation": m.Run.Evidence, "diff": m.Diff}
		}
	}
	if runs, err := s.m.pg.ListFindingRetests(r.FindingID); err == nil {
		for _, v := range runs {
			if v.Kind == db.RetestKindVerify && v.Status == "completed" {
				out["original_verification"] = map[string]any{"verdict": v.Verdict, "summary": v.Summary, "evidence": v.Evidence}
				break
			}
		}
	}
	return out
}

// afterPullRequestMerged starts the re-check on its own when the finding's task
// verifies automatically; otherwise the Mitigation tab offers the button.
func (s *Server) afterPullRequestMerged(mitigateRunID int64, lang locale.Lang) {
	ctx := locale.WithLang(s.ctx, lang)
	m, err := s.m.pg.GetFindingMitigation(ctx, mitigateRunID)
	if err != nil || m == nil {
		return
	}
	f, err := s.m.pg.GetFinding(m.Run.FindingID)
	if err != nil || f == nil || f.TaskID == nil {
		return
	}
	if auto, err := s.m.pg.GetTaskAutomation(*f.TaskID); err != nil || !auto.Verify {
		return
	}
	if _, _, err := s.startMitigationRecheck(ctx, mitigateRunID); err != nil {
		log.Printf("[recheck] fix %d: %v", mitigateRunID, err)
	}
}

// watchPullRequests checks open pull requests for a merge or close every few
// minutes, and once at start so a merge made while the server was down shows at
// once. It only reads the pull request state; without a GitHub token it does nothing.
func (s *Server) watchPullRequests() {
	t := time.NewTicker(pullRequestWatchEvery)
	defer t.Stop()
	for {
		s.refreshOpenPullRequests()
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Server) refreshOpenPullRequests() {
	if _, err := s.githubToken(); err != nil {
		return
	}
	ids, err := s.m.pg.ListOpenPullRequestRuns(s.ctx)
	if err != nil {
		log.Printf("[recheck] list open pull requests: %v", err)
		return
	}
	for _, id := range ids {
		if _, err := s.refreshMitigationPR(locale.WithLang(s.ctx, locale.ServerDefault()), id); err != nil {
			log.Printf("[recheck] refresh fix %d: %v", id, err)
		}
	}
}

// findingLifecycles reads, for the report, what happened to each finding of a
// code review after it was reported. The fix shown is the published attempt (the
// one a pull request carried), else the newest.
func (s *Server) findingLifecycles(ctx context.Context, findings []*db.DBFinding) (map[int64]report.FindingLifecycle, error) {
	out := make(map[int64]report.FindingLifecycle, len(findings))
	for _, f := range findings {
		var l report.FindingLifecycle
		runs, err := s.m.pg.ListFindingRetests(f.ID)
		if err != nil {
			return nil, err
		}
		for _, r := range runs {
			if r.Kind == db.RetestKindVerify && r.Status == "completed" {
				l.Verification, l.VerifiedAt = r.Verdict, r.FinishedAt
				break
			}
		}
		ms, err := s.m.pg.ListFindingMitigations(ctx, f.ID)
		if err != nil {
			return nil, err
		}
		var shown *db.FindingMitigation
		for _, m := range ms {
			if m.PRURL != "" {
				shown = m
				break
			}
		}
		if shown == nil && len(ms) > 0 {
			shown = ms[0]
		}
		if shown != nil {
			l.Fix, l.PRURL, l.PROpenedAt = shown.State, shown.PRURL, shown.PublishedAt
			if shown.PRState != "none" {
				l.PRState = shown.PRState
			}
			if shown.Run != nil {
				rc, err := s.m.pg.LatestCompletedRecheck(ctx, shown.Run.ID)
				if err != nil {
					return nil, err
				}
				if rc != nil {
					l.Recheck, l.RecheckedAt = rc.Verdict, rc.FinishedAt
				}
			}
		}
		out[f.ID] = l
	}
	return out, nil
}
