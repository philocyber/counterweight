package server

import (
	"testing"

	"github.com/Philocyber/counterweight/db"
)

func TestDescribeFindingSelection(t *testing.T) {
	cases := []struct {
		scope    string
		filter   db.FindingFilter
		selected int
		want     string
	}{
		{"selected", db.FindingFilter{}, 3, "Selected findings (3)"},
		{"all", db.FindingFilter{Severity: "high"}, 0, "All findings in the register"},
		{"filtered", db.FindingFilter{}, 0, "Filtered view with no active filters (all findings)"},
		{"", db.FindingFilter{Severity: "high", TaskID: db.FindingUnassignedTask, Query: "sqli"}, 0, "Filtered: severity=high, task=unassigned, search=sqli"},
	}
	for _, c := range cases {
		if got := describeFindingSelection(c.scope, c.filter, c.selected); got != c.want {
			t.Errorf("%q: got %q, want %q", c.scope, got, c.want)
		}
	}
}
