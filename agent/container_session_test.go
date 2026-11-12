package agent

import (
	"context"
	"iter"
	"testing"

	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/llm"
	actool "github.com/Autumn-27/norma/tool"
	"github.com/Philocyber/counterweight/execution"
)

type containerSessionProvider struct {
	captureUsageProvider
	names map[string]bool
}

func (p containerSessionProvider) Stream(_ context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	for _, tool := range req.Tools {
		p.names[tool.Name] = true
	}
	return func(yield func(llm.StreamEvent, error) bool) {
		yield(llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "end_turn"}, nil)
	}
}

func TestContainerProductionSessionBackgroundTools(t *testing.T) {
	for _, mode := range []string{execution.Host, execution.ConfinedFiles, execution.Container} {
		t.Run(mode, func(t *testing.T) {
			containerCatalogFixture(t, mode, &containerBackendFixture{})
			tools, _, closeTools := AugmentTools(t.Context(), "worker", []actool.CoreTool{actool.NewBash()})
			defer closeTools()
			names := map[string]bool{}
			provider := containerSessionProvider{names: names}
			// captureRun applies the production options hook and constructs the SDK session.
			_, _, err := captureRun(t.Context(), agentcore.Options{Provider: provider, Tools: tools, WorkingDir: t.TempDir(), MaxTurns: 1}, "fixture", nil)
			if err != nil {
				t.Fatal(err)
			}
			if !names["Bash"] {
				t.Fatal("provider did not receive the tool catalog")
			}
			for _, name := range []string{"TaskOutput", "TaskStop", "TaskList", "Monitor"} {
				if names[name] != (mode != execution.Container) {
					t.Fatalf("%s availability in %s: %v", name, mode, names[name])
				}
			}
		})
	}
}
