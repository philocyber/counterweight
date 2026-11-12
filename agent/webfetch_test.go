package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/guard"
	"github.com/Philocyber/counterweight/intercept"
)

// webFetchHooks records every reviewed call and refuses one URL.
type webFetchHooks struct {
	mu     sync.Mutex
	refuse string
	inputs []string
}

func (h *webFetchHooks) PreToolUse(_ context.Context, name string, input []byte) (bool, string, []byte) {
	var in struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(input, &in)
	h.mu.Lock()
	h.inputs = append(h.inputs, name+" "+string(input))
	h.mu.Unlock()
	if in.URL == h.refuse {
		return true, "fixture refusal", nil
	}
	return false, "", nil
}
func (*webFetchHooks) PostToolUse(context.Context, string, []byte, []byte, bool) {}
func (*webFetchHooks) Stop(context.Context, []llm.Message) (bool, []string, string) {
	return false, nil, ""
}

// webFetchInSession lets a session make one WebFetch call, the way an agent
// does, and returns the result the agent receives.
func webFetchInSession(t *testing.T, ctx context.Context, opts agentcore.Options, input string) db.Activity {
	t.Helper()
	opts.Provider = oneToolCall("fetch-call", "WebFetch", input)
	opts.Tools = []actool.CoreTool{}
	opts.EnableWebFetch, opts.DisableBackgroundTasks = true, true
	opts.PermissionMode, opts.WorkingDir, opts.MaxTurns = permission.ModeBypass, t.TempDir(), 2
	var results []db.Activity
	if _, _, err := captureRun(ctx, opts, "fixture", func(a db.Activity) {
		if a.Kind == "tool_result" {
			results = append(results, a)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d tool results", len(results))
	}
	return results[0]
}

func webFetchInput(address string) string {
	b, _ := json.Marshal(map[string]any{"url": address, "extract": true})
	return string(b)
}

const webFetchFixturePage = `<html><head><title>Fixture</title><meta name="generator" content="fixture 1.0">
<script src="/static/app.js"></script><script>fetch("/api/v1/users")</script></head>
<body><!-- staging note --><h1>Heading</h1><p>Some <b>bold</b> text and a <a href="/next">link</a>.</p>
<form method="post" action="/login"><input name="user"><input type="hidden" name="csrf" value="token-1"></form>
</body></html>`

// A redirect is reviewed as a WebFetch call to the new location before any
// request goes there. A refused one ends the call and names the location; an
// allowed one returns the page exactly as the SDK renders it.
func TestWebFetchReviewsEachRedirectBeforeFollowingIt(t *testing.T) {
	var landings atomic.Int32
	landing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		landings.Add(1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Server", "fixture-server")
		w.Header().Set("X-Powered-By", "fixture-engine")
		_, _ = w.Write([]byte(webFetchFixturePage))
	}))
	defer landing.Close()
	second := landing.URL + "/internal/page?a=1&b=<2>"
	start := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", second)
		w.WriteHeader(http.StatusFound)
	}))
	defer start.Close()
	first := start.URL + "/start"
	reviewed := []string{"WebFetch " + webFetchInput(first), `WebFetch {"extract":true,"url":"` + second + `"}`}

	t.Run("refused", func(t *testing.T) {
		hooks := &webFetchHooks{refuse: second}
		result := webFetchInSession(t, t.Context(), agentcore.Options{Hooks: hooks}, webFetchInput(first))
		if !result.IsError || !strings.Contains(result.Detail, second) || !strings.Contains(result.Detail, "fixture refusal") {
			t.Fatalf("refused redirect not reported: %+v", result)
		}
		if landings.Load() != 0 {
			t.Fatal("the refused location was requested")
		}
		if !reflect.DeepEqual(hooks.inputs, reviewed) {
			t.Fatalf("reviewed %q", hooks.inputs)
		}
	})
	t.Run("allowed", func(t *testing.T) {
		hooks := &webFetchHooks{}
		result := webFetchInSession(t, t.Context(), agentcore.Options{Hooks: hooks}, webFetchInput(first))
		if !reflect.DeepEqual(hooks.inputs, reviewed) {
			t.Fatalf("reviewed %q", hooks.inputs)
		}
		sdk, err := actool.NewWebFetch(actool.WebFetchConfig{}).Call(t.Context(), json.RawMessage(webFetchInput(second)), nil)
		if err != nil || sdk.IsError || !strings.Contains(sdk.Flatten(), "=== RECON ===") {
			t.Fatalf("SDK fixture: %v %s", err, sdk.Flatten())
		}
		if result.IsError || result.Detail != sdk.Flatten() {
			t.Fatalf("output differs from the SDK's for the final page:\n%s\n---\n%s", result.Detail, sdk.Flatten())
		}
		if !strings.HasPrefix(result.Detail, "[200 OK] "+second+"\nServer: fixture-server\nX-Powered-By: fixture-engine\n") {
			t.Fatalf("first lines: %q", result.Detail)
		}
	})
	t.Run("without hooks", func(t *testing.T) {
		before := landings.Load()
		result := webFetchInSession(t, t.Context(), agentcore.Options{}, webFetchInput(first))
		if result.IsError || landings.Load() != before+1 || !strings.HasPrefix(result.Detail, "[200 OK] "+second+"\n") {
			t.Fatalf("unguarded session: %+v", result)
		}
	})
}

// With the real guard, the redirect is judged like a direct WebFetch call to
// the new location, and its record belongs to the same tool call as the first.
func TestWebFetchRedirectGoesThroughTheGuardAndIsRecorded(t *testing.T) {
	d := testDB(t)
	t.Cleanup(func() { _ = d.Close() })
	ic := intercept.New(d)
	tools, err := ic.GetEnabledTools()
	if err != nil {
		t.Fatal(err)
	}
	config := ic.GetJudgeConfig()
	t.Cleanup(func() { _ = ic.SetEnabledTools(tools); _ = ic.SetJudgeConfig(config) })
	if err := ic.SetEnabledTools([]string{"WebFetch"}); err != nil {
		t.Fatal(err)
	}
	if err := ic.SetJudgeConfig(intercept.JudgeConfig{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	var landings atomic.Int32
	landing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		landings.Add(1)
		_, _ = w.Write([]byte("landing"))
	}))
	defer landing.Close()
	second := landing.URL + "/internal?a=1&b=2"
	start := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, second, http.StatusMovedPermanently)
	}))
	defer start.Close()
	var reviewed []string
	ic.SetReviewer(func(_ context.Context, _ int64, _ string, in intercept.ReviewInput) (intercept.Decision, error) {
		reviewed = append(reviewed, in.Tool+" "+string(in.Arguments))
		if strings.Contains(string(in.Arguments), "/internal") {
			return intercept.Decision{Action: "deny", Message: "fixture verdict"}, nil
		}
		return intercept.Decision{Action: "allow"}, nil
	})
	taskID := "webfetch-redirect-review"
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM intercept_pending WHERE task_id=$1`, taskID) })
	ctx := intercept.WithTaskContext(t.Context(), taskID, "fixture", nil)
	input := webFetchInput(start.URL + "/start")
	result := webFetchInSession(t, ctx, agentcore.Options{Hooks: guard.NewWithInterceptor(ic).Hooks()}, input)
	if !result.IsError || !strings.Contains(result.Detail, second) || !strings.Contains(result.Detail, "fixture verdict") || landings.Load() != 0 {
		t.Fatalf("denied redirect: landings=%d result=%+v", landings.Load(), result)
	}
	if want := []string{"WebFetch " + input, `WebFetch {"extract":true,"url":"` + second + `"}`}; !reflect.DeepEqual(reviewed, want) {
		t.Fatalf("reviewed %q, want %q", reviewed, want)
	}
	rows, err := d.ListTaskIntercepts(taskID)
	if err != nil || len(rows) != 2 {
		t.Fatalf("audit rows %d %v", len(rows), err)
	}
	statuses := map[string]string{}
	for _, row := range rows {
		detail, err := d.GetInterceptDetail(row.ID)
		if err != nil || detail.ToolName != "WebFetch" || detail.Audit == nil || detail.Audit.ToolUseID != "fetch-call" || detail.Audit.Correlation != "exact" {
			t.Fatalf("record is not attributed to the WebFetch call: %+v %v", detail, err)
		}
		var in struct {
			URL string `json:"url"`
		}
		_ = json.Unmarshal(detail.ToolInput, &in)
		statuses[in.URL] = detail.Status
	}
	if want := map[string]string{start.URL + "/start": "allowed", second: "denied"}; !reflect.DeepEqual(statuses, want) {
		t.Fatalf("recorded %v, want %v", statuses, want)
	}
}

// A query the relay could not receive, here one with a raw space, goes to the
// target as written, and the answer shown is the target's own.
func TestWebFetchSendsAQueryTheRelayCannotReceive(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	requested := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		first, _ := reader.ReadString('\n')
		for line := first; line != "\r\n" && line != ""; {
			line, _ = reader.ReadString('\n')
		}
		requested <- first
		_, _ = conn.Write([]byte("HTTP/1.1 418 I'm a teapot\r\nContent-Length: 6\r\nConnection: close\r\n\r\ntarget"))
	}()
	address := "http://" + listener.Addr().String() + "/page?q=a b"
	result := webFetchInSession(t, t.Context(), agentcore.Options{Hooks: &webFetchHooks{}}, webFetchInput(address))
	select {
	case first := <-requested:
		if first != "GET /page?q=a b HTTP/1.1\r\n" {
			t.Fatalf("the target received %q", first)
		}
	default:
		t.Fatalf("the target was not contacted: %+v", result)
	}
	if result.IsError || result.Detail != "[418 I'm a teapot] "+address+"\ntarget" {
		t.Fatalf("the answer shown is not the target's: %+v", result)
	}
}

func TestWebFetchStopsAfterTenRedirects(t *testing.T) {
	var requests atomic.Int32
	loop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, fmt.Sprintf("/hop/%d", requests.Add(1)), http.StatusFound)
	}))
	defer loop.Close()
	hooks := &webFetchHooks{}
	result := webFetchInSession(t, t.Context(), agentcore.Options{Hooks: hooks}, webFetchInput(loop.URL+"/hop/0"))
	if !result.IsError || !strings.Contains(result.Detail, "10 redirects") {
		t.Fatalf("endless redirects not stopped: %+v", result)
	}
	if requests.Load() != 10 || len(hooks.inputs) != 10 {
		t.Fatalf("requests=%d reviews=%d, want 10 of each", requests.Load(), len(hooks.inputs))
	}
}

// A request that fails through the configured proxy is reported as failed. It
// is not repeated without the proxy, with or without hooks on the session.
func TestWebFetchDoesNotLeaveTheProxyWhenItFails(t *testing.T) {
	var direct atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		direct.Add(1)
		_, _ = w.Write([]byte("reached without the proxy"))
	}))
	defer target.Close()

	broken, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer broken.Close()
	go func() {
		for {
			conn, err := broken.Accept()
			if err != nil {
				return
			}
			_, _ = conn.Read(make([]byte, 4096))
			_ = conn.Close()
		}
	}()
	for _, guarded := range []bool{false, true} {
		opts := agentcore.Options{WebFetchProxy: "http://" + broken.Addr().String()}
		if guarded {
			opts.Hooks = &webFetchHooks{}
		}
		result := webFetchInSession(t, t.Context(), opts, webFetchInput(target.URL+"/page"))
		if !result.IsError || !strings.Contains(result.Detail, "proxy") || strings.Contains(result.Detail, "reached without the proxy") {
			t.Fatalf("guarded=%v: proxy failure not reported: %+v", guarded, result)
		}
	}
	if direct.Load() != 0 {
		t.Fatalf("the target was contacted %d time(s) outside the proxy", direct.Load())
	}

	// A proxy address that cannot be read fails the request as well.
	result := webFetchInSession(t, t.Context(), agentcore.Options{WebFetchProxy: "127.0.0.1:%zz"}, webFetchInput(target.URL+"/page"))
	if !result.IsError || direct.Load() != 0 {
		t.Fatalf("unusable proxy address let the request out: %+v", result)
	}

	// A working proxy still carries the request.
	var proxied []string
	working := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied = append(proxied, r.Method+" "+r.RequestURI+" "+r.Header.Get("User-Agent"))
		_, _ = w.Write([]byte("answered by the proxy"))
	}))
	defer working.Close()
	result = webFetchInSession(t, t.Context(), agentcore.Options{WebFetchProxy: working.URL}, webFetchInput(target.URL+"/page"))
	if result.IsError || !strings.Contains(result.Detail, "answered by the proxy") || direct.Load() != 0 {
		t.Fatalf("configured proxy not used: %+v", result)
	}
	if want := []string{"GET " + target.URL + "/page norma/0.4"}; !reflect.DeepEqual(proxied, want) {
		t.Fatalf("proxy saw %q, want %q", proxied, want)
	}
}

// In container mode the session gets no host-side WebFetch, from the SDK or
// from the catalog; outside it the SDK's injection is replaced by the
// reviewed tool, which presents itself exactly as the SDK's does.
func TestContainerModeSessionsGetNoWebFetch(t *testing.T) {
	names := func(opts agentcore.Options) (found []actool.CoreTool) {
		for _, tool := range opts.Tools {
			if tool.Name() == "WebFetch" {
				found = append(found, tool)
			}
		}
		return found
	}
	plain := actool.Build(actool.Spec{Name: "fixture", Schema: map[string]any{"type": "object"}})
	contained := agentcore.Options{Tools: []actool.CoreTool{&containerCatalogTool{plain}}, EnableWebFetch: true, Hooks: &webFetchHooks{}}
	ConfigureOperationReview(&contained)
	if contained.EnableWebFetch || len(names(contained)) != 0 {
		t.Fatal("a container-mode session keeps a host-side WebFetch")
	}
	sdk := actool.NewWebFetch(actool.WebFetchConfig{})
	if containerAllowed(sdk) {
		t.Fatal("the container catalog keeps a tool named WebFetch")
	}

	host := agentcore.Options{Tools: []actool.CoreTool{plain}, EnableWebFetch: true, Hooks: &webFetchHooks{}}
	ConfigureOperationReview(&host)
	found := names(host)
	if host.EnableWebFetch || len(found) != 1 {
		t.Fatalf("SDK injection on=%v, reviewed tools=%d", host.EnableWebFetch, len(found))
	}
	bound, ok := found[0].(toolUnwrapper).UnwrapTool().(*operationBoundTool)
	if !ok {
		t.Fatalf("WebFetch is %T, not bound to the session's hooks", found[0])
	}
	if _, ok := bound.CoreTool.(*reviewedWebFetch); !ok || host.Hooks.(*operationHooks).custom["WebFetch"] {
		t.Fatal("WebFetch is not the reviewed tool behind the ordinary hook")
	}
	raw := json.RawMessage(`{"url":"https://fixture.invalid/"}`)
	if bound.Description() != sdk.Description() || !reflect.DeepEqual(bound.InputSchema(), sdk.InputSchema()) ||
		!reflect.DeepEqual(bound.CheckPermissions(t.Context(), raw, permission.Context{}), sdk.CheckPermissions(t.Context(), raw, permission.Context{})) {
		t.Fatal("WebFetch presents itself differently from the SDK's tool")
	}
}

// webFetchSpy records the addresses the embedded SDK tool is asked to fetch.
type webFetchSpy struct {
	actool.CoreTool
	addresses []string
}

func (s *webFetchSpy) Call(ctx context.Context, input json.RawMessage, tc *actool.ToolContext) (actool.Result, error) {
	var in struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(input, &in)
	s.addresses = append(s.addresses, in.URL)
	return s.CoreTool.Call(ctx, input, tc)
}

func refusesConnections(address string) bool {
	conn, err := net.DialTimeout("tcp", address, 3*time.Second)
	if err == nil {
		_ = conn.Close()
	}
	return err != nil
}

// The SDK tool only ever sees the relay's loopback address, and the relay is
// gone when the call returns: after a fetch, and after a call the SDK refuses,
// which sends nothing to the target.
func TestWebFetchRelayIsClosedAfterTheCall(t *testing.T) {
	var requests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/moved" {
			// A redirect status without a location is a final response.
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusFound)
		}
		_, _ = w.Write([]byte("<p>plain body</p>"))
	}))
	defer target.Close()
	sdk := actool.NewWebFetch(actool.WebFetchConfig{})
	for _, path := range []string{"/notes.txt?v=1", "/moved", "/bundle.JS?v=3"} {
		tool := newReviewedWebFetch("", "", false)
		spy := &webFetchSpy{CoreTool: tool.CoreTool}
		tool.CoreTool = spy
		before := requests.Load()
		input := json.RawMessage(webFetchInput(target.URL + path))
		got, err := tool.Call(t.Context(), input, nil)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := sdk.Call(t.Context(), input, nil)
		static := strings.HasPrefix(path, "/bundle")
		if got.IsError != static || got.Flatten() != want.Flatten() || !static && !strings.Contains(got.Flatten(), "plain body") {
			t.Fatalf("%s: got %q, the SDK returns %q", path, got.Flatten(), want.Flatten())
		}
		// The comparison above made the SDK's own request for the fetched page.
		wantRequests, wantPasses := before+2, 2
		if static {
			wantRequests, wantPasses = before, 1
		}
		if requests.Load() != wantRequests || len(spy.addresses) != wantPasses {
			t.Fatalf("%s: target requests %d, SDK passes %d", path, requests.Load()-before, len(spy.addresses))
		}
		for _, address := range spy.addresses {
			local, err := url.Parse(address)
			if err != nil || local.Hostname() != "127.0.0.1" || !strings.HasSuffix(local.Path, strings.SplitN(path, "?", 2)[0]) {
				t.Fatalf("the SDK tool was given %q", address)
			}
			if !refusesConnections(local.Host) {
				t.Fatalf("%s: the relay still accepts connections", path)
			}
		}
	}
}

// The relay answers only the path token of its call, serves the response it
// holds once, sends no redirect, and stops listening when closed.
func TestWebFetchRelayServesOneResponseToItsTokenOnly(t *testing.T) {
	relay, err := startWebFetchRelay()
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	defer func() {
		if !closed {
			relay.close()
		}
	}()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		t.Error("the relay sent a redirect")
		return http.ErrUseLastResponse
	}}
	get := func(address string) (int, http.Header, string) {
		t.Helper()
		resp, err := client.Get(address)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header, string(body)
	}
	local, _ := url.Parse(relay.base)
	relay.offer(&webFetchPage{status: http.StatusFound, header: http.Header{"Server": {"fixture"}}, body: []byte("<html>moved</html>")})
	for _, other := range []string{"/", "/guess/page", relay.token + "x"} {
		if status, _, body := get("http://" + local.Host + other); status != http.StatusNotFound || strings.Contains(body, "moved") {
			t.Fatalf("%s answered %d %q", other, status, body)
		}
	}
	if _, reached := relay.asked(); reached {
		t.Fatal("a request without the token counted as the SDK's")
	}
	status, header, body := get(relay.base + "/page?x=1")
	if status != http.StatusFound || body != "<html>moved</html>" || header.Get("Server") != "fixture" || header.Get("Location") != "" || header.Get("Content-Type") != "" {
		t.Fatalf("served %d %v %q", status, header, body)
	}
	if status, _, body := get(relay.base + "/page?x=1"); status != http.StatusNoContent || body != "" {
		t.Fatalf("the response was served again: %d %q", status, body)
	}
	relay.close()
	closed = true
	if !refusesConnections(local.Host) {
		t.Fatal("the relay still accepts connections after close")
	}
}
