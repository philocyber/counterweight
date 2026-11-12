package agent

import (
	"strings"
	"testing"

	"github.com/Philocyber/counterweight/locale"
)

func TestBuiltinPromptLanguageAndInterpolation(t *testing.T) {
	old := PromptOverride
	PromptOverride = nil
	defer func() { PromptOverride = old }()
	en := renderSystem("planner", plannerDefaultTmpl, PlannerVars{Goal: "user goal / café"}, locale.En)
	if !strings.Contains(en, "You are the planner") {
		t.Fatal("prompt language selection failed")
	}
	if !strings.Contains(en, "user goal / café") || strings.Contains(en, "{{.Goal}}") {
		t.Fatal("goal altered or not interpolated")
	}
}

func TestEditedPromptIsNotTranslated(t *testing.T) {
	old := PromptOverride
	defer func() { PromptOverride = old }()
	const custom = "user's own custom template {{.Goal}}"
	PromptOverride = func(string) (string, bool) { return custom, true }
	got := renderSystem("planner", plannerDefaultTmpl, PlannerVars{Goal: "original"}, locale.En)
	if got != "user's own custom template original" {
		t.Fatalf("edited prompt changed: %q", got)
	}
}
