package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Autumn-27/norma/skill"
)

func TestBundledAISkillsLoad(t *testing.T) {
	root := filepath.Join("..", "skills")
	reg, err := skill.LoadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"ai-red-team-method", "ai-target-recon", "ai-prompt-injection-testing",
		"ai-agent-workflows", "ai-rag-retrieval", "ai-mcp-tool-trust",
		"ai-model-supply-chain", "ai-llm-output-sinks",
		"ai-serving-infrastructure", "ai-control-validation",
	} {
		loaded, ok := reg.Get(name)
		if !ok || loaded.Dir == "" {
			t.Errorf("bundled skill %q was not loaded", name)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "ai-prompt-injection-testing", "references", "payload-catalog.md")); err != nil {
		t.Fatalf("payload catalog unavailable to skill: %v", err)
	}
}
