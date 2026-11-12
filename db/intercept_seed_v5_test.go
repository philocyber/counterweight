package db

import (
	"regexp"
	"testing"
)

func TestInterceptSeedV5(t *testing.T) {
	names := make(map[string]bool)
	for _, r := range InterceptRuleRefinementsV5() {
		if names[r.Name] || r.Name == "" {
			t.Errorf("empty or duplicate name: %q", r.Name)
		}
		names[r.Name] = true
		if r.OldPattern == r.NewPattern {
			t.Errorf("unchanged refinement: %s", r.Name)
		}
		for _, pattern := range []string{r.OldPattern, r.NewPattern} {
			if _, err := regexp.Compile(pattern); err != nil {
				t.Errorf("%s: %v", r.Name, err)
			}
		}
	}
	if len(names) != 9 {
		t.Fatalf("want 9 system-command refinements, got %d", len(names))
	}
}
