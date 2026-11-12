package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/Philocyber/counterweight/codesrc"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/locale"
)

// Change-scoped code reviews: a review limited to a pull request or a base/head
// pair. The clone, its token handling and its hardened git configuration are the
// ones of a whole-repository review; the change is fetched into that clone.

// fetchChange is codesrc.FetchChange, replaceable in tests so they need no network.
var fetchChange = codesrc.FetchChange

// changeReviewMu serializes the check for an existing review of a head commit
// with the creation of the task that would become that review.
var changeReviewMu sync.Mutex

// codeReviewCommand is a parsed /code-review chat command.
type codeReviewCommand struct {
	IsCmd bool
	Repo  codesrc.Repo
	Mode  codesrc.Mode
	// Change is set by owner/repo#123, a pull request URL or owner/repo base..head.
	Change *codesrc.ChangeRequest
	// ChangeSyntax is true when the command used one of those forms, so a
	// mistake in it is reported instead of being left to the agent.
	ChangeSyntax bool
}

const codeReviewUsage = "usage: /code-review <owner/repo | owner/repo#123 | owner/repo base..head> [light|balanced|comprehensive]"

// parseCodeReviewChangeCommand recognises
//
//	/code-review owner/repo [mode]
//	/code-review owner/repo#123 [mode]          (a pull request)
//	/code-review owner/repo base..head [mode]   (a base/head pair)
func parseCodeReviewChangeCommand(msg string) (codeReviewCommand, error) {
	fields := strings.Fields(strings.TrimSpace(msg))
	if len(fields) == 0 || !strings.EqualFold(fields[0], "/code-review") {
		return codeReviewCommand{}, nil
	}
	cmd := codeReviewCommand{IsCmd: true}
	if len(fields) < 2 {
		return cmd, locale.NewError(codeReviewUsage)
	}
	rest := fields[2:]
	rangeArg := ""
	if len(rest) > 0 && strings.Contains(rest[0], "..") {
		rangeArg, rest = rest[0], rest[1:]
	}
	repoText, change, err := codesrc.ParseChangeTarget(fields[1], rangeArg)
	if err != nil {
		cmd.ChangeSyntax = true
		return cmd, err
	}
	cmd.Change, cmd.ChangeSyntax = change, change != nil
	if cmd.Repo, err = codesrc.ParseRepo(repoText); err != nil {
		return cmd, err
	}
	if change != nil && cmd.Repo.Ref != "" {
		return cmd, locale.NewError("A change review names its change; leave the ref out")
	}
	modeStr := ""
	if len(rest) > 0 {
		modeStr = rest[0]
	}
	cmd.Mode, err = codesrc.ParseMode(modeStr)
	return cmd, err
}

// createCodeReviewChange creates a code review task. With a nil change it is
// createCodeReview. Otherwise the review is scoped to the change; with reuse,
// a live review of the same repository and head commit is returned instead of a
// new one (existing is then true). A review that failed does not count: it can
// never produce results, so asking again starts a new one.
func (s *Server) createCodeReviewChange(ctx context.Context, repo codesrc.Repo, mode codesrc.Mode, req *codesrc.ChangeRequest, instructions string, opts db.TaskCreateOptions, reuse bool) (t *Task, existing bool, err error) {
	if req == nil {
		t, err = s.createCodeReview(ctx, repo, mode, instructions, opts)
		return t, false, err
	}
	if repo.Ref != "" {
		return nil, false, locale.NewError("A change review names its change; leave the ref out")
	}
	change := *req
	if err := change.Validate(); err != nil {
		return nil, false, err
	}
	ctx, cancel := context.WithTimeout(ctx, codeReviewCreateTimeout)
	defer cancel()

	pullURL := ""
	if change.PullNumber > 0 {
		pullURL = fmt.Sprintf("https://github.com/%s/pull/%d", repo.Slug(), change.PullNumber)
		if change.Base == "" {
			info, err := s.githubPullInfo(ctx, repo, change.PullNumber)
			if err != nil {
				return nil, false, err
			}
			change.Base = info.Base.Ref
		}
	}
	path, _, err := s.cloneForReview(ctx, repo, mode)
	if err != nil {
		return nil, false, locale.Errorf("Could not clone %s: %w", repo.Slug(), err)
	}
	discard := func() { _ = os.RemoveAll(filepath.Dir(path)) }
	cs, err := s.fetchChangeForReview(ctx, repo, path, change)
	if err != nil {
		discard()
		return nil, false, err
	}
	cs.PullURL = pullURL

	changeReviewMu.Lock()
	defer changeReviewMu.Unlock()
	if reuse {
		if found := s.existingChangeReview(repo.Slug(), cs.HeadCommit, sameResolvedChange(cs)); found != nil {
			discard()
			return found, true, nil
		}
	}
	t, err = s.createChangeReviewFromClone(repo, mode, path, cs, instructions, opts)
	if err != nil {
		discard()
		return nil, false, err
	}
	return t, false, nil
}

// fetchChangeForReview fetches the change anonymously and, like the clone, retries
// with the stored GitHub token when that fails.
func (s *Server) fetchChangeForReview(ctx context.Context, repo codesrc.Repo, dir string, req codesrc.ChangeRequest) (*codesrc.ChangeSet, error) {
	opts := codesrc.FetchChangeOptions{Repo: repo, Dir: dir, Request: req}
	cs, err := fetchChange(ctx, opts)
	if err == nil || ctx.Err() != nil {
		return cs, err
	}
	cred, credErr := s.loadGitHubCredential()
	if credErr != nil {
		return nil, locale.Errorf("%w. If the repository is private, add a GitHub token with read access to it under System settings", err)
	}
	opts.Token = cred.Token
	return fetchChange(ctx, opts)
}

// existingChangeReview returns the newest live review of the same change, skipping
// failed ones and tasks that are gone. The head commit alone does not identify a
// change: the same commit can be reviewed against another base or as part of another
// pull request, and the result (which lines count as changed) differs. same decides
// whether a stored change is the one asked for.
func (s *Server) existingChangeReview(repository, head string, same func(*codesrc.ChangeSet) bool) *Task {
	ids, err := s.m.pg.ChangeReviewTaskIDs(repository, head)
	if err != nil {
		return nil
	}
	for _, id := range ids {
		t, ok := s.m.Task(i64s(id))
		if !ok {
			continue
		}
		if st := s.resolvedTaskStatus(t); st == "failed" || st == "timeout" {
			continue
		}
		target, err := s.m.pg.GetTaskCodeTarget(id)
		if err != nil || target == nil || target.Change == nil || !same(target.Change) {
			continue
		}
		return t
	}
	return nil
}

// sameResolvedChange matches a stored change against a freshly computed one: same pull
// request and same base and merge-base commits.
func sameResolvedChange(cs *codesrc.ChangeSet) func(*codesrc.ChangeSet) bool {
	return func(stored *codesrc.ChangeSet) bool {
		return stored.PullNumber == cs.PullNumber && stored.BaseCommit == cs.BaseCommit && stored.MergeBase == cs.MergeBase
	}
}

// sameRequestedChange matches a stored change against a request that has not been
// cloned yet. Only what the request names can be compared: the pull request and the
// base ref.
func sameRequestedChange(req *codesrc.ChangeRequest) func(*codesrc.ChangeSet) bool {
	return func(stored *codesrc.ChangeSet) bool {
		return stored.PullNumber == req.PullNumber && stored.BaseRef == req.Base
	}
}

// changeLabel names a change for task titles: #123, or base..head.
func changeLabel(c *codesrc.ChangeSet) string {
	if c.PullNumber > 0 {
		return "#" + strconv.Itoa(c.PullNumber)
	}
	short := func(ref string) string {
		if len(ref) == 40 {
			return ref[:12]
		}
		return ref
	}
	return short(c.BaseRef) + ".." + short(c.HeadRef)
}

// createChangeReviewFromClone is createCodeReviewFromClone for a change: the head
// is what the clone holds, and the change is recorded with the target.
func (s *Server) createChangeReviewFromClone(repo codesrc.Repo, mode codesrc.Mode, path string, cs *codesrc.ChangeSet, instructions string, opts db.TaskCreateOptions) (*Task, error) {
	desc := locale.Text(opts.Language, "Code review: ") + repo.Slug() + " " + changeLabel(cs)
	goalRepo := repo
	goalRepo.Ref = cs.HeadRef
	goal := composeCodeReviewGoal(goalRepo, mode, path, opts.Language) +
		locale.Text(opts.Language, " This review is limited to one change, described in the review context. Report problems that the change introduces or exposes.")
	if instructions = strings.TrimSpace(instructions); instructions != "" {
		goal += locale.Text(opts.Language, " Additional operator instructions: ") + instructions
	}
	opts.AssessmentType = db.AssessmentCodeReview
	opts.CodeTarget = &db.TaskCodeTarget{
		Repository: repo.Slug(), URL: "https://github.com/" + repo.Slug(), Ref: cs.HeadRef, CommitSHA: cs.HeadCommit,
		Mode: string(mode), LocalPath: path, Change: cs,
	}
	opts.OperatorRequest = desc + "\n" + goal
	t, err := s.m.CreateTaskWithOptions(desc, goal, opts)
	if err != nil {
		return nil, locale.Errorf("Cloned, but could not create the review task: %w", err)
	}
	if id, err := strconv.ParseInt(t.ID, 10, 64); err == nil {
		s.startScanners(id, path)
	}
	return t, nil
}

// githubPullRequest is the part of a pull request the server needs.
type githubPullRequest struct {
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
}

// githubPullInfo reads a pull request's base branch. It asks anonymously first
// and, if that fails, with the stored GitHub token.
func (s *Server) githubPullInfo(ctx context.Context, repo codesrc.Repo, number int) (*githubPullRequest, error) {
	path := "/repos/" + repo.Slug() + "/pulls/" + strconv.Itoa(number)
	var pr githubPullRequest
	_, err := githubDoOptional(ctx, "", http.MethodGet, path, &pr)
	if err != nil && ctx.Err() == nil {
		if cred, credErr := s.loadGitHubCredential(); credErr == nil {
			pr = githubPullRequest{}
			_, err = githubDoOptional(ctx, cred.Token, http.MethodGet, path, &pr)
		}
	}
	if err != nil {
		return nil, locale.Errorf("Could not read pull request #%d of %s from GitHub. If the repository is private, add a GitHub token with read access to it under System settings", number, repo.Slug())
	}
	probe := codesrc.ChangeRequest{PullNumber: number, Base: pr.Base.Ref}
	if pr.Base.Ref == "" || probe.Validate() != nil {
		return nil, locale.Errorf("GitHub did not return a usable base branch for pull request #%d", number)
	}
	return &pr, nil
}

// githubDoOptional is githubDo with an optional token: without one the request
// is anonymous, which is enough for a public repository.
func githubDoOptional(ctx context.Context, token, method, path string, out any) (http.Header, error) {
	if token != "" {
		return githubDo(ctx, token, method, path, nil, out)
	}
	return githubDoAnonymous(ctx, method, path, out)
}

// githubDoAnonymous calls the GitHub REST API without credentials.
func githubDoAnonymous(ctx context.Context, method, path string, out any) (http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, method, githubAPI+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "Counterweight")
	resp, err := githubHTTP.Do(req)
	if err != nil {
		return nil, locale.Errorf("GitHub request failed: %v", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return resp.Header, locale.Errorf("GitHub %s %s: %d", method, path, resp.StatusCode)
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return resp.Header, err
		}
	}
	return resp.Header, nil
}

// changeRequest is the change scope accepted by the task-creation API.
type changeRequest = codesrc.ChangeRequest
