package subscription

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/norma/llm"
	"github.com/google/uuid"
)

func TestCodexHelper(t *testing.T) {
	if os.Getenv("COUNTERWEIGHT_FAKE_CODEX") != "1" {
		return
	}
	if _, err := os.Stat(os.Getenv("CODEX_HOME")); err != nil {
		fmt.Fprintln(os.Stderr, "Error: CODEX_HOME does not exist")
		os.Exit(1)
	}
	if os.Getenv("COUNTERWEIGHT_FAKE_STARTUP_FAIL") == "1" {
		fmt.Fprintln(os.Stderr, "Error: fixture startup rejected")
		os.Exit(1)
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 8<<20)
	emit :=func(v any) { _ = json.NewEncoder(os.Stdout).Encode(v) }
	notify := func(method string, params any) { emit(map[string]any{"method": method, "params": params}) }
	for scanner.Scan() {
		var p packet
		if json.Unmarshal(scanner.Bytes(), &p) != nil {
			os.Exit(2)
		}
		reply := func(v any) { emit(map[string]any{"id": p.ID, "result": v}) }
		switch p.Method {
		case "initialize":
			if !strings.Contains(string(p.Params), `"name":"counterweight"`) {
				os.Exit(3)
			}
			reply(map[string]any{})
		case "initialized":
		case "account/login/start":
			reply(map[string]any{"loginId": "login-1", "authUrl": "https://auth.openai.com/oauth/authorize?client_id=native-test"})
			if os.Getenv("COUNTERWEIGHT_FAKE_WAIT") != "1" {
				notify("account/login/completed", map[string]any{"loginId": "login-1", "success": true})
			}
		case "account/read":
			reply(map[string]any{"account": map[string]any{"type": "chatgpt", "email": "test@example.test", "planType": "enterprise"}})
		case "model/list":
			reply(map[string]any{"data": []any{map[string]any{"model": "account-model", "displayName": "Account Model", "availableAccessPrograms": map[string]any{"cyber": []string{"standard", "daybreakBlue"}}}, map[string]any{"model": "hidden", "hidden": true}}})
		case "thread/start":
			if !strings.Contains(string(p.Params), `"ephemeral":true`) || !strings.Contains(string(p.Params), `"sandbox":"read-only"`) {
				os.Exit(4)
			}
			reply(map[string]any{"thread": map[string]any{"id": "thread-1"}})
		case "thread/inject_items":
			if os.Getenv("COUNTERWEIGHT_FAKE_EXPECT_HISTORY") == "1" && !strings.Contains(string(p.Params), "function_call_output") {
				os.Exit(5)
			}
			reply(map[string]any{})
		case "turn/start":
			if want := os.Getenv("COUNTERWEIGHT_FAKE_EXPECT_PROGRAM"); want != "" && !strings.Contains(string(p.Params), `"cyberAccessProgram":"`+want+`"`) {
				os.Exit(7)
			}
			reply(map[string]any{"turn": map[string]any{"id": "turn-1"}})
			switch os.Getenv("COUNTERWEIGHT_FAKE_MODE") {
			case "tool":
				emit(map[string]any{"id": "request-1", "method": "item/tool/call", "params": map[string]any{"tool": "counterweight_inspect", "callId": "call-1", "arguments": map[string]any{"target": "fixture"}}})
			case "hang":
			case "builtin":
				notify("item/started", map[string]any{"item": map[string]any{"type": "commandExecution"}})
			case "unknown":
				emit(map[string]any{"id": "request-1", "method": "item/tool/call", "params": map[string]any{"tool": "shell", "callId": "call-1", "arguments": map[string]any{}}})
			case "failed":
				notify("turn/completed", map[string]any{"turn": map[string]any{"status": "failed", "error": map[string]any{"message": "fixture usage limit"}}})
			case "truncated":
				os.Exit(0)
			default:
				notify("item/agentMessage/delta", map[string]any{"delta": "native answer"})
				notify("thread/tokenUsage/updated", map[string]any{"tokenUsage": map[string]any{"last": map[string]any{"inputTokens": 25, "outputTokens": 5, "cachedInputTokens": 10}}})
				notify("turn/completed", map[string]any{"turn": map[string]any{"status": "completed"}})
			}
		default:
			fmt.Fprintln(os.Stderr, p.Method)
			os.Exit(6)
		}
	}
	os.Exit(0)
}
func fixture(t *testing.T) *Manager {
	t.Helper()
	m := New(t.TempDir())
	m.command = func(ctx context.Context) *exec.Cmd {
		c := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCodexHelper$")
		c.Env = []string{"COUNTERWEIGHT_FAKE_CODEX=1"}
		return c
	}
	return m
}
func connectedFixture(t *testing.T) *Manager {
	m := fixture(t)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.load(); err != nil {
		t.Fatal(err)
	}
	m.accounts[fixtureID] = Account{ID: fixtureID, Connected: true}
	if err := m.save(); err != nil {
		t.Fatal(err)
	}
	return m
}

const fixtureID = "7f6cf9ae-3b38-4188-a8f0-b7d5989431f3"

func TestSubscriptionNativeLoginModelsAndStorage(t *testing.T) {
	m := fixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a, err := m.Start(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(a.URL, "https://auth.openai.com/") {
		t.Fatal(a.URL)
	}
	var status Attempt
	for ctx.Err() == nil {
		status, err = m.Status(a.ID)
		if err != nil {
			t.Fatal(err)
		}
		if status.Status != "pending" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status.Status != "connected" || status.Account == nil || status.Account.Plan != "enterprise" {
		t.Fatalf("status=%+v", status)
	}
	if status.URL != "" {
		t.Fatal("status exposed login URL")
	}
	models, err := m.Models(ctx, status.Account.ID)
	if err != nil || len(models) != 1 || models[0].DisplayName != "Account Model" || len(models[0].CyberPrograms) != 2 {
		t.Fatalf("models=%+v err=%v", models, err)
	}
	info, err := os.Stat(filepath.Join(m.root, "connections.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("private manifest permissions", err)
	}
	restored := New(strings.TrimSuffix(strings.TrimSuffix(m.root, "/native-codex"), "/chatgpt-subscription"))
	accounts, err := restored.Accounts()
	if err != nil || len(accounts) != 1 {
		t.Fatal(accounts, err)
	}
	if _, err = m.directory("../escape"); err == nil {
		t.Fatal("path traversal accepted")
	}
}
func TestSubscriptionNativeStartupFailureReportsStderr(t *testing.T) {
	t.Setenv("COUNTERWEIGHT_FAKE_STARTUP_FAIL", "1")
	m := fixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := m.Start(ctx, "")
	if err == nil || !strings.Contains(err.Error(), "fixture startup rejected") {
		t.Fatalf("err=%v", err)
	}
}
func TestSubscriptionNativeRelativeDataDir(t *testing.T) {
	t.Setenv("COUNTERWEIGHT_FAKE_WAIT", "1")
	t.Chdir(t.TempDir())
	m := New(".")
	m.command = fixture(t).command
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a, err := m.Start(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	_ = m.Cancel(a.ID)
}
func TestSubscriptionNativeCancelAndConcurrentLogin(t *testing.T) {
	t.Setenv("COUNTERWEIGHT_FAKE_WAIT", "1")
	m := fixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a, err := m.Start(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Start(ctx, ""); err == nil {
		t.Fatal("parallel login allowed")
	}
	if err = m.Cancel(a.ID); err != nil {
		t.Fatal(err)
	}
	status, _ := m.Status(a.ID)
	if status.Status != "cancelled" {
		t.Fatal(status)
	}
	if _, err = m.Account(uuid.NewString()); err == nil {
		t.Fatal("unregistered connection accepted")
	}
}
func TestSubscriptionNativeInferenceAndToolRoundTrip(t *testing.T) {
	for _, mode := range []string{"text", "tool", "failed", "truncated", "unknown", "builtin"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("COUNTERWEIGHT_FAKE_MODE", mode)
			m := connectedFixture(t)
			p, err := m.Provider(fixtureID, "account-model", "", "standard")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			req := llm.CompletionRequest{System: []string{"Host instructions"}, Messages: []llm.Message{llm.UserText("Inspect fixture")}, Tools: []llm.ToolSchema{{Name: "inspect", InputSchema: map[string]any{"type": "object"}}}}
			msg, stop, usage, err := p.Complete(ctx, req)
			switch mode {
			case "text":
				if err != nil || msg.Text() != "native answer" || stop != "end_turn" || usage.InputTokens != 25 {
					t.Fatal(msg, stop, usage, err)
				}
			case "tool":
				if err != nil || stop != "tool_use" || len(msg.ToolUses()) != 1 || msg.ToolUses()[0].Name != "inspect" {
					t.Fatal(msg, stop, err)
				}
				t.Setenv("COUNTERWEIGHT_FAKE_MODE", "text")
				t.Setenv("COUNTERWEIGHT_FAKE_EXPECT_HISTORY", "1")
				req.Messages = append(req.Messages, msg, llm.Message{Role: llm.RoleUser, Content: []llm.ContentBlock{llm.ToolResultText("call-1", "inspected", false)}})
				answer, _, _, err := p.Complete(ctx, req)
				if err != nil || answer.Text() != "native answer" {
					t.Fatal(answer, err)
				}
			default:
				if err == nil {
					t.Fatal("failed or invalid native response treated as successful")
				}
			}
		})
	}
}
func TestSubscriptionNativeEnvironmentIsolation(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "do-not-inherit")
	t.Setenv("CODEX_ACCESS_TOKEN", "do-not-inherit")
	for _, e := range runtimeEnv("/private/codex") {
		if strings.Contains(e, "do-not-inherit") {
			t.Fatal("inherited another credential")
		}
	}
}
func TestNativeInstalledProtocol(t *testing.T) {
	if os.Getenv("COUNTERWEIGHT_TEST_CODEX_PROTOCOL") != "1" {
		t.Skip("opt-in local runtime protocol check")
	}
	m := New(t.TempDir())
	id := uuid.NewString()
	dir, err := m.directory(id)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := m.launch(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	b, err := c.call(ctx, "account/read", map[string]any{"refreshToken": false})
	if err != nil || !strings.Contains(string(b), `"account":null`) {
		t.Fatal(string(b), err)
	}
	login, err := c.call(ctx, "account/login/start", map[string]any{"type": "chatgpt"})
	if err != nil {
		t.Fatal(err)
	}
	var nativeLogin struct {
		URL string `json:"authUrl"`
		ID  string `json:"loginId"`
	}
	if json.Unmarshal(login, &nativeLogin) != nil {
		t.Fatal("invalid native login result")
	}
	u, err := url.Parse(nativeLogin.URL)
	if err != nil || u.Host != "auth.openai.com" || u.Path != "/oauth/authorize" || u.Query().Get("client_id") == "dynamic_agent_client" {
		t.Fatal("native runtime did not use Codex browser sign-in")
	}
	if _, err = c.call(ctx, "account/login/cancel", map[string]any{"loginId": nativeLogin.ID}); err != nil {
		t.Fatal(err)
	}
	b, err = c.call(ctx, "thread/start", map[string]any{"ephemeral": true, "sandbox": "read-only", "approvalPolicy": "never", "dynamicTools": []any{map[string]any{"type": "function", "name": "counterweight_inspect", "description": "fixture", "inputSchema": map[string]any{"type": "object"}}}})
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	json.Unmarshal(b, &v)
	_, err = c.call(ctx, "thread/inject_items", map[string]any{"threadId": v.Thread.ID, "items": []any{map[string]any{"type": "function_call", "call_id": "call-1", "name": "counterweight_inspect", "arguments": "{}"}, map[string]any{"type": "function_call_output", "call_id": "call-1", "output": "fixture"}}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSubscriptionNativeCancellationAndDaybreak(t *testing.T) {
	m := connectedFixture(t)
	t.Setenv("COUNTERWEIGHT_FAKE_MODE", "hang")
	p, err := m.Provider(fixtureID, "account-model", "", "daybreakBlue")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, _, _, err = p.Complete(ctx, llm.CompletionRequest{Messages: []llm.Message{llm.UserText("fixture")}})
	if err == nil {
		t.Fatal("cancelled inference succeeded")
	}
	t.Setenv("COUNTERWEIGHT_FAKE_MODE", "text")
	t.Setenv("COUNTERWEIGHT_FAKE_EXPECT_PROGRAM", "daybreakBlue")
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	_, _, _, err = p.Complete(ctx2, llm.CompletionRequest{Messages: []llm.Message{llm.UserText("fixture")}})
	if err != nil {
		t.Fatal("cancellation did not release connection", err)
	}
	if _, err = m.Provider(fixtureID, "account-model", "", "invented"); err == nil {
		t.Fatal("invalid access program accepted")
	}
}
