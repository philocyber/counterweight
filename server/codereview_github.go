package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Philocyber/counterweight/codesrc"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/locale"
	"github.com/Philocyber/counterweight/privatefs"
)

// GitHub publishing for approved fixes. The token is entered once in the UI and
// kept in a private credential file (never in the database, the model context or
// logs). Only server code calls GitHub; no agent tool can publish.

// githubAPI is the REST base URL; tests point it at an httptest server.
var githubAPI = "https://api.github.com"

var githubHTTP = &http.Client{Timeout: 30 * time.Second}

// pushFix pushes a fix branch; tests replace it to avoid contacting github.com.
var pushFix = codesrc.PushFix

var errGitHubTokenMissing = locale.NewError("No GitHub token is configured; add one under Settings to open pull requests")

type githubCredential struct {
	Token     string `json:"token"`
	Login     string `json:"login"`
	UserID    int64  `json:"user_id"`
	ExpiresAt string `json:"expires_at,omitempty"`
	SavedAt   string `json:"saved_at"`
}

// githubStatus is the token metadata the UI may see.
type githubStatus struct {
	Configured bool   `json:"configured"`
	Login      string `json:"login,omitempty"`
	Hint       string `json:"hint,omitempty"`
	ExpiresAt  string `json:"expires_at,omitempty"`
	Expired    bool   `json:"expired"`
}

func (s *Server) githubCredentialPath() string { return filepath.Join(s.m.dir, githubCredentialFile) }

func (s *Server) loadGitHubCredential() (*githubCredential, error) {
	raw, err := privatefs.ReadFile(s.githubCredentialPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, errGitHubTokenMissing
	}
	if err != nil {
		return nil, err
	}
	var c githubCredential
	if err := json.Unmarshal(raw, &c); err != nil || c.Token == "" {
		return nil, errGitHubTokenMissing
	}
	return &c, nil
}

func (s *Server) githubToken() (string, error) {
	c, err := s.loadGitHubCredential()
	if err != nil {
		return "", err
	}
	return c.Token, nil
}

func (c *githubCredential) status() githubStatus {
	st := githubStatus{Configured: true, Login: c.Login, ExpiresAt: c.ExpiresAt}
	if n := len(c.Token); n > 4 {
		st.Hint = "…" + c.Token[n-4:]
	}
	if c.ExpiresAt != "" {
		if t, err := parseGitHubExpiry(c.ExpiresAt); err == nil && time.Now().After(t) {
			st.Expired = true
		}
	}
	return st
}

// parseGitHubExpiry reads the github-authentication-token-expiration header
// ("2026-12-01 10:00:00 UTC" or with a numeric offset).
func parseGitHubExpiry(v string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02 15:04:05 MST", "2006-01-02 15:04:05 -0700", time.RFC3339} {
		if t, err := time.Parse(layout, v); err == nil {
			return t, nil
		}
	}
	return time.Time{}, locale.Errorf("unrecognized token expiration %q", v)
}

// githubDo calls the GitHub REST API and decodes a JSON reply into out.
func githubDo(ctx context.Context, token, method, path string, body, out any) (http.Header, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, githubAPI+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "Counterweight")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := githubHTTP.Do(req)
	if err != nil {
		return nil, locale.Errorf("GitHub request failed: %v", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &e)
		return resp.Header, locale.Errorf("GitHub %s %s: %d %s", method, path, resp.StatusCode, e.Message)
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return resp.Header, err
		}
	}
	return resp.Header, nil
}

// saveGitHubToken validates a token against GET /user and stores it privately.
func (s *Server) saveGitHubToken(ctx context.Context, token string) (githubStatus, error) {
	token = strings.TrimSpace(token)
	if token == "" || len(token) > 512 || strings.ContainsAny(token, " \r\n\t") {
		return githubStatus{}, locale.NewError("Enter a valid GitHub token")
	}
	var user struct {
		Login string `json:"login"`
		ID    int64  `json:"id"`
	}
	hdr, err := githubDo(ctx, token, http.MethodGet, "/user", nil, &user)
	if err != nil {
		return githubStatus{}, err
	}
	c := githubCredential{Token: token, Login: user.Login, UserID: user.ID, SavedAt: time.Now().UTC().Format(time.RFC3339)}
	if exp, err := parseGitHubExpiry(hdr.Get("github-authentication-token-expiration")); err == nil {
		c.ExpiresAt = exp.UTC().Format(time.RFC3339)
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return githubStatus{}, err
	}
	if err := privatefs.PublishFile(s.githubCredentialPath(), raw); err != nil {
		return githubStatus{}, err
	}
	return c.status(), nil
}

// publishMitigation commits, pushes and opens the pull request of a fix the
// patch reviewer approved. It runs only on an explicit human request.
func (s *Server) publishMitigation(ctx context.Context, runID int64) (string, error) {
	m, err := s.m.pg.GetFindingMitigation(ctx, runID)
	if err != nil {
		return "", err
	}
	if m == nil || m.State != "ready" {
		return "", locale.NewError("Only a fix approved by the patch review can be published")
	}
	f, err := s.m.pg.GetFinding(m.Run.FindingID)
	if err != nil {
		return "", err
	}
	target, err := s.findingCodeReviewTarget(f)
	if err != nil {
		return "", err
	}
	wt := fixWorktreePath(target.clonePath, runID)
	// Publish exactly what was reviewed: the worktree must still produce that diff.
	if _, diff, err := codesrc.CollectChanges(ctx, wt); err != nil || diff != m.Diff {
		return "", locale.NewError("The fix changed after its review; generate it again")
	}
	if s.diffLeaksSecret(m.Diff) {
		return "", locale.NewError("The fix contains a Counterweight secret (an API key, token or password)")
	}
	cred, err := s.loadGitHubCredential()
	if err != nil {
		return "", err
	}
	if cred.status().Expired {
		return "", locale.NewError("The GitHub token has expired; add a new one under Settings")
	}
	upstream, err := codesrc.ParseRepo(target.review.Repository)
	if err != nil {
		return "", err
	}
	var repo struct {
		DefaultBranch string `json:"default_branch"`
		Permissions   struct {
			Push bool `json:"push"`
		} `json:"permissions"`
	}
	if _, err := githubDo(ctx, cred.Token, http.MethodGet, "/repos/"+upstream.Slug(), nil, &repo); err != nil {
		return "", err
	}
	// The base of the pull request is settled, and the fix proven to sit on commits the
	// upstream already has, before any fork is created or ref pushed.
	base := target.review.Ref
	if target.change != nil {
		if err := checkChangeFixPublishable(ctx, cred.Token, upstream, target.change); err != nil {
			return "", err
		}
		if base = target.change.BaseRef; base == "" {
			base = repo.DefaultBranch
		}
		if _, err := githubDo(ctx, cred.Token, http.MethodGet, "/repos/"+upstream.Slug()+"/branches/"+githubBranchPath(base), nil, nil); err != nil {
			return "", locale.Errorf("The base of the reviewed change, %s, is not a branch of %s", base, upstream.Slug())
		}
	} else if base == "" {
		base = repo.DefaultBranch
	}
	pushTo := upstream
	if !repo.Permissions.Push {
		if pushTo, err = ensureGitHubFork(ctx, cred.Token, upstream); err != nil {
			return "", err
		}
	}
	title := locale.Text(locale.En, "fix(security): %s", firstNonEmpty(f.Name, f.VulnClass, "finding #"+strconv.FormatInt(f.ID, 10)))
	email := fmt.Sprintf("%d+%s@users.noreply.github.com", cred.UserID, cred.Login)
	if _, err := codesrc.CommitFix(ctx, wt, title, cred.Login, email); err != nil {
		return "", err
	}
	if err := pushFix(ctx, wt, pushTo, m.Branch, cred.Token); err != nil {
		return "", err
	}
	var pr struct {
		HTMLURL string `json:"html_url"`
	}
	_, err = githubDo(ctx, cred.Token, http.MethodPost, "/repos/"+upstream.Slug()+"/pulls", map[string]any{
		"title": title, "head": pushTo.Owner + ":" + m.Branch, "base": base,
		"body": mitigationPRBody(f, m), "maintainer_can_modify": true,
	}, &pr)
	if err != nil {
		// The call can fail after GitHub created the pull request. Ask which
		// it was: a pull request that exists is the result, and the pushed
		// branch is removed only when GitHub confirms that none carries it.
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		var created []struct {
			HTMLURL string `json:"html_url"`
		}
		if _, listErr := githubDo(cleanup, cred.Token, http.MethodGet, "/repos/"+upstream.Slug()+"/pulls?state=all&head="+url.QueryEscape(pushTo.Owner+":"+m.Branch), nil, &created); listErr != nil {
			return "", err
		}
		if len(created) == 0 {
			_, _ = githubDo(cleanup, cred.Token, http.MethodDelete, "/repos/"+pushTo.Slug()+"/git/refs/heads/"+githubBranchPath(m.Branch), nil, nil)
			return "", err
		}
		pr.HTMLURL = created[0].HTMLURL
	}
	if pr.HTMLURL == "" {
		return "", locale.NewError("GitHub did not return the pull request")
	}
	if err := s.m.pg.MarkMitigationPublished(ctx, runID, pr.HTMLURL); err != nil {
		return "", err
	}
	return pr.HTMLURL, nil
}

// githubBranchPath escapes a branch name for a URL path, keeping its slashes.
func githubBranchPath(branch string) string {
	return strings.ReplaceAll(url.PathEscape(branch), "%2F", "/")
}

// checkChangeFixPublishable refuses a fix for a reviewed change unless the reviewed head
// consists of commits the upstream repository already holds. The fix branch is built on
// that head, so pushing it would otherwise publish the commits of a pull request from a
// fork, or of any other history that exists only in the review clone, into the repository.
func checkChangeFixPublishable(ctx context.Context, token string, upstream codesrc.Repo, change *codesrc.ChangeSet) error {
	if change.PullNumber > 0 {
		var pr struct {
			Head struct {
				Repo *struct {
					FullName string `json:"full_name"`
				} `json:"repo"`
			} `json:"head"`
		}
		if _, err := githubDo(ctx, token, http.MethodGet, "/repos/"+upstream.Slug()+"/pulls/"+strconv.Itoa(change.PullNumber), nil, &pr); err != nil {
			return err
		}
		if pr.Head.Repo == nil || !strings.EqualFold(pr.Head.Repo.FullName, upstream.Slug()) {
			return locale.NewError("The pull request comes from another repository, so a fix cannot be published on top of its commits")
		}
		return nil
	}
	// A base..head review: the head must be the tip of a branch the upstream has.
	var branch struct {
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	if change.HeadRef == "" || change.HeadCommit == "" {
		return locale.NewError("The reviewed head is not a branch of the repository, so a fix cannot be published on top of it")
	}
	if _, err := githubDo(ctx, token, http.MethodGet, "/repos/"+upstream.Slug()+"/branches/"+githubBranchPath(change.HeadRef), nil, &branch); err != nil || !strings.EqualFold(branch.Commit.SHA, change.HeadCommit) {
		return locale.NewError("The reviewed head is not the current tip of a branch of the repository, so a fix cannot be published on top of it")
	}
	return nil
}

// ensureGitHubFork returns the token owner's fork of upstream, creating it if needed.
func ensureGitHubFork(ctx context.Context, token string, upstream codesrc.Repo) (codesrc.Repo, error) {
	var fork struct {
		FullName string `json:"full_name"`
	}
	if _, err := githubDo(ctx, token, http.MethodPost, "/repos/"+upstream.Slug()+"/forks", map[string]any{"default_branch_only": true}, &fork); err != nil {
		return codesrc.Repo{}, err
	}
	repo, err := codesrc.ParseRepo(fork.FullName)
	if err != nil {
		return codesrc.Repo{}, err
	}
	// Forking is asynchronous: wait until the fork answers before pushing.
	for i := 0; i < 10; i++ {
		if _, err := githubDo(ctx, token, http.MethodGet, "/repos/"+repo.Slug(), nil, nil); err == nil {
			return repo, nil
		}
		select {
		case <-ctx.Done():
			return codesrc.Repo{}, ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	return codesrc.Repo{}, locale.Errorf("the fork %s is not ready yet; try again in a minute", repo.Slug())
}

// refreshMitigationPR follows an open pull request to merged or closed.
func (s *Server) refreshMitigationPR(ctx context.Context, runID int64) (*db.FindingMitigation, error) {
	m, err := s.m.pg.GetFindingMitigation(ctx, runID)
	if err != nil || m == nil || m.PRURL == "" || m.PRState != "open" {
		return m, err
	}
	path, ok := githubPullPath(m.PRURL)
	if !ok {
		return m, nil
	}
	tok, err := s.githubToken()
	if err != nil {
		return m, err
	}
	var pr struct {
		State  string `json:"state"`
		Merged bool   `json:"merged"`
	}
	if _, err := githubDo(ctx, tok, http.MethodGet, path, nil, &pr); err != nil {
		return m, err
	}
	state := "open"
	switch {
	case pr.Merged:
		state = "merged"
	case pr.State == "closed":
		state = "closed"
	}
	if state != m.PRState {
		if err := s.m.pg.SetMitigationPRState(ctx, runID, state); err != nil {
			return m, err
		}
		if state == "merged" {
			go s.afterPullRequestMerged(runID, locale.FromContext(ctx))
		}
		return s.m.pg.GetFindingMitigation(ctx, runID)
	}
	return m, nil
}

// githubPullPath turns https://github.com/o/r/pull/N into /repos/o/r/pulls/N.
func githubPullPath(htmlURL string) (string, bool) {
	rest, ok := strings.CutPrefix(htmlURL, "https://github.com/")
	if !ok {
		return "", false
	}
	p := strings.Split(rest, "/")
	if len(p) != 4 || p[2] != "pull" {
		return "", false
	}
	if _, err := strconv.Atoi(p[3]); err != nil {
		return "", false
	}
	return "/repos/" + p[0] + "/" + p[1] + "/pulls/" + p[3], true
}

// mitigationPRBody is built by the server. Model-written text is length-capped
// and @mentions are neutralized so a pull request never pings anyone.
func mitigationPRBody(f *db.DBFinding, m *db.FindingMitigation) string {
	clip := func(s string, n int) string {
		s = strings.TrimSpace(s)
		if r := []rune(s); len(r) > n {
			s = string(r[:n]) + "…"
		}
		return strings.ReplaceAll(s, "@", "@​")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## Security fix: %s\n\n", clip(firstNonEmpty(f.Name, f.VulnClass), 200))
	fmt.Fprintf(&b, "**Severity:** %s\n\n", nz(db.NormalizeSeverity(f.Severity), "info"))
	fmt.Fprintf(&b, "### Finding\n\n%s\n\n", clip(f.Summary, 2000))
	fmt.Fprintf(&b, "### Evidence\n\n%s\n\n", clip(f.Evidence, 6000))
	fmt.Fprintf(&b, "### Fix\n\n%s\n\n%s\n\n", clip(m.Run.Summary, 500), clip(m.Run.Evidence, 6000))
	if m.Review != nil {
		fmt.Fprintf(&b, "### Independent patch review: %s\n\n%s\n\n", clip(m.Review.Verdict, 20), clip(m.Review.Summary, 1000))
	}
	fmt.Fprintf(&b, "---\nGenerated by Counterweight white-box code review from commit `%s`. A human approved opening this pull request; it still needs your review before merging. The regression test was written but not run by Counterweight.\n", clip(m.BaseCommit, 40))
	return b.String()
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
