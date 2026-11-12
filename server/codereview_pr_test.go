package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Philocyber/counterweight/codesrc"
	"github.com/Philocyber/counterweight/db"
)

func TestParseCodeReviewChangeCommand(t *testing.T) {
	cmd, err := parseCodeReviewChangeCommand("/code-review acme/example#123 light")
	if err != nil || !cmd.IsCmd || cmd.Change == nil || cmd.Change.PullNumber != 123 || cmd.Repo.Slug() != "acme/example" || cmd.Repo.Ref != "" || cmd.Mode != codesrc.ModeLight {
		t.Fatalf("pull request form: %+v %v", cmd, err)
	}
	cmd, err = parseCodeReviewChangeCommand("/code-review https://github.com/acme/example/pull/9")
	if err != nil || cmd.Change == nil || cmd.Change.PullNumber != 9 || cmd.Mode != codesrc.ModeBalanced {
		t.Fatalf("url form: %+v %v", cmd, err)
	}
	cmd, err = parseCodeReviewChangeCommand("/code-review acme/example main..feature/x comprehensive")
	if err != nil || cmd.Change == nil || cmd.Change.Base != "main" || cmd.Change.Head != "feature/x" || cmd.Mode != codesrc.ModeComprehensive {
		t.Fatalf("range form: %+v %v", cmd, err)
	}
	// Plain forms keep their meaning, including a named ref.
	cmd, err = parseCodeReviewChangeCommand("/code-review acme/example#release-1")
	if err != nil || cmd.Change != nil || cmd.Repo.Ref != "release-1" || cmd.ChangeSyntax {
		t.Fatalf("plain ref: %+v %v", cmd, err)
	}
	for _, bad := range []string{"/code-review acme/example#0", "/code-review acme/example a..", "/code-review acme/example#5 a..b", "/code-review acme/example -x..y"} {
		cmd, err := parseCodeReviewChangeCommand(bad)
		if err == nil || !cmd.IsCmd || !cmd.ChangeSyntax {
			t.Errorf("%q: cmd=%+v err=%v, want a reported change-syntax error", bad, cmd, err)
		}
	}
	// A mistake outside the change syntax is not flagged as one (the agent still gets the message).
	if cmd, err := parseCodeReviewChangeCommand("/code-review acme/example super light"); err == nil || cmd.ChangeSyntax {
		t.Errorf("bad mode: %+v %v", cmd, err)
	}
}

// prFixture is a server over a real test database whose clone and fetch steps
// read a local repository instead of GitHub.
type prFixture struct {
	s        *Server
	origin   string
	base     string
	head     string
	clones   atomic.Int32
	gitToken string
}

func prGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-C", dir, "-c", "user.name=f", "-c", "user.email=f@example.invalid", "-c", "commit.gpgsign=false", "-c", "protocol.file.allow=always"}, args...)
	out, err := exec.Command("git", full...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newPRFixture(t *testing.T) *prFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("isolated PostgreSQL unavailable: %v", err)
	}
	t.Cleanup(func() { m.Close() })
	s := newAdmissionTestServer(m, func(*Task) bool { return false })
	s.jwtKey = []byte(strings.Repeat("k", 32))
	f := &prFixture{s: s, origin: t.TempDir()}

	prGit(t, f.origin, "init", "-q", "-b", "main")
	write := func(name, body string) {
		p := filepath.Join(f.origin, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("app/db.py", "a\nb\nc\nd\ne\nf\n")
	write("lib/old.py", "x\ny\n")
	prGit(t, f.origin, "add", "-A")
	prGit(t, f.origin, "commit", "-q", "-m", "init")
	f.base = prGit(t, f.origin, "rev-parse", "HEAD")
	prGit(t, f.origin, "checkout", "-q", "-b", "topic")
	write("app/db.py", "a\nb\nCHANGED\nd\ne\nf\n")
	prGit(t, f.origin, "commit", "-q", "-am", "topic")
	f.head = prGit(t, f.origin, "rev-parse", "HEAD")
	prGit(t, f.origin, "checkout", "-q", "main")

	restoreClone, restoreFetch, restoreAPI := cloneRepository, fetchChange, githubAPI
	t.Cleanup(func() { cloneRepository, fetchChange, githubAPI = restoreClone, restoreFetch, restoreAPI })
	cloneRepository = func(ctx context.Context, opts codesrc.CloneOptions) (string, error) {
		f.clones.Add(1)
		out, err := exec.CommandContext(ctx, "git", "clone", "-q", "--no-single-branch", "file://"+filepath.ToSlash(f.origin), opts.Dest).CombinedOutput()
		if err != nil {
			t.Errorf("clone: %v %s", err, out)
		}
		return opts.Dest, err
	}
	fetchChange = func(ctx context.Context, opts codesrc.FetchChangeOptions) (*codesrc.ChangeSet, error) {
		f.gitToken = opts.Token
		head := "origin/topic"
		if opts.Request.PullNumber == 404 {
			return nil, context.DeadlineExceeded
		}
		hs := prGit(t, opts.Dir, "rev-parse", head)
		bs := prGit(t, opts.Dir, "rev-parse", "origin/"+opts.Request.Base)
		prGit(t, opts.Dir, "checkout", "-q", "--detach", hs)
		c, err := codesrc.ComputeChange(ctx, opts.Dir, bs, hs)
		if c != nil {
			c.BaseRef, c.HeadRef = opts.Request.Base, opts.Request.Head
			if opts.Request.PullNumber > 0 {
				c.HeadRef, c.PullNumber = "pull/"+strconv.Itoa(opts.Request.PullNumber), opts.Request.PullNumber
			}
		}
		return c, err
	}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.EqualFold(r.URL.Path, "/repos/acme/example/pulls/7") {
			_, _ = w.Write([]byte(`{"base":{"ref":"main"},"html_url":"https://github.com/acme/example/pull/7"}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(api.Close)
	githubAPI = api.URL
	return f
}

func (f *prFixture) session(t *testing.T) string {
	t.Helper()
	tok, err := signTestSession(t, f.s)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func (f *prFixture) do(method, path, token string, body any) *httptest.ResponseRecorder {
	var rd *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	} else {
		rd = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, path, rd)
	r.Host = "cw.example.test"
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	f.s.Handler().ServeHTTP(w, r)
	return w
}

func decodeBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("body %q: %v", w.Body.String(), err)
	}
	return out
}

func (f *prFixture) cleanupTask(t *testing.T, id string) {
	n, _ := strconv.ParseInt(id, 10, 64)
	t.Cleanup(func() { _ = f.s.m.pg.DeleteTask(n) })
}

func TestStartReviewIsChangeScopedAndIdempotent(t *testing.T) {
	f := newPRFixture(t)
	sess := f.session(t)

	w := f.do("POST", "/api/reviews", sess, map[string]any{"repository": "acme/example", "pull_number": 7, "depth": "light"})
	if w.Code != 201 {
		t.Fatalf("start: %d %s", w.Code, w.Body.String())
	}
	first := decodeBody(t, w)
	id, _ := first["id"].(string)
	f.cleanupTask(t, id)
	if id == "" || first["task_id"] != id || first["status"] != "queued" || !strings.HasSuffix(first["url"].(string), "/function/tasks/detail/?id="+id) || !strings.HasPrefix(first["url"].(string), "http://cw.example.test") {
		t.Fatalf("start reply: %v", first)
	}

	// The task API returns the change scope without the diff or the clone path.
	tw := f.do("GET", "/api/tasks/"+id, sess, nil)
	var task struct {
		AssessmentType string `json:"assessment_type"`
		CodeTarget     struct {
			CommitSHA string `json:"commit_sha"`
			Change    struct {
				PullNumber int    `json:"pull_number"`
				HeadCommit string `json:"head_commit"`
				BaseRef    string `json:"base_ref"`
				Files      []struct {
					Path string `json:"path"`
				} `json:"files"`
			} `json:"change"`
		} `json:"code_target"`
	}
	if err := json.Unmarshal(tw.Body.Bytes(), &task); err != nil {
		t.Fatal(err)
	}
	if task.AssessmentType != "code_review" || task.CodeTarget.Change.PullNumber != 7 || task.CodeTarget.Change.HeadCommit != f.head || task.CodeTarget.CommitSHA != f.head ||
		task.CodeTarget.Change.BaseRef != "main" || len(task.CodeTarget.Change.Files) != 1 || task.CodeTarget.Change.Files[0].Path != "app/db.py" {
		t.Fatalf("task: %s", tw.Body.String())
	}
	if strings.Contains(tw.Body.String(), "diff --git") || strings.Contains(tw.Body.String(), "local_path") {
		t.Fatalf("task API leaks the diff or the clone path: %s", tw.Body.String())
	}
	goal, _ := decodeBody(t, tw)["goal"].(string)
	if !strings.Contains(goal, "limited to one change") || !strings.Contains(goal, "pull/7") {
		t.Fatalf("goal: %s", goal)
	}

	// Same head again: the existing review, no new task.
	w = f.do("POST", "/api/reviews", sess, map[string]any{"repository": "Acme/Example", "pull_number": 7})
	second := decodeBody(t, w)
	if w.Code != 200 || second["id"] != id || second["existing"] != true {
		t.Fatalf("repeat: %d %v", w.Code, second)
	}
	// A caller that names the head gets it without any clone.
	before := f.clones.Load()
	w = f.do("POST", "/api/reviews", sess, map[string]any{"repository": "acme/example", "pull_number": 7, "head": f.head})
	if w.Code != 200 || decodeBody(t, w)["id"] != id || f.clones.Load() != before {
		t.Fatalf("head hint: %d %s clones %d->%d", w.Code, w.Body.String(), before, f.clones.Load())
	}

	// A failed review does not block a new one for the same head.
	tk, _ := f.s.m.Task(id)
	tk.updateLifecycle(func(s *taskLifecycleState) { s.Status = "failed" })
	w = f.do("POST", "/api/reviews", sess, map[string]any{"repository": "acme/example", "pull_number": 7})
	third := decodeBody(t, w)
	if w.Code != 201 || third["id"] == id {
		t.Fatalf("after failure: %d %v", w.Code, third)
	}
	f.cleanupTask(t, third["id"].(string))
}

func TestStartReviewValidation(t *testing.T) {
	f := newPRFixture(t)
	sess := f.session(t)
	for name, body := range map[string]map[string]any{
		"no change":      {"repository": "acme/example"},
		"base only":      {"repository": "acme/example", "base": "main"},
		"bad repository": {"repository": "https://gitlab.com/a/b", "pull_number": 1},
		"ref in repo":    {"repository": "acme/example#dev", "pull_number": 1},
		"bad depth":      {"repository": "acme/example", "pull_number": 1, "depth": "deep"},
		"bad head hint":  {"repository": "acme/example", "pull_number": 1, "head": "main"},
		"flag as base":   {"repository": "acme/example", "base": "--x", "head": "y"},
	} {
		if w := f.do("POST", "/api/reviews", sess, body); w.Code != 400 {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	if f.clones.Load() != 0 {
		t.Fatal("invalid input was cloned")
	}
	// A pull request the fetch cannot find is a creation failure, with nothing left behind.
	if w := f.do("POST", "/api/reviews", sess, map[string]any{"repository": "acme/example", "pull_number": 404, "base": "main"}); w.Code != 422 {
		t.Errorf("fetch failure: %d %s", w.Code, w.Body.String())
	}
}

func TestReviewResultsSplitByChange(t *testing.T) {
	f := newPRFixture(t)
	sess := f.session(t)
	w := f.do("POST", "/api/reviews", sess, map[string]any{"repository": "acme/example", "base": "main", "head": "topic", "depth": "light"})
	if w.Code != 201 {
		t.Fatalf("start: %d %s", w.Code, w.Body.String())
	}
	id := decodeBody(t, w)["id"].(string)
	f.cleanupTask(t, id)
	n, _ := strconv.ParseInt(id, 10, 64)
	pg := f.s.m.pg
	add := func(class, name, sev, summary, evidence string) {
		if _, err := pg.AddFinding(n, 0, class, name, sev, summary, evidence, "worker", nil); err != nil {
			t.Fatal(err)
		}
	}
	add("sqli", "SQL injection in lookup", "high", "uid reaches SQL. password=hunter2secret from /home/user/src", "app/db.py:3 builds the query")
	add("weak-hash", "Old weak hash", "medium", "unchanged code", "lib/old.py:1 uses md5")
	add("misc", "No location", "low", "somewhere", "no code reference")

	status := decodeBody(t, f.do("GET", "/api/reviews/"+id, sess, nil))
	counts := status["counts"].(map[string]any)
	in, out := counts["in_change"].(map[string]any), counts["outside_change"].(map[string]any)
	// A finding with no readable location cannot be placed, so it counts inside the change.
	if in["high"] != float64(1) || in["low"] != float64(1) || in["total"] != float64(2) || out["medium"] != float64(1) || out["total"] != float64(1) {
		t.Fatalf("counts: %v", counts)
	}
	if status["head_commit"] != f.head || status["status"] != "queued" || status["repository"] != "acme/example" {
		t.Fatalf("status: %v", status)
	}

	// SARIF: all findings by default, only the change on request, locations relative.
	count := func(path string) (int, string) {
		w := f.do("GET", path, sess, nil)
		if w.Code != 200 || w.Header().Get("Content-Type") != "application/sarif+json" {
			t.Fatalf("%s: %d %s", path, w.Code, w.Header().Get("Content-Type"))
		}
		var log struct {
			Runs []struct {
				Results []any `json:"results"`
			} `json:"runs"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &log); err != nil {
			t.Fatal(err)
		}
		return len(log.Runs[0].Results), w.Body.String()
	}
	if n, _ := count("/api/reviews/" + id + "/sarif"); n != 3 {
		t.Fatalf("all results = %d", n)
	}
	n2, raw := count("/api/reviews/" + id + "/sarif?only_change=true")
	if n2 != 2 || !strings.Contains(raw, `"uri": "app/db.py"`) || strings.Contains(raw, "uriBaseId") || strings.Contains(raw, "hunter2secret") || strings.Contains(raw, "/home/user") {
		t.Fatalf("only_change results=%d: %s", n2, raw)
	}
	if w := f.do("GET", "/api/reviews/"+id+"/sarif?only_change=maybe", sess, nil); w.Code != 400 {
		t.Fatalf("bad only_change: %d", w.Code)
	}

	// Markdown summary.
	w = f.do("GET", "/api/reviews/"+id+"/summary", sess, nil)
	md := w.Body.String()
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/markdown") {
		t.Fatalf("summary: %d %s", w.Code, w.Header().Get("Content-Type"))
	}
	for _, want := range []string{"acme/example", "SQL injection in lookup", "`app/db.py:3`", "http://cw.example.test/function/findings/detail?id=", "**Outside the changed lines:** 1", "has not finished"} {
		if !strings.Contains(md, want) {
			t.Errorf("summary missing %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "hunter2secret") || strings.Contains(md, "/home/user") {
		t.Fatalf("summary leaks: %s", md)
	}

	// A whole-repository review and unknown ids are not served here.
	for _, p := range []string{"/api/reviews/999999999", "/api/reviews/abc"} {
		if w := f.do("GET", p, sess, nil); w.Code != 404 {
			t.Errorf("%s: %d", p, w.Code)
		}
	}
}

func TestReviewStateMapping(t *testing.T) {
	for _, tc := range []struct {
		dto            TaskDTO
		status, reason string
	}{
		{TaskDTO{Status: "created"}, "queued", ""},
		{TaskDTO{Status: "queued"}, "queued", ""},
		{TaskDTO{Status: "running"}, "running", ""},
		{TaskDTO{Status: "done"}, "finished", ""},
		{TaskDTO{Status: "failed"}, "failed", ""},
		{TaskDTO{Status: "timeout"}, "failed", "timeout"},
		{TaskDTO{Status: "waiting_approval"}, "waiting", "approval"},
		{TaskDTO{Status: "paused", Budget: TaskBudgetDTO{PausedForBudget: true}}, "waiting", "budget"},
		{TaskDTO{Status: "paused"}, "waiting", "paused"},
		{TaskDTO{Status: "running", LLMFailoverState: "chain_exhausted"}, "waiting", "model"},
	} {
		if st, r := reviewState(tc.dto); st != tc.status || r != tc.reason {
			t.Errorf("%+v: %s/%s want %s/%s", tc.dto, st, r, tc.status, tc.reason)
		}
	}
	_ = db.ScopeReviewsRead
}

func TestTaskCreationAcceptsChangeScope(t *testing.T) {
	f := newPRFixture(t)
	sess := f.session(t)
	w := f.do("POST", "/api/tasks", sess, map[string]any{"assessment_type": "code_review", "repository": "acme/example", "mode": "light",
		"change": map[string]any{"pull_number": 7}})
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	f.cleanupTask(t, body["id"].(string))
	change := body["code_target"].(map[string]any)["change"].(map[string]any)
	if change["pull_number"] != float64(7) || change["head_commit"] != f.head {
		t.Fatalf("change: %v", change)
	}
	for name, req := range map[string]map[string]any{
		"empty change":      {"assessment_type": "code_review", "repository": "acme/example", "change": map[string]any{}},
		"change with a ref": {"assessment_type": "code_review", "repository": "acme/example#dev", "change": map[string]any{"pull_number": 1}},
		"not a review":      {"assessment_type": "general", "description": "x", "change": map[string]any{"pull_number": 1}},
	} {
		if w := f.do("POST", "/api/tasks", sess, req); w.Code != 400 && w.Code != 422 {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
}

func TestFindingsPageMarksInChange(t *testing.T) {
	f := newPRFixture(t)
	sess := f.session(t)
	w := f.do("POST", "/api/reviews", sess, map[string]any{"repository": "acme/example", "base": "main", "head": "topic", "depth": "light"})
	if w.Code != 201 {
		t.Fatalf("start: %d %s", w.Code, w.Body.String())
	}
	id := decodeBody(t, w)["id"].(string)
	f.cleanupTask(t, id)
	n, _ := strconv.ParseInt(id, 10, 64)
	for _, c := range []struct{ name, evidence string }{
		{"inside", "app/db.py:3 builds the query"},
		{"outside", "lib/old.py:1 uses md5"},
	} {
		if _, err := f.s.m.pg.AddFinding(n, 0, "misc", c.name, "low", "s", c.evidence, "worker", nil); err != nil {
			t.Fatal(err)
		}
	}
	w = f.do("GET", "/api/exploration/findings?task_id="+id+"&page=1&limit=20", sess, nil)
	var page struct {
		Items []struct {
			Name     string `json:"name"`
			InChange *bool  `json:"in_change"`
		} `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || len(page.Items) != 2 {
		t.Fatalf("page: %v %s", err, w.Body.String())
	}
	for _, it := range page.Items {
		want := it.Name == "inside"
		if it.InChange == nil || *it.InChange != want {
			t.Errorf("%s: in_change = %v, want %v", it.Name, it.InChange, want)
		}
	}
	// A task that is not change-scoped carries no in_change field at all.
	raw, _ := json.Marshal(FindingDTO{})
	if strings.Contains(string(raw), "in_change") {
		t.Errorf("unset in_change was serialized: %s", raw)
	}
	other := f.do("GET", "/api/exploration/findings?task_id=999999&page=1&limit=20", sess, nil)
	if strings.Contains(other.Body.String(), "in_change") {
		t.Errorf("unknown task: %s", other.Body.String())
	}
}
