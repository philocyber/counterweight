package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/harness"
	actool "github.com/Autumn-27/norma/tool"

	"github.com/Philocyber/counterweight/agent"
	"github.com/Philocyber/counterweight/authz"
	"github.com/Philocyber/counterweight/locale"
)

// setAgentAccess turns the agents' use of an account on or off through the API.
func (f *authzFixture) setAgentAccess(key string, on bool) {
	f.t.Helper()
	for _, a := range decodeBody(f.t, f.do("GET", f.path(""), f.sess, nil))["accounts"].([]any) {
		account := a.(map[string]any)
		if account["key"] != key {
			continue
		}
		w := f.do("PATCH", f.path(fmt.Sprintf("/accounts/%d", int64(account["id"].(float64)))), f.sess, map[string]any{"agent_access": on})
		if w.Code != 200 || decodeBody(f.t, w)["agent_access"] != on {
			f.t.Fatalf("set agent access: %d %s", w.Code, w.Body.String())
		}
		return
	}
	f.t.Fatalf("no account %s", key)
}

// agentCall calls the handler of request_as_role as an agent of the task
// would, and checks that the result holds no credential.
func (f *authzFixture) agentCall(input map[string]any) (text string, isError bool) {
	f.t.Helper()
	raw, _ := json.Marshal(input)
	ctx := agent.WithRunInfo(context.Background(), agent.RunInfo{TaskID: f.taskID})
	res, err := f.s.requestAsRole(ctx, raw, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	text = res.Flatten()
	for _, secret := range authzSecrets {
		if strings.Contains(text, secret) {
			f.t.Errorf("a tool result holds the credential %q: %s", secret, text)
		}
	}
	return text, res.IsError
}

func (f *authzFixture) savePolicy() {
	f.t.Helper()
	if w := f.do("PUT", f.path("/policy"), f.sess, map[string]any{"policy": f.policy()}); w.Code != 200 || !strings.Contains(w.Body.String(), `"ok":true`) {
		f.t.Fatalf("save policy: %d %s", w.Code, w.Body.String())
	}
}

func TestRequestAsRoleIsOptInPerAccount(t *testing.T) {
	f := newAuthzFixture(t)
	f.addAccounts()
	f.savePolicy()
	ctx := agent.WithRunInfo(context.Background(), agent.RunInfo{TaskID: f.taskID})

	// Off by default: the tool is not offered and its handler refuses.
	if tools := f.s.authzAgentTools(ctx); len(tools) != 0 {
		t.Fatalf("tool offered without an enabled account: %d", len(tools))
	}
	if text, isError := f.agentCall(map[string]any{}); !isError || !strings.Contains(text, "No test account of this task is enabled for agents") {
		t.Fatalf("handler without an enabled account: %s", text)
	}
	if tools := f.s.authzAgentTools(context.Background()); len(tools) != 0 {
		t.Fatal("tool offered outside a task")
	}

	f.setAgentAccess("tenant_b_user", true)
	tools := f.s.authzAgentTools(ctx)
	if len(tools) != 1 || tools[0].Name() != authzAgentToolName {
		t.Fatalf("tools = %v", tools)
	}

	// The role list names only what the operator enabled; the admin stays out.
	text, isError := f.agentCall(map[string]any{})
	if isError || !strings.Contains(text, `"role":"tenant_b_user"`) || !strings.Contains(text, `"name":"Tenant B user"`) ||
		!strings.Contains(text, `"role":"anonymous"`) || strings.Contains(text, "platform_admin") || !strings.Contains(text, f.target.URL) {
		t.Fatalf("role list: %s", text)
	}
	if f.hits.Load() != 0 {
		t.Fatalf("listing roles sent %d requests", f.hits.Load())
	}
	text, isError = f.agentCall(map[string]any{"role": "platform_admin", "method": "GET", "path": "/api/admin/users"})
	if !isError || !strings.Contains(text, "not available to agents") || f.hits.Load() != 0 {
		t.Fatalf("a role that is not enabled was used: %s (%d requests)", text, f.hits.Load())
	}

	// An enabled role signs in on the server; the agent gets the answer only.
	text, isError = f.agentCall(map[string]any{"role": "tenant_b_user", "method": "get", "path": "/api/me"})
	if isError || !strings.Contains(text, `"status":200`) || !strings.Contains(text, "b@tenant-b.test") {
		t.Fatalf("request as an enabled role: %s", text)
	}
	text, _ = f.agentCall(map[string]any{"role": "anonymous", "method": "GET", "path": "/api/me"})
	if !strings.Contains(text, `"status":401`) {
		t.Fatalf("anonymous request: %s", text)
	}
	// With the planted flaw the answer carries another session of the task's
	// accounts; it is removed before the agent sees it.
	f.adminOpen.Store(true)
	text, _ = f.agentCall(map[string]any{"role": "tenant_b_user", "method": "GET", "path": "/api/admin/users"})
	if !strings.Contains(text, `"status":200`) || !strings.Contains(text, "[REDACTED]") {
		t.Fatalf("leaked session in the answer: %s", text)
	}

	// Turning the switch off takes the role, the tool and the signed-in session away.
	f.setAgentAccess("tenant_b_user", false)
	if tools := f.s.authzAgentTools(ctx); len(tools) != 0 {
		t.Fatal("tool still offered after the account was disabled")
	}
	before := f.hits.Load()
	if text, isError := f.agentCall(map[string]any{"role": "tenant_b_user", "method": "GET", "path": "/api/me"}); !isError || f.hits.Load() != before {
		t.Fatalf("a disabled role was used: %s", text)
	}
	authzBrokers.Lock()
	_, kept := authzBrokers.m[authzRunKey{f.s, f.taskID}]
	authzBrokers.Unlock()
	if kept {
		t.Error("the broker with the opened credentials was kept after the account was disabled")
	}
}

func TestRequestAsRoleCannotBeSteeredOffTarget(t *testing.T) {
	f := newAuthzFixture(t)
	f.addAccounts()
	f.savePolicy()
	f.setAgentAccess("tenant_b_user", true)
	f.setAgentAccess("platform_admin", true)

	for name, input := range map[string]map[string]any{
		"credential header":     {"role": "tenant_b_user", "method": "GET", "path": "/api/me", "headers": map[string]any{"Authorization": "Bearer x"}},
		"cookie header":         {"role": "tenant_b_user", "method": "GET", "path": "/api/me", "headers": map[string]any{"cookie": "sid=x"}},
		"host header":           {"role": "tenant_b_user", "method": "GET", "path": "/api/me", "headers": map[string]any{"Host": "other.example"}},
		"absolute URL":          {"role": "tenant_b_user", "method": "GET", "path": "https://other.example/x"},
		"protocol-relative URL": {"role": "tenant_b_user", "method": "GET", "path": "//other.example/x"},
		"unknown method":        {"role": "tenant_b_user", "method": "CONNECT", "path": "/api/me"},
		"two bodies":            {"role": "tenant_b_user", "method": "POST", "path": "/api/me", "json": map[string]any{"a": 1}, "body": "x"},
		"unknown role":          {"role": "root", "method": "GET", "path": "/api/me"},
	} {
		if text, isError := f.agentCall(input); !isError {
			t.Errorf("%s was accepted: %s", name, text)
		}
	}
	if f.hits.Load() != 0 {
		t.Fatalf("refused requests reached the target %d times", f.hits.Load())
	}

	// A reference written by the agent is plain text: it is never replaced by a credential.
	text, isError := f.agentCall(map[string]any{"role": "tenant_b_user", "method": "GET", "path": "/api/me?x=${account:platform_admin.token}",
		"headers": map[string]any{"X-Probe": "${account:platform_admin.token}"}})
	if isError {
		t.Fatalf("request with a literal reference: %s", text)
	}
	if got, _ := f.probe.Load().(string); got != "${account:platform_admin.token}" {
		t.Fatalf("the target received %q for a reference written by the agent", got)
	}

	// The blocked targets of the task apply to the agent's requests too.
	if _, err := f.s.m.Assets().CreateTaskInterceptRule(f.taskID, "block", "exact_ip", "127.0.0.1", "", true); err != nil {
		t.Fatal(err)
	}
	before := f.hits.Load()
	if text, isError := f.agentCall(map[string]any{"role": "tenant_b_user", "method": "GET", "path": "/api/me"}); !isError || f.hits.Load() != before {
		t.Fatalf("a request reached a blocked target: %s", text)
	}
}

// reviewHooks records what the approval gate is asked and can refuse it.
type reviewHooks struct {
	harness.HookRunner
	seen  []string
	block bool
}

func (h *reviewHooks) PreToolUse(_ context.Context, _ string, input []byte) (bool, string, []byte) {
	h.seen = append(h.seen, string(input))
	return h.block, "refused by a rule of the test", nil
}

func TestRequestAsRoleGoesThroughTheOperationReview(t *testing.T) {
	f := newAuthzFixture(t)
	f.addAccounts()
	f.savePolicy()
	f.setAgentAccess("tenant_b_user", true)
	ctx := agent.WithRunInfo(context.Background(), agent.RunInfo{TaskID: f.taskID})
	tool := f.s.authzAgentTool(locale.En)
	input := json.RawMessage(`{"role":"tenant_b_user","method":"DELETE","path":"/api/docs/7","json":{"reason":"test"}}`)

	// Called outside an agent session there is no review to bind: refused, nothing sent.
	res, err := tool.Call(ctx, input, nil)
	if err != nil || !res.IsError || f.hits.Load() != 0 {
		t.Fatalf("unreviewed call: %s %v (%d requests)", res.Flatten(), err, f.hits.Load())
	}

	// Inside a session the gate sees the request as it will be sent, without credentials.
	hooks := &reviewHooks{block: true}
	opts := &agentcore.Options{Tools: []actool.CoreTool{tool}, Hooks: hooks}
	agent.ConfigureOperationReview(opts)
	res, err = opts.Tools[0].Call(ctx, input, nil)
	if err != nil || !res.IsError || !strings.Contains(res.Flatten(), "refused by a rule of the test") || f.hits.Load() != 0 {
		t.Fatalf("a refused operation was sent: %s %v (%d requests)", res.Flatten(), err, f.hits.Load())
	}
	if len(hooks.seen) != 1 {
		t.Fatalf("the gate was asked %d times", len(hooks.seen))
	}
	var op map[string]any
	if err := json.Unmarshal([]byte(hooks.seen[0]), &op); err != nil {
		t.Fatal(err)
	}
	if op["kind"] != "http" || op["method"] != "DELETE" || op["url"] != f.target.URL+"/api/docs/7" || op["as_role"] != "tenant_b_user" || op["body"] != `{"reason":"test"}` {
		t.Fatalf("operation shown to the gate: %v", op)
	}
	for _, secret := range authzSecrets {
		if strings.Contains(hooks.seen[0], secret) {
			t.Errorf("the gate was shown the credential %q", secret)
		}
	}

	// Approved: the request is sent.
	hooks.block = false
	res, err = opts.Tools[0].Call(ctx, input, nil)
	if err != nil || res.IsError || f.hits.Load() == 0 {
		t.Fatalf("approved operation: %s %v", res.Flatten(), err)
	}
}

func TestRequestAsRoleDescriptionAndSchema(t *testing.T) {
	desc, schema := authzAgentToolMeta(locale.En)
	props, _ := schema["properties"].(map[string]any)
	for _, name := range []string{"role", "method", "path", "headers", "json", "form", "body"} {
		if _, ok := props[name]; !ok {
			t.Errorf("schema lacks %s", name)
		}
	}
	for _, want := range []string{"never receive the password", "cannot choose another host", "not stored as captured traffic"} {
		if !strings.Contains(desc, want) {
			t.Errorf("description lacks %q", want)
		}
	}
}

// The tool reaches the agents that test a running application and nobody
// else, and stays under the operation review after the registry wraps it.
func TestRequestAsRoleIsBoundToTestingAgents(t *testing.T) {
	f := newAuthzFixture(t)
	f.addAccounts()
	f.savePolicy()
	f.setAgentAccess("tenant_b_user", true)
	wireTools(f.s.m.pg, nil)
	ctx := agent.WithRunInfo(context.Background(), agent.RunInfo{TaskID: f.taskID})
	find := func(tools []actool.CoreTool) actool.CoreTool {
		for _, tool := range tools {
			if tool.Name() == authzAgentToolName {
				return tool
			}
		}
		return nil
	}
	host, _ := f.s.hostTools(ctx)
	if find(host) == nil {
		t.Fatal("the host tools of a task with an enabled account lack request_as_role")
	}
	if other, _ := f.s.hostTools(context.Background()); find(other) != nil {
		t.Fatal("request_as_role is offered outside the task")
	}
	for agentKey, want := range map[string]bool{"worker": true, "pentest": true, "planner": false, "mainagent": false, "goals": false} {
		if got := find(agent.ToolResolve(ctx, agentKey, host)) != nil; got != want {
			t.Errorf("agent %s: tool present = %v, want %v", agentKey, got, want)
		}
	}
	resolved := find(agent.ToolResolve(ctx, "worker", host))
	hooks := &reviewHooks{block: true}
	opts := &agentcore.Options{Tools: []actool.CoreTool{resolved}, Hooks: hooks}
	agent.ConfigureOperationReview(opts)
	res, err := opts.Tools[0].Call(ctx, json.RawMessage(`{"role":"tenant_b_user","method":"GET","path":"/api/me"}`), nil)
	if err != nil || !res.IsError || len(hooks.seen) != 1 || f.hits.Load() != 0 {
		t.Fatalf("the wrapped tool escaped the review: %s %v (asked %d times, %d requests)", res.Flatten(), err, len(hooks.seen), f.hits.Load())
	}
}

// revokingHooks approves the request, but the operator turns the account off
// while the request is waiting for that approval.
type revokingHooks struct {
	harness.HookRunner
	revoke func()
}

func (h *revokingHooks) PreToolUse(context.Context, string, []byte) (bool, string, []byte) {
	h.revoke()
	return false, "", nil
}

func TestRequestAsRoleApprovalDoesNotOutliveARevocation(t *testing.T) {
	f := newAuthzFixture(t)
	f.addAccounts()
	f.savePolicy()
	f.setAgentAccess("tenant_b_user", true)
	f.setAgentAccess("platform_admin", true)
	ctx := agent.WithRunInfo(context.Background(), agent.RunInfo{TaskID: f.taskID})
	opts := &agentcore.Options{
		Tools: []actool.CoreTool{f.s.authzAgentTool(locale.En)},
		Hooks: &revokingHooks{revoke: func() { f.setAgentAccess("tenant_b_user", false) }},
	}
	agent.ConfigureOperationReview(opts)
	res, err := opts.Tools[0].Call(ctx, json.RawMessage(`{"role":"tenant_b_user","method":"GET","path":"/api/me"}`), nil)
	if err != nil || !res.IsError || f.hits.Load() != 0 {
		t.Fatalf("a request approved before the account was disabled was sent: %s %v (%d requests)", res.Flatten(), err, f.hits.Load())
	}
}

// A change to the task's accounts or policy ends the agents' signed-in
// sessions at once, without waiting for the next tool call.
func TestRequestAsRoleSessionsEndWhenTheTaskChanges(t *testing.T) {
	f := newAuthzFixture(t)
	f.addAccounts()
	f.savePolicy()
	f.setAgentAccess("tenant_b_user", true)
	key := authzRunKey{f.s, f.taskID}
	entry := func() *authzBrokerEntry {
		authzBrokers.Lock()
		defer authzBrokers.Unlock()
		return authzBrokers.m[key]
	}
	// The deletion goes last: it leaves the policy without one of its accounts.
	for _, step := range []struct {
		name   string
		change func()
	}{
		{"policy saved", func() { f.savePolicy() }},
		{"account edited", func() { f.setAgentAccess("platform_admin", true) }},
		{"account deleted", func() {
			for _, a := range decodeBody(t, f.do("GET", f.path(""), f.sess, nil))["accounts"].([]any) {
				if a.(map[string]any)["key"] == "platform_admin" {
					f.do("DELETE", f.path(fmt.Sprintf("/accounts/%d", int64(a.(map[string]any)["id"].(float64)))), f.sess, nil)
				}
			}
		}},
	} {
		name, change := step.name, step.change
		if text, isError := f.agentCall(map[string]any{"role": "anonymous", "method": "GET", "path": "/api/me"}); isError {
			t.Fatalf("%s: setup call failed: %s", name, text)
		}
		before := entry()
		if before == nil {
			t.Fatalf("%s: no broker after a call", name)
		}
		change()
		if entry() != nil {
			t.Errorf("%s: the broker with the opened credentials was kept", name)
		}
		if _, err := before.broker.Do(context.Background(), "anonymous", authz.Request{Method: "GET", Path: "/api/me"}); !errors.Is(err, authz.ErrBrokerClosed) {
			t.Errorf("%s: a broker obtained before the change still sends requests: %v", name, err)
		}
	}
}
