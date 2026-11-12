package server

import (
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/locale"
	"testing"
)

func TestSeededAgentMetadataLocalePreservesEdits(t *testing.T) {
	for _, tc := range []struct{ key, name string }{
		{"reporter", "Report writer"},
		{db.FindingRetestAgentKey, "Finding retest"},
	} {
		original := &db.Agent{Key: tc.key, Name: tc.name, Description: "Custom description café"}
		result := agentDTO(original, locale.En)
		if result.Name != tc.name || result.Description != original.Description || original.Name != tc.name {
			t.Fatalf("stock metadata localization changed custom/stored content: %+v", result)
		}
		original.Name = "Custom name café"
		if result := agentDTO(original, locale.En); result.Name != original.Name {
			t.Fatal("custom name translated")
		}
	}
}
