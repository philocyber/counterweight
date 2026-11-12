package db

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Philocyber/counterweight/locale"
)

// Mitigation of confirmed code-review findings. A mitigate run is a codefixer
// conversation that edits a server-owned git worktree; the server computes and
// stores the diff, a patch_review run (patchreviewer) judges it, and only a human
// click publishes it as a pull request. All runs reuse the finding_retests
// conversation binding, sealing and restart recovery.

const (
	CodeFixerAgentKey     = "codefixer"
	PatchReviewerAgentKey = "patchreviewer"

	RetestKindMitigate    = "mitigate"
	RetestKindPatchReview = "patch_review"
	// RetestKindRecheck re-verifies a finding on the code merged by its fix's pull request.
	RetestKindRecheck = "recheck"
)

var (
	ErrFindingNotConfirmed = locale.NewError("Only findings confirmed by independent verification can be fixed")
	ErrMitigationPublished = locale.NewError("This finding already has an open or merged pull request")
	ErrMitigationNotMerged = locale.NewError("Only a fix whose pull request was merged can be re-checked")
)

// FindingMitigation is one fix attempt with its review, as shown in the Mitigation tab.
type FindingMitigation struct {
	Run        *FindingRetest `json:"run"`
	Review     *FindingRetest `json:"review"`
	Branch     string         `json:"branch"`
	BaseCommit string         `json:"base_commit"`
	Diff       string         `json:"diff"`
	Blocked    string         `json:"blocked"`
	PRURL      string         `json:"pr_url"`
	PRState    string         `json:"pr_state"`
	// Recheck is the latest post-merge re-check of this attempt, if any.
	Recheck     *FindingRetest `json:"recheck"`
	PublishedAt *time.Time     `json:"published_at"`
	State       string         `json:"state"`
}

// CreateFindingMitigation queues a fix attempt. The finding must be confirmed and
// carry a completed verify run with verdict confirmed, unless unverifiedOK says the
// operator chose to fix it anyway after being warned it may be a false positive.
// A finding whose latest attempt already has an open or merged pull request is refused.
func (d *DB) CreateFindingMitigation(ctx context.Context, findingID int64, unverifiedOK bool) (*FindingRetest, *Conversation, bool, error) {
	var confirmed bool
	err := d.QueryRowContext(ctx, `SELECT f.status=$2 AND EXISTS(SELECT 1 FROM finding_retests r
		WHERE r.finding_id=f.id AND r.kind=$3 AND r.status='completed' AND r.verdict='confirmed')
		FROM findings f WHERE f.id=$1`, findingID, FindingConfirmed, RetestKindVerify).Scan(&confirmed)
	if err != nil {
		return nil, nil, false, err
	}
	if !confirmed && !unverifiedOK {
		return nil, nil, false, ErrFindingNotConfirmed
	}
	var published bool
	if err := d.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM finding_retests
		WHERE finding_id=$1 AND kind=$2 AND pr_state IN ('open','merged'))`, findingID, RetestKindMitigate).Scan(&published); err != nil {
		return nil, nil, false, err
	}
	if published {
		return nil, nil, false, ErrMitigationPublished
	}
	lang := locale.FromContext(ctx)
	return d.createFindingRun(ctx, findingID, findingRunSpec{
		kind: RetestKindMitigate, agentKey: CodeFixerAgentKey,
		convTitle:   func(title string) string { return locale.Text(lang, "Fix #%d · %s", findingID, title) },
		userSummary: locale.Text(lang, "Please write a fix for finding #%d", findingID),
		message:     func(r *FindingRetest) string { return r.MitigationMessage(lang) },
	})
}

// CreateMitigationRecheck queues the post-merge re-check of a fix whose pull request
// was merged: the verifier reads the merged code and says whether the finding is
// still there (confirmed) or gone (rejected), and the finding status follows.
func (d *DB) CreateMitigationRecheck(ctx context.Context, mitigateRunID int64) (*FindingRetest, *Conversation, bool, error) {
	var findingID int64
	var merged bool
	err := d.QueryRowContext(ctx, `SELECT finding_id, pr_state='merged' FROM finding_retests WHERE id=$1 AND kind=$2`,
		mitigateRunID, RetestKindMitigate).Scan(&findingID, &merged)
	if err != nil {
		return nil, nil, false, err
	}
	if !merged {
		return nil, nil, false, ErrMitigationNotMerged
	}
	lang := locale.FromContext(ctx)
	return d.createFindingRun(ctx, findingID, findingRunSpec{
		kind: RetestKindRecheck, agentKey: CodeVerifierAgentKey, parentID: &mitigateRunID,
		convTitle:   func(title string) string { return locale.Text(lang, "Re-check #%d · %s", findingID, title) },
		userSummary: locale.Text(lang, "Please re-check finding #%d on the merged code", findingID),
		message:     func(r *FindingRetest) string { return r.RecheckMessage(lang) },
	})
}

// RecheckMessage is the built-in first message of a post-merge re-check conversation.
func (r *FindingRetest) RecheckMessage(lang locale.Lang) string {
	return locale.Text(lang, "Re-check code-review finding #%d on the code as merged after its fix. First call get_finding_verification_context: clone_path is the merged code and recheck describes the merged fix. Re-read every location the finding cites, then call record_finding_verification_result once: confirmed if the vulnerability is still present, rejected if the merged code no longer has it, needs_validation if you cannot tell.", r.FindingID)
}

// ListOpenPullRequestRuns returns the fix attempts whose pull request is open.
func (d *DB) ListOpenPullRequestRuns(ctx context.Context) ([]int64, error) {
	rows, err := d.QueryContext(ctx, `SELECT id FROM finding_retests WHERE kind=$1 AND pr_state='open' ORDER BY id`, RetestKindMitigate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// CreatePatchReview queues the independent review of a sealed, unblocked fix.
func (d *DB) CreatePatchReview(ctx context.Context, mitigateRunID int64) (*FindingRetest, *Conversation, bool, error) {
	var findingID int64
	var ok bool
	err := d.QueryRowContext(ctx, `SELECT finding_id, status='completed' AND verdict='patched' AND patch_diff<>'' AND patch_blocked=''
		FROM finding_retests WHERE id=$1 AND kind=$2`, mitigateRunID, RetestKindMitigate).Scan(&findingID, &ok)
	if err != nil {
		return nil, nil, false, err
	}
	if !ok {
		return nil, nil, false, locale.NewError("Only a completed, unblocked fix with a diff can be reviewed")
	}
	lang := locale.FromContext(ctx)
	return d.createFindingRun(ctx, findingID, findingRunSpec{
		kind: RetestKindPatchReview, agentKey: PatchReviewerAgentKey, parentID: &mitigateRunID,
		convTitle:   func(title string) string { return locale.Text(lang, "Patch review #%d · %s", findingID, title) },
		userSummary: locale.Text(lang, "Please review the fix for finding #%d", findingID),
		message:     func(r *FindingRetest) string { return r.PatchReviewMessage(lang) },
	})
}

// MitigationMessage is the built-in first message of a codefixer conversation.
func (r *FindingRetest) MitigationMessage(lang locale.Lang) string {
	return locale.Text(lang, "Write the fix for confirmed code-review finding #%d. First call get_finding_mitigation_context to read the finding, its verification and the worktree path you may edit, change only files inside that worktree, then call record_finding_patch once with patched or cannot_fix.", r.FindingID)
}

// PatchReviewMessage is the built-in first message of a patchreviewer conversation.
func (r *FindingRetest) PatchReviewMessage(lang locale.Lang) string {
	return locale.Text(lang, "Independently review the proposed fix for code-review finding #%d. You did not write it. First call get_patch_review_context to read the finding, the fix explanation, the diff and the worktree path, re-read the changed code, then call record_patch_review once with approve, changes_needed or reject.", r.FindingID)
}

// RecordFindingPatch stages the codefixer's outcome; the diff itself is computed
// by the server from the worktree, never taken from the model.
func (d *DB) RecordFindingPatch(ctx context.Context, conversationID int64, verdict, summary, evidence string) error {
	if verdict != "patched" && verdict != "cannot_fix" {
		return locale.NewError("verdict must be patched / cannot_fix")
	}
	return d.recordFindingRunResult(ctx, conversationID, RetestKindMitigate, verdict, summary, evidence)
}

// RecordPatchReview stages the patchreviewer's verdict.
func (d *DB) RecordPatchReview(ctx context.Context, conversationID int64, verdict, summary, evidence string) error {
	if verdict != "approve" && verdict != "changes_needed" && verdict != "reject" {
		return locale.NewError("verdict must be approve / changes_needed / reject")
	}
	return d.recordFindingRunResult(ctx, conversationID, RetestKindPatchReview, verdict, summary, evidence)
}

// SetMitigationWorkspace records the server-created branch and its base commit.
func (d *DB) SetMitigationWorkspace(ctx context.Context, runID int64, branch, baseCommit string) error {
	_, err := d.ExecContext(ctx, `UPDATE finding_retests SET patch_branch=$2, patch_base_commit=$3 WHERE id=$1 AND kind=$4`,
		runID, branch, baseCommit, RetestKindMitigate)
	return err
}

// SetMitigationDiff stores the server-computed diff, or the reason the guards blocked it.
func (d *DB) SetMitigationDiff(ctx context.Context, runID int64, diff, blocked string) error {
	_, err := d.ExecContext(ctx, `UPDATE finding_retests SET patch_diff=$2, patch_blocked=$3 WHERE id=$1 AND kind=$4`,
		runID, diff, blocked, RetestKindMitigate)
	return err
}

// MarkMitigationPublished records the pull request opened for an approved fix.
// It succeeds once: a second publish of the same run is refused.
func (d *DB) MarkMitigationPublished(ctx context.Context, runID int64, prURL string) error {
	res, err := d.ExecContext(ctx, `UPDATE finding_retests SET pr_url=$2, pr_state='open', published_at=now()
		WHERE id=$1 AND kind=$3 AND pr_state='none'`, runID, prURL, RetestKindMitigate)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrMitigationPublished
	}
	return nil
}

// SetMitigationPRState follows the pull request after publication.
func (d *DB) SetMitigationPRState(ctx context.Context, runID int64, state string) error {
	if state != "open" && state != "merged" && state != "closed" {
		return locale.NewError("invalid pull request state")
	}
	_, err := d.ExecContext(ctx, `UPDATE finding_retests SET pr_state=$2 WHERE id=$1 AND kind=$3 AND pr_url<>''`, runID, state, RetestKindMitigate)
	return err
}

// GetFindingMitigation loads one fix attempt with its latest review.
func (d *DB) GetFindingMitigation(ctx context.Context, runID int64) (*FindingMitigation, error) {
	r, err := scanRetest(d.QueryRowContext(ctx, `SELECT `+retestCols+` FROM finding_retests WHERE id=$1 AND kind=$2`, runID, RetestKindMitigate))
	if err != nil || r == nil {
		return nil, err
	}
	return d.loadMitigation(ctx, r)
}

// ListFindingMitigations returns a finding's fix attempts, newest first.
func (d *DB) ListFindingMitigations(ctx context.Context, findingID int64) ([]*FindingMitigation, error) {
	rows, err := d.QueryContext(ctx, `SELECT `+retestCols+` FROM finding_retests WHERE finding_id=$1 AND kind=$2 ORDER BY id DESC`, findingID, RetestKindMitigate)
	if err != nil {
		return nil, err
	}
	var runs []*FindingRetest
	for rows.Next() {
		r, err := scanRetest(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		runs = append(runs, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := []*FindingMitigation{}
	for _, r := range runs {
		m, err := d.loadMitigation(ctx, r)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

func (d *DB) loadMitigation(ctx context.Context, r *FindingRetest) (*FindingMitigation, error) {
	m := &FindingMitigation{Run: r}
	err := d.QueryRowContext(ctx, `SELECT patch_branch, patch_base_commit, patch_diff, patch_blocked, pr_url, pr_state, published_at
		FROM finding_retests WHERE id=$1`, r.ID).Scan(&m.Branch, &m.BaseCommit, &m.Diff, &m.Blocked, &m.PRURL, &m.PRState, &m.PublishedAt)
	if err != nil {
		return nil, err
	}
	m.Review, err = scanRetest(d.QueryRowContext(ctx, `SELECT `+retestCols+` FROM finding_retests
		WHERE parent_id=$1 AND kind=$2 ORDER BY id DESC LIMIT 1`, r.ID, RetestKindPatchReview))
	if err != nil {
		return nil, err
	}
	m.Recheck, err = scanRetest(d.QueryRowContext(ctx, `SELECT `+retestCols+` FROM finding_retests
		WHERE parent_id=$1 AND kind=$2 ORDER BY id DESC LIMIT 1`, r.ID, RetestKindRecheck))
	if err != nil {
		return nil, err
	}
	m.State = MitigationState(m)
	return m, nil
}

// LatestCompletedRecheck is the newest finished post-merge re-check of a fix
// attempt. A running or failed retry does not hide an earlier verdict.
func (d *DB) LatestCompletedRecheck(ctx context.Context, mitigationID int64) (*FindingRetest, error) {
	return scanRetest(d.QueryRowContext(ctx, `SELECT `+retestCols+` FROM finding_retests
		WHERE parent_id=$1 AND kind=$2 AND status='completed' ORDER BY id DESC LIMIT 1`, mitigationID, RetestKindRecheck))
}

// MitigationState derives the single status the Mitigation tab shows.
func MitigationState(m *FindingMitigation) string {
	r := m.Run
	switch {
	case r.Status == "pending" || r.Status == "running":
		return "drafting"
	case r.Status != "completed":
		return "failed"
	case r.Verdict == "cannot_fix":
		return "cannot_fix"
	case m.Blocked != "":
		return "blocked"
	}
	switch m.PRState {
	case "open":
		return "pr_open"
	case "merged":
		return "merged"
	case "closed":
		return "pr_closed"
	}
	rv := m.Review
	switch {
	case rv == nil || rv.Status == "pending" || rv.Status == "running":
		return "in_review"
	case rv.Status != "completed":
		return "review_failed"
	case rv.Verdict == "approve":
		return "ready"
	case rv.Verdict == "changes_needed":
		return "changes_needed"
	}
	return "rejected"
}

// MitigationForConversation finds the fix attempt a codefixer or patchreviewer
// conversation is bound to (for a review, its parent fix).
func (d *DB) MitigationForConversation(ctx context.Context, conversationID int64) (*FindingMitigation, *FindingRetest, error) {
	run, err := d.FindingRetestForConversation(ctx, conversationID)
	if err != nil || run == nil {
		return nil, run, err
	}
	id := run.ID
	if run.Kind == RetestKindPatchReview || run.Kind == RetestKindRecheck {
		if err := d.QueryRowContext(ctx, `SELECT parent_id FROM finding_retests WHERE id=$1`, run.ID).Scan(&id); err != nil {
			return nil, run, err
		}
	} else if run.Kind != RetestKindMitigate {
		return nil, run, nil
	}
	m, err := d.GetFindingMitigation(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, run, nil
	}
	return m, run, err
}

// SetRecheckCommit records the merged commit a post-merge re-check reads.
func (d *DB) SetRecheckCommit(ctx context.Context, runID int64, commit string) error {
	_, err := d.ExecContext(ctx, `UPDATE finding_retests SET patch_base_commit=$2 WHERE id=$1 AND kind=$3`, runID, commit, RetestKindRecheck)
	return err
}

// RecheckCommit returns the merged commit a re-check reads, "" when not recorded.
func (d *DB) RecheckCommit(ctx context.Context, runID int64) (string, error) {
	var commit string
	err := d.QueryRowContext(ctx, `SELECT patch_base_commit FROM finding_retests WHERE id=$1 AND kind=$2`, runID, RetestKindRecheck).Scan(&commit)
	return commit, err
}
