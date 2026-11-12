package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	actool "github.com/Autumn-27/norma/tool"
	"github.com/Philocyber/counterweight/agent"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/execution"
	"github.com/Philocyber/counterweight/intercept"
	"github.com/Philocyber/counterweight/locale"
	"github.com/Philocyber/counterweight/traffic"
)

func isolationConversation(t *testing.T, m *Manager, key string, tasks ...int64) *db.Conversation {
	t.Helper()
	if a, err := m.pg.GetAgentByKey(key); err != nil {
		t.Fatal(err)
	} else if a == nil {
		created, err := m.pg.CreateAgent(key, "Fixture", "Fixture")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = m.pg.Exec(`DELETE FROM agents WHERE id=$1`, created.ID) })
	}
	conv, err := m.pg.CreateConversation(key, "Fixture", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = m.pg.Exec(`DELETE FROM conversations WHERE id=$1`, conv.ID) })
	if err := m.pg.BindConversationTasks(conv.ID, tasks); err != nil {
		t.Fatal(err)
	}
	return conv
}

func TestConversationAgentsAreConfinedToTheirOwnDirectory(t *testing.T) {
	m, e := executionFixture(t)
	task := executionTask(t, m, db.AssessmentGeneral)
	taskID, _ := strconv.ParseInt(task.ID, 10, 64)
	outside := filepath.Join(m.dir, "outside.txt")
	if err := os.WriteFile(outside, []byte("outside-secret-marker"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, key string
		tasks     []int64
		writable  bool
	}{
		{"operator chat", "auto", nil, true},
		{"standalone agent", "pentest", nil, true},
		{"trigger run bound to a task", "isolation_fixture_agent", []int64{taskID}, true},
		{"source review chat", agent.CodeReviewChatAgentKey, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conv := isolationConversation(t, m, tc.key, tc.tasks...)
			ctx := intercept.WithConvID(t.Context(), conv.ID)
			roots, on, err := e.fileRoots(ctx, tc.key)
			if err != nil || !on {
				t.Fatalf("admission: %v %v", on, err)
			}
			dir := filepath.Join(m.dir, "sessions", "conv-"+strconv.FormatInt(conv.ID, 10))
			if roots.WorkingDir != dir || !slices.Equal(roots.Read, []string{dir}) || tc.writable != slices.Equal(roots.Write, []string{dir}) || !tc.writable && len(roots.Write) != 0 {
				t.Fatalf("roots: %+v", roots)
			}
			files, err := execution.Open(roots)
			if err != nil {
				t.Fatal(err)
			}
			defer files.Close()
			for _, path := range []string{outside, filepath.Join("..", "..", "outside.txt"), filepath.Join(m.dir, "tasks", task.ID)} {
				raw, _ := json.Marshal(map[string]string{"file_path": path, "path": path, "content": "changed"})
				for _, tool := range []actool.CoreTool{actool.NewRead(), actool.NewWrite()} {
					result, err := files.Tool(tool).Call(ctx, raw, &actool.ToolContext{WorkingDir: dir})
					if err != nil || !result.IsError || strings.Contains(result.Flatten(), "outside-secret-marker") {
						t.Fatalf("%s left the conversation directory through %s: %v %s", tool.Name(), path, err, result.Flatten())
					}
				}
			}
			if content, _ := os.ReadFile(outside); string(content) != "outside-secret-marker" {
				t.Fatalf("outside file changed: %q", content)
			}
		})
	}
	conv := isolationConversation(t, m, "auto")
	if err := m.pg.SetSetting(settingIsolation, "false"); err != nil {
		t.Fatal(err)
	}
	if _, on, err := e.fileRoots(intercept.WithConvID(t.Context(), conv.ID), "auto"); on || err != nil {
		t.Fatalf("isolation off: %v %v", on, err)
	}
}

func TestContainerModeConversationsRunCommandsInTheirContainer(t *testing.T) {
	// The product refuses container mode when it runs in a container itself,
	// so the fake backend below never becomes ready there.
	if execution.RunningInContainer() {
		t.Skip("container mode is refused when the server itself runs in a container")
	}
	m, e := executionFixture(t)
	runner := &lifecycleDockerRunner{}
	e.execution.ConfigureDocker(execution.DockerOptions{Runner: runner, Scope: m.dir, DataDir: m.dir})
	t.Cleanup(func() { _ = e.execution.Docker().Close() })
	if status := e.execution.ModeStatus(execution.Container); status.Status != "ready" {
		t.Fatalf("fake readiness: %+v", status)
	}
	if err := m.pg.SetSetting(settingIsolation, execution.Container); err != nil {
		t.Fatal(err)
	}
	task := executionTask(t, m, db.AssessmentGeneral)
	taskID, _ := strconv.ParseInt(task.ID, 10, 64)
	oldAugment, oldResolve := agent.ToolAugment, agent.ToolResolve
	t.Cleanup(func() { agent.ToolAugment, agent.ToolResolve = oldAugment, oldResolve })
	agent.ToolResolve = nil
	for key, kind := range map[string]string{"traffic_isolation_fixture": "command", "isolation_script_fixture": "script", "isolation_http_fixture": "http"} {
		if err := m.pg.CreateCustomTool(&db.Tool{Key: key, Kind: kind, Enabled: true, Exec: json.RawMessage(`{}`), Schema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}}}`)}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = m.pg.DeleteCustomTool(key) })
	}
	inert := func(name string) actool.CoreTool {
		return actool.Build(actool.Spec{Name: name, Schema: map[string]any{"type": "object"}})
	}
	wireAgentAugment(m.pg, t.TempDir(), func(context.Context) ([]actool.CoreTool, map[string][]string) {
		host := []actool.CoreTool{inert("traffic_isolation_fixture"), inert("isolation_script_fixture"), inert("isolation_http_fixture"), inert("create_mcp"), inert("get_task_graph"), inert(authzAgentToolName)}
		for _, key := range []string{db.FindingRetestAgentKey, db.CodeVerifierAgentKey, db.CodeFixerAgentKey, db.PatchReviewerAgentKey} {
			for _, name := range findingRunTools(key) {
				host = append(host, inert(name))
			}
		}
		for _, meta := range traffic.SeedToolMetas() {
			host = append(host, inert(meta.Name()))
		}
		return host, nil
	})
	// A finding run keeps the tools it reads its context and records its result
	// with, and only its own.
	for _, key := range []string{db.FindingRetestAgentKey, db.CodeVerifierAgentKey, db.CodeFixerAgentKey, db.PatchReviewerAgentKey} {
		conv := isolationConversation(t, m, key, taskID)
		ctx := agent.WithFileAccess(intercept.WithConvID(t.Context(), conv.ID), e.fileRoots, e.fileAccessFailed)
		tools, _, cleanup := agent.AugmentTools(ctx, key, actool.DefaultTools())
		names := map[string]bool{}
		for _, tool := range tools {
			names[tool.Name()] = true
		}
		cleanup()
		for _, other := range []string{db.FindingRetestAgentKey, db.CodeVerifierAgentKey, db.CodeFixerAgentKey, db.PatchReviewerAgentKey} {
			for _, name := range findingRunTools(other) {
				if names[name] != (other == key) {
					t.Errorf("%s in container mode: %s present=%v", key, name, names[name])
				}
			}
		}
		if names["create_mcp"] || names["get_task_graph"] || names["isolation_script_fixture"] {
			t.Errorf("%s in container mode kept other server tools: %v", key, names)
		}
	}
	for _, tc := range []struct {
		name  string
		tasks []int64
	}{{"operator chat", nil}, {"trigger run bound to a task", []int64{taskID}}} {
		t.Run(tc.name, func(t *testing.T) {
			conv := isolationConversation(t, m, "isolation_fixture_agent", tc.tasks...)
			identity := "conv-" + strconv.FormatInt(conv.ID, 10)
			ctx := agent.WithFileAccess(intercept.WithConvID(t.Context(), conv.ID), e.fileRoots, e.fileAccessFailed)
			roots, on, err := e.fileRoots(ctx, "isolation_fixture_agent")
			if err != nil || !on || roots.Mode != execution.Container || roots.TaskID != identity || roots.Backend == nil {
				t.Fatalf("container roots: %+v %v %v", roots, on, err)
			}
			tools, _, cleanup := agent.AugmentTools(ctx, "isolation_fixture_agent", actool.DefaultTools())
			defer cleanup()
			names := map[string]actool.CoreTool{}
			for _, tool := range tools {
				names[tool.Name()] = tool
			}
			for _, name := range []string{"traffic_isolation_fixture", "isolation_script_fixture", "shell_open", "shell_send", "TaskOutput"} {
				if names[name] != nil {
					t.Errorf("host-executing tool %s reached a container-mode conversation", name)
				}
			}
			for _, name := range []string{"Bash", "Read", "isolation_http_fixture", "traffic_search", "traffic_get", "traffic_blob", authzAgentToolName} {
				if names[name] == nil {
					t.Errorf("container-mode conversation lost %s", name)
				}
			}
			// The operator's own chat keeps the built-in server tools; a run
			// started from task data has the same catalog as the task's agents.
			if kept := names["create_mcp"] != nil && names["get_task_graph"] != nil; kept != (tc.tasks == nil) || !kept && (names["create_mcp"] != nil || names["get_task_graph"] != nil) {
				t.Errorf("built-in server tools: create_mcp=%v get_task_graph=%v", names["create_mcp"] != nil, names["get_task_graph"] != nil)
			}
			if names["Bash"] == nil {
				return
			}
			input, _ := json.Marshal(map[string]string{"command": "printf fixture"})
			if result, err := names["Bash"].Call(ctx, input, &actool.ToolContext{WorkingDir: roots.WorkingDir}); err != nil || result.IsError {
				t.Fatalf("container command: %v %s", err, result.Flatten())
			}
			if !slices.Contains(e.execution.Docker().TaskIDs(), identity) {
				t.Fatalf("command did not run in the conversation's container: %v", e.execution.Docker().TaskIDs())
			}
			// The container does not outlive the turn that used it.
			runner.mu.Lock()
			runner.removed = 0
			runner.mu.Unlock()
			s := &Server{m: m, engine: e, ctx: context.Background()}
			turn, cancel := context.WithCancelCause(ctx)
			s.runConversationTurn(turn, cancel, conv, "fixture", s.convBusyKey(conv.ID))
			if runner.removed != 1 || slices.Contains(e.execution.Docker().TaskIDs(), identity) {
				t.Fatalf("conversation container survived its turn: removed=%d identities=%v", runner.removed, e.execution.Docker().TaskIDs())
			}
		})
	}
}

func TestFinishedTaskIsAdmittedAgainUnderTheSelectedMode(t *testing.T) {
	m, e := executionFixture(t)
	if err := m.pg.SetSetting(settingIsolation, "false"); err != nil {
		t.Fatal(err)
	}
	task := executionTask(t, m, db.AssessmentGeneral)
	taskID, _ := strconv.ParseInt(task.ID, 10, 64)
	ctx := agent.WithRunInfo(t.Context(), agent.RunInfo{TaskID: taskID})
	if _, on, err := e.fileRoots(ctx, "worker"); on || err != nil {
		t.Fatalf("host admission: %v %v", on, err)
	}
	segments := func() (count int, last string) {
		t.Helper()
		rows, err := m.pg.Query(`SELECT mode FROM task_execution_segments WHERE task_id=$1 ORDER BY id`, taskID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			count++
			if err := rows.Scan(&last); err != nil {
				t.Fatal(err)
			}
		}
		return count, last
	}
	before, mode := segments()
	if before == 0 || mode != execution.Host {
		t.Fatalf("first segment: %d %q", before, mode)
	}
	// The operator tightens the mode while the task is still running: the task
	// keeps the mode it was admitted with until it ends.
	if err := m.pg.SetSetting(settingIsolation, "true"); err != nil {
		t.Fatal(err)
	}
	if _, on, err := e.fileRoots(ctx, "worker"); on || err != nil {
		t.Fatalf("running task changed mode: %v %v", on, err)
	}
	e.releaseExecution(task.ID) // the task reached a terminal state
	fid, err := m.pg.AddFinding(taskID, 0, "fixture", "finished task", "low", "fixture", "fixture", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = m.pg.DeleteFinding(fid) })
	_, conv, _, err := m.pg.CreateFindingRetest(t.Context(), fid, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = m.pg.Exec(`DELETE FROM conversations WHERE id=$1`, conv.ID) })
	taskDir := filepath.Join(m.dir, "tasks", task.ID)
	for name, run := range map[string]func() (execution.Roots, bool, error){
		"main agent turn": func() (execution.Roots, bool, error) { return e.fileRoots(ctx, "mainagent") },
		"finding retest": func() (execution.Roots, bool, error) {
			return e.fileRoots(intercept.WithConvID(t.Context(), conv.ID), db.FindingRetestAgentKey)
		},
	} {
		roots, on, err := run()
		if err != nil || !on || !slices.Contains(roots.Read, taskDir) {
			t.Fatalf("%s after the task finished ran under the earlier mode: %+v %v %v", name, roots, on, err)
		}
	}
	if after, mode := segments(); after != before+1 || mode != execution.ConfinedFiles {
		t.Fatalf("new admission was not recorded: %d -> %d, last=%q", before, after, mode)
	}
}

func TestPlatformDefinitionToolsWaitForAPerson(t *testing.T) {
	m, _ := executionFixture(t)
	s := &Server{m: m, skillDir: t.TempDir()}
	tools := map[string]actool.CoreTool{}
	for _, tool := range s.platformTools(locale.En) {
		tools[tool.Name()] = tool
	}
	if err := os.MkdirAll(filepath.Join(s.skillDir, "approval-fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	conv := isolationConversation(t, m, "auto")
	t.Cleanup(func() { _, _ = m.pg.Exec(`DELETE FROM intercept_pending WHERE conversation_id=$1`, conv.ID) })
	t.Cleanup(func() { _, _ = m.pg.Exec(`DELETE FROM mcp_servers WHERE name LIKE 'approval-fixture-%'`) })
	// No interception rule, no intercepted tool and no reviewer: the question
	// still reaches a person.
	prior, err := m.interceptor.GetEnabledTools()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.interceptor.SetEnabledTools(prior) })
	if err := m.interceptor.SetEnabledTools([]string{}); err != nil {
		t.Fatal(err)
	}
	decide := func(allow bool) {
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			var id int64
			if m.pg.QueryRow(`SELECT id FROM intercept_pending WHERE conversation_id=$1 AND status='pending' ORDER BY id DESC LIMIT 1`, conv.ID).Scan(&id) == nil {
				if err := m.interceptor.Decide(id, allow); err != nil {
					t.Error(err)
				}
				return
			}
		}
		t.Error("no approval request was created")
	}
	count := func(name string) (n int) {
		t.Helper()
		if err := m.pg.QueryRow(`SELECT count(*) FROM mcp_servers WHERE name=$1`, name).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	ctx := intercept.WithConvID(t.Context(), conv.ID)
	for name, input := range map[string]string{
		"create_mcp":        `{"name":"approval-fixture-denied","transport":"stdio","command":"fixture-command","args":["--fixture"]}`,
		"update_skill_file": `{"name":"approval-fixture","content":"changed instructions"}`,
	} {
		go decide(false)
		result, err := tools[name].Call(ctx, json.RawMessage(input), nil)
		if err != nil || !result.IsError {
			t.Fatalf("%s ran after a person denied it: %v %s", name, err, result.Flatten())
		}
	}
	if _, err := os.Stat(filepath.Join(s.skillDir, "approval-fixture", "SKILL.md")); !os.IsNotExist(err) || count("approval-fixture-denied") != 0 {
		t.Fatalf("a denied change took effect: skill=%v servers=%d", err, count("approval-fixture-denied"))
	}
	go decide(true)
	result, err := tools["create_mcp"].Call(ctx, json.RawMessage(`{"name":"approval-fixture-allowed","transport":"stdio","command":"fixture-command"}`), nil)
	if err != nil || result.IsError || count("approval-fixture-allowed") != 1 {
		t.Fatalf("an approved change did not take effect: %v %s", err, result.Flatten())
	}
	for _, name := range []string{"create_skill", "update_skill_file", "create_custom_tool", "update_custom_tool", "create_mcp", "update_mcp"} {
		if _, gated := tools[name].(operatorApprovedTool); !gated {
			t.Errorf("%s runs without a person's decision", name)
		}
	}
	// The gate leaves exactly these tools to their own question.
	for name, tool := range tools {
		if _, gated := tool.(operatorApprovedTool); gated != intercept.OperatorDecidedTool(name) {
			t.Errorf("%s: asks a person itself = %v, left to it by the gate = %v", name, gated, !gated)
		}
	}
	if result, err := (operatorApprovedTool{s.toolCreateMCP(locale.En), &Server{}}).Call(ctx, json.RawMessage(`{"name":"approval-fixture-unwired","transport":"stdio","command":"fixture-command"}`), nil); err != nil || !result.IsError {
		t.Fatalf("change ran without an approval gate: %v %s", err, result.Flatten())
	}
}

// In a task the call waits while the task is held. Its result carries the hold
// marker, which is what makes the worker repeat the approved call on resume,
// and the repeated call then runs without a second question.
func TestPlatformDefinitionToolResumesAfterApprovalInATask(t *testing.T) {
	m, _ := executionFixture(t)
	s := &Server{m: m, skillDir: t.TempDir()}
	task, err := m.CreateTask("platform approval fixture", "fixture", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	taskID, _ := strconv.ParseInt(task.ID, 10, 64)
	t.Cleanup(func() {
		_, _ = m.pg.Exec(`DELETE FROM intercept_pending WHERE task_id=$1`, task.ID)
		_, _ = m.pg.Exec(`DELETE FROM mcp_servers WHERE name='approval-fixture-task'`)
		_ = m.pg.DeleteTask(taskID)
	})
	m.interceptor.SetTaskApprovalHandler(func(id string, pendingID int64, expires *time.Time, action string) (bool, error) {
		n, _ := strconv.ParseInt(id, 10, 64)
		return m.pg.BeginTaskApprovalHold(n, pendingID, expires, action)
	})
	t.Cleanup(func() { m.interceptor.SetTaskApprovalHandler(nil) })
	ctx := intercept.WithTaskContext(t.Context(), task.ID, "fixture", func(db.Activity) {})
	input := json.RawMessage(`{"name":"approval-fixture-task","transport":"stdio","command":"fixture-command"}`)
	tool := operatorApprovedTool{s.toolCreateMCP(locale.En), s}
	created := func() (n int) {
		t.Helper()
		if err := m.pg.QueryRow(`SELECT count(*) FROM mcp_servers WHERE name='approval-fixture-task'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	held, err := tool.Call(ctx, input, nil)
	if err != nil || !held.IsError || !strings.Contains(held.Flatten(), intercept.ApprovalDeferredMarker) || created() != 0 {
		t.Fatalf("held call: %v %q created=%d", err, held.Flatten(), created())
	}
	var pendingID int64
	if err := m.pg.QueryRow(`SELECT id FROM intercept_pending WHERE task_id=$1 AND status='pending' ORDER BY id DESC LIMIT 1`, task.ID).Scan(&pendingID); err != nil {
		t.Fatal(err)
	}
	if err := m.interceptor.Decide(pendingID, true); err != nil {
		t.Fatal(err)
	}
	if err := m.pg.RefreshTaskApprovalReplays(taskID); err != nil {
		t.Fatal(err)
	}
	done, err := tool.Call(ctx, input, nil)
	if err != nil || done.IsError || created() != 1 {
		t.Fatalf("approved call did not run on its repeat: %v %q created=%d", err, done.Flatten(), created())
	}
	var asked int
	if err := m.pg.QueryRow(`SELECT count(*) FROM intercept_pending WHERE task_id=$1`, task.ID).Scan(&asked); err != nil || asked != 1 {
		t.Fatalf("the one call was put to a person %d times (%v)", asked, err)
	}
}

func TestTrafficCallerIsResolvedFromServerRecords(t *testing.T) {
	m, _ := executionFixture(t)
	task := executionTask(t, m, db.AssessmentGeneral)
	taskID, _ := strconv.ParseInt(task.ID, 10, 64)
	resolve := trafficCallerTasks(m.pg)
	retest := func(findingTask int64) int64 {
		t.Helper()
		fid, err := m.pg.AddFinding(findingTask, 0, "fixture", "traffic caller", "low", "fixture", "fixture", "worker", nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = m.pg.DeleteFinding(fid) })
		_, conv, _, err := m.pg.CreateFindingRetest(t.Context(), fid, "")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = m.pg.Exec(`DELETE FROM conversations WHERE id=$1`, conv.ID) })
		return conv.ID
	}
	conversation := func(id int64) context.Context { return intercept.WithConvID(t.Context(), id) }
	for name, tc := range map[string]struct {
		ctx   context.Context
		tasks []int64
		bound bool
	}{
		"no identity":                {t.Context(), nil, false},
		"task run":                   {agent.WithRunInfo(t.Context(), agent.RunInfo{TaskID: taskID}), []int64{taskID}, true},
		"task run without its id":    {agent.WithRunInfo(t.Context(), agent.RunInfo{AssessmentType: db.AssessmentGeneral}), nil, true},
		"operator chat":              {conversation(isolationConversation(t, m, "auto").ID), nil, false},
		"trigger run":                {conversation(isolationConversation(t, m, "auto", taskID).ID), []int64{taskID}, true},
		"finding run of a task":      {conversation(retest(taskID)), []int64{taskID}, true},
		"finding run without a task": {conversation(retest(0)), nil, true},
	} {
		tasks, bound, err := resolve(tc.ctx)
		if err != nil || bound != tc.bound || !slices.Equal(tasks, tc.tasks) {
			t.Errorf("%s: tasks=%v bound=%v err=%v, want tasks=%v bound=%v", name, tasks, bound, err, tc.tasks, tc.bound)
		}
	}
}
