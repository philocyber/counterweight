package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	actool "github.com/Autumn-27/norma/tool"
	"github.com/Philocyber/counterweight/agent"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/guard"
	"github.com/Philocyber/counterweight/intercept"
	"github.com/Philocyber/counterweight/locale"
)

func TestHTTPToolURLValuesKeepTheirComponent(t *testing.T) {
	hostile := `a/b?c=d#e@f "g"`
	rendered, err := renderHTTPURL("https://api.fixture.test/items/{id}/detail?q={q}&fixed=1#{anchor}", map[string]any{"id": hostile, "q": hostile, "anchor": hostile})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(rendered)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Scheme != "https" || parsed.Host != "api.fixture.test" || parsed.User != nil || parsed.Path != "/items/"+hostile+"/detail" || strings.Count(parsed.EscapedPath(), "/") != 3 {
		t.Fatalf("value changed the URL structure: %s", rendered)
	}
	if query := parsed.Query(); len(query) != 2 || query.Get("q") != hostile || query.Get("fixed") != "1" || parsed.Fragment != hostile {
		t.Fatalf("value changed the query or fragment: %s", rendered)
	}
	for template, values := range map[string][]string{
		"https://api.fixture.test{path}":      {"/v1", "@other.test/", ".other.test/x", "?x=1", "#x", ".other.test", "ing", "-x"},
		"https://svc-{tenant}/items":          {"a.other.test", "a.b"},
		"https://node{tenant}{s}:8443/items":  {".other.test"},
		"https://{tenant}.fixture.test/items": {"other.test/", "other.test#", "user@other", "a b", `a"b`},
		"http{s}://api.fixture.test/items":    {"s://other.test/", "s:", "/"},
	} {
		for _, value := range values {
			if rendered, err := renderHTTPURL(template, map[string]any{"path": value, "tenant": value, "s": value}); err == nil {
				t.Fatalf("value entered the scheme or host of %s: %s", template, rendered)
			}
		}
	}
	// A dot segment would be resolved against the fixed segments around it.
	for _, value := range []string{".", ".."} {
		if rendered, err := renderHTTPURL("https://api.fixture.test/items/{id}/detail", map[string]any{"id": value}); err == nil {
			t.Fatalf("dot segment entered the path: %s", rendered)
		}
	}
	if rendered, err := renderHTTPURL("https://api.fixture.test/items/{id}{s}/detail", map[string]any{"id": ".", "s": "."}); err == nil {
		t.Fatalf("two values formed a dot segment: %s", rendered)
	}
	for template, want := range map[string]string{
		"https://{tenant}.fixture.test/items":     "https://acme-1.fixture.test/items",
		"https://svc-{tenant}/items":              "https://svc-acme-1/items",
		"https://node{port}/items":                "https://node8443/items",
		"https://api.{tenant}/items/../{id}":      "https://api.acme-1/items/../a%2Fb",
		"http{s}://api.fixture.test:{port}/x":     "https://api.fixture.test:8443/x",
		"{origin}/v1/{id}":                        "https://chosen.fixture.test/base/v1/a%2Fb",
		"{origin}":                                "https://chosen.fixture.test/base",
		"https://api.fixture.test/{missing}/{id}": "https://api.fixture.test/{missing}/a%2Fb",
	} {
		rendered, err := renderHTTPURL(template, map[string]any{"tenant": "acme-1", "s": "s", "port": float64(8443), "origin": "https://chosen.fixture.test/base", "id": "a/b"})
		if err != nil || rendered != want {
			t.Fatalf("%s rendered %q (%v), want %q", template, rendered, err, want)
		}
	}
}

func TestHTTPToolBodyValuesKeepTheirStructure(t *testing.T) {
	hostile := `x","admin":true,"y":"\`
	rendered := renderHTTPBody(`{"name":"{name}","count":{count},"tags":{tags},"note":{note}}`, map[string]any{"name": hostile, "count": float64(3), "tags": []any{"a", "b"}, "note": hostile}, false)
	var decoded map[string]any
	if err := json.Unmarshal([]byte(rendered), &decoded); err != nil {
		t.Fatalf("rendered body is not JSON: %s (%v)", rendered, err)
	}
	if len(decoded) != 4 || decoded["name"] != hostile || decoded["count"] != float64(3) || decoded["note"] != hostile || fmt.Sprint(decoded["tags"]) != "[a b]" {
		t.Fatalf("value changed the JSON structure: %s", rendered)
	}
	// Leaving an optional parameter out does not switch the encoding off for the others.
	quoted, _ := json.Marshal(hostile)
	if got, want := renderHTTPBody(`{"name":"{name}","page":{page}}`, map[string]any{"name": hostile}, false), `{"name":`+string(quoted)+`,"page":{page}}`; got != want {
		t.Fatalf("omitted parameter changed the encoding: %s, want %s", got, want)
	}
	if got := renderHTTPBody("a={a}&fixed=1", map[string]any{"a": "x&fixed=2 y"}, true); got != "a=x%26fixed%3D2+y&fixed=1" {
		t.Fatalf("form value changed the fields: %q", got)
	}
	// A body that is only a placeholder, and a body in another format, are the
	// caller's content.
	for template, want := range map[string]string{"{body}": hostile, " {body} ": " " + hostile + " ", "<v>{body}</v>": "<v>" + hostile + "</v>"} {
		if got := renderHTTPBody(template, map[string]any{"body": hostile}, false); got != want {
			t.Fatalf("%q rendered %q, want %q", template, got, want)
		}
	}
}

func TestCustomToolRefusesUndeclaredAndMistypedParameters(t *testing.T) {
	var requests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer target.Close()
	httpExec, _ := json.Marshal(map[string]any{"method": "POST", "url": target.URL + "/items/{id}", "body": `{"a":1,"id":"{id}"}`})
	httpTool := (&Server{}).buildCustomToolForLanguage(&db.Tool{Key: "http_fixture", Kind: "http", Exec: httpExec, Schema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"additionalProperties":true}`)}, locale.En)
	if httpTool.InputSchema()["additionalProperties"] != false {
		t.Fatal("model schema still advertises undeclared HTTP parameters")
	}
	// The second key spells fixed template text; it must not become a placeholder.
	result, err := httpTool.Call(t.Context(), json.RawMessage(`{"id":"7","\"a\":1,\"id\":\"{id}\"":"\"a\":2"}`), nil)
	if err != nil || !result.IsError || !strings.Contains(result.Flatten(), locale.Text(locale.En, "HTTP tool parameters must be declared in its schema")) || requests.Load() != 0 {
		t.Fatalf("undeclared HTTP parameter was accepted: %q %v requests=%d", result.Flatten(), err, requests.Load())
	}

	commandExec, _ := json.Marshal(commandExec{Command: `echo {count} {ratio} {enabled} {optional} {name}`})
	commandTool := (&Server{}).buildCustomToolForLanguage(&db.Tool{Key: "typed_fixture", Kind: "command", Exec: commandExec, Schema: json.RawMessage(`{"type":"object","properties":{"count":{"type":"integer"},"ratio":{"type":"number"},"enabled":{"type":"boolean"},"optional":{"type":["integer","null"]},"name":{"type":"string"}}}`)}, locale.En)
	for _, input := range []string{`{"count":"1"}`, `{"count":"a[$(id)]"}`, `{"count":1.5}`, `{"count":null}`, `{"count":true}`, `{"ratio":"1.5"}`, `{"enabled":"true"}`, `{"enabled":1}`, `{"optional":"1"}`} {
		result, err := commandTool.Call(t.Context(), json.RawMessage(input), nil)
		if err != nil || !result.IsError || !strings.Contains(result.Flatten(), "does not have its declared type") {
			t.Fatalf("mistyped parameter %s was accepted: %q %v", input, result.Flatten(), err)
		}
	}
	for _, tc := range []struct {
		types []string
		value any
	}{
		{[]string{"integer"}, float64(-3)}, {[]string{"number"}, 1.5}, {[]string{"boolean"}, false},
		{[]string{"integer", "null"}, nil}, {[]string{"string"}, "text"}, {[]string{"integer", "string"}, "text"}, {nil, "text"}, {[]string{"array"}, []any{1.0}},
	} {
		if !declaredScalarMatches(tc.types, tc.value) {
			t.Fatalf("%v refused a value of its declared type: %#v", tc.types, tc.value)
		}
	}
}

func TestCustomHTTPReviewRecordOmitsStoredCredentials(t *testing.T) {
	dsn, _, err := db.DSN()
	if err != nil {
		t.Skip("requires disposable PostgreSQL")
	}
	d, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	ic := intercept.New(d)
	priorTools, err := ic.GetEnabledTools()
	if err != nil {
		t.Fatal(err)
	}
	priorConfig := ic.GetJudgeConfig()
	t.Cleanup(func() { _ = ic.SetEnabledTools(priorTools); _ = ic.SetJudgeConfig(priorConfig) })
	if err := ic.SetEnabledTools([]string{"WebFetch"}); err != nil {
		t.Fatal(err)
	}
	if err := ic.SetJudgeConfig(intercept.JudgeConfig{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	priorResolve, priorAugment := agent.ToolResolve, agent.ToolAugment
	agent.ToolResolve = nil
	t.Cleanup(func() { agent.ToolResolve, agent.ToolAugment = priorResolve, priorAugment })

	var received http.Header
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = r.Header.Clone()
		_, _ = w.Write([]byte("fixture response"))
	}))
	defer target.Close()
	const secret, token = "STORED-HEADER-SECRET", "MODEL-SUPPLIED-TOKEN"
	exec, _ := json.Marshal(map[string]any{"method": "GET", "url": strings.Replace(target.URL, "http://", "http://operator:"+secret+"@", 1) + "/items/{id}", "headers": map[string]string{"Authorization": "Bearer " + secret, "X-Api-Key": secret + "-{id}"}})
	row := &db.Tool{Key: "credential_fixture", Kind: "http", Exec: exec, Schema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}}}`)}
	tool := (&Server{}).buildCustomTool(row)
	agent.ToolAugment = func(context.Context, string) ([]actool.CoreTool, agent.DeferredInfo, func()) {
		return []actool.CoreTool{tool}, agent.DeferredInfo{}, func() {}
	}
	var reviewed string
	ic.SetReviewer(func(_ context.Context, _ int64, _ string, in intercept.ReviewInput) (intercept.Decision, error) {
		reviewed += string(in.Arguments)
		return intercept.Decision{Action: "allow", Message: "fixture verdict"}, nil
	})
	const taskID = "credential-review-fixture"
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM intercept_pending WHERE task_id=$1`, taskID) })
	ctx := intercept.WithTaskContext(t.Context(), taskID, "fixture", nil)
	input, _ := json.Marshal(map[string]string{"id": token})
	chat := agent.NewChatAgent(&customOperationProvider{name: row.Key, input: input}, "fixture", t.TempDir(), nil, 100000)
	chat.SetGuard(guard.NewWithInterceptor(ic))
	var activities strings.Builder
	var result db.Activity
	if _, err := chat.Chat(ctx, "fixture", "credential-session", "fixture", 2, 0, false, func(a db.Activity) {
		activities.WriteString(a.Detail)
		if a.Kind == "tool_result" {
			result = a
		}
	}); err != nil {
		t.Fatal(err)
	}
	if result.IsError || received.Get("Authorization") != "Bearer "+secret || received.Get("X-Api-Key") != secret+"-"+token {
		t.Fatalf("stored headers did not reach the destination: result=%+v headers=%v", result, received)
	}
	if !strings.Contains(reviewed, "Authorization") || !strings.Contains(reviewed, "/items/"+token) || !strings.Contains(reviewed, token) {
		t.Fatalf("review lost the header names, destination or model-supplied value: %s", reviewed)
	}
	rows, err := d.ListTaskIntercepts(taskID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("review records: %d %v", len(rows), err)
	}
	detail, err := d.GetInterceptDetail(rows[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := json.Marshal(detail)
	for name, text := range map[string]string{"reviewer input": reviewed, "review record": string(stored), "activity stream": activities.String()} {
		if strings.Contains(text, secret) {
			t.Fatalf("%s holds the stored credential: %s", name, text)
		}
	}
}
