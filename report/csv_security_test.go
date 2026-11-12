package report

import (
	"encoding/csv"
	"strings"
	"testing"

	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/locale"
)

func TestFindingsCSVTreatsUntrustedCellsAsText(t *testing.T) {
	for _, lang := range []locale.Lang{locale.En} {
		for _, payload := range []string{"=1+1", "+1", "-1", "@SUM(1,2)", "\t=1", "\r=1", "\n=1", "  =1", "\uFEFF=1", "＝1", "＋1", "－1", "＠SUM(1,2)", `=1+1";=1+1`} {
			f := &db.DBFinding{ID: 42, Name: payload, VulnClass: payload, Severity: payload, Status: payload, TaskDescription: payload, Summary: payload}
			original := *f
			data := strings.TrimPrefix(string(FindingsCSV([]*db.DBFinding{f}, lang)), "\uFEFF")
			rows, err := csv.NewReader(strings.NewReader(data)).ReadAll()
			if err != nil || len(rows) != 2 {
				t.Fatalf("invalid CSV for %q: %v", payload, err)
			}
			for _, index := range []int{1, 2, 4, 5, 7} {
				want := payload
				if index == 7 {
					want = strings.TrimSpace(want)
				}
				if rows[1][index] != "\t"+want {
					t.Fatalf("cell %d remained executable or lost data: got %q want %q", index, rows[1][index], "\t"+want)
				}
			}
			if !strings.Contains(data, "\"\t") {
				t.Fatal("formula prefix is not inside a quoted CSV field")
			}
			if rows[1][0] != "42" || rows[1][8] != "0" {
				t.Fatal("numeric export fields changed")
			}
			if f.Name != original.Name || f.Summary != original.Summary || f.VulnClass != original.VulnClass {
				t.Fatal("export modified stored finding content")
			}
		}
	}
}

func TestFindingsCSVPreservesOrdinaryTextAndDelimiters(t *testing.T) {
	text := "user café, quoted \"text\""
	f := &db.DBFinding{ID: 1, Name: text, Summary: text}
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(FindingsCSV([]*db.DBFinding{f})), "\uFEFF"))).ReadAll()
	if err != nil || rows[1][1] != text || rows[1][7] != text {
		t.Fatalf("ordinary text changed: rows=%v err=%v", rows, err)
	}
}
