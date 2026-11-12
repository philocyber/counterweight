package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Philocyber/counterweight/agent"
	"github.com/Philocyber/counterweight/db"
	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
)

func TestToolRegistryFailureCannotRestoreSDKDefaults(t *testing.T) {
	dsn, _, err := db.DSN()
	if err != nil {
		t.Skip("requires disposable PostgreSQL")
	}
	pg, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.Close() })
	prior := agent.ToolResolve
	t.Cleanup(func() { agent.ToolResolve = prior })
	priorAugment, priorBinding := agent.ToolAugment, agent.FindingTrafficBindingEnabled
	agent.ToolAugment = nil
	t.Cleanup(func() { agent.ToolAugment, agent.FindingTrafficBindingEnabled = priorAugment, priorBinding })
	wireTools(pg, nil)
	if err := pg.Close(); err != nil {
		t.Fatal(err)
	}
	for _, binding := range []bool{false, true} {
		agent.FindingTrafficBindingEnabled = func() bool { return binding }
		tools, _, cleanup := agent.AugmentTools(t.Context(), "worker", actool.DefaultTools())
		cleanup()
		if tools == nil || len(tools) != 0 {
			t.Fatalf("unavailable registry returned unrestricted tools: nil=%v count=%d", tools == nil, len(tools))
		}
		// Use the actual SDK without hooks: the empty list itself must suppress its
		// nil-list default fallback. This proves correctness independent of the interception hooks.
		path := filepath.Join(t.TempDir(), "must-not-write.txt")
		provider := &taskGuardFixtureProvider{path: path}
		session := agentcore.NewSession(agentcore.Options{Provider: provider, Tools: tools, PermissionMode: permission.ModeBypass, WorkingDir: t.TempDir(), MaxTurns: 2, DisableBackgroundTasks: true})
		defer session.Close()
		for _, err := range session.Prompt(context.Background(), "attempt the isolated fixture write") {
			if err != nil {
				t.Fatal(err)
			}
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("SDK restored its Write default: %v", err)
		}
	}
}
