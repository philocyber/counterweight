package server

import (
	"testing"

	"github.com/Philocyber/counterweight/codesrc"
	"github.com/Philocyber/counterweight/db"
)

func TestChangeClassifierReadsAnyChangedFile(t *testing.T) {
	change := &codesrc.ChangeSet{Files: []codesrc.ChangedFile{
		{Path: "web/login.jsp", Status: "modified", Ranges: []codesrc.LineRange{{Start: 10, End: 20}}},
		{Path: "src/routes/+page.server.ts", Status: "modified", Ranges: []codesrc.LineRange{{Start: 5, End: 8}}},
		{Path: "app/[id]/route.ts", Status: "modified", Ranges: []codesrc.LineRange{{Start: 1, End: 3}}},
		{Path: "Dockerfile", Status: "modified", Ranges: []codesrc.LineRange{{Start: 2, End: 2}}},
		{Path: "main.go", Status: "modified", Ranges: []codesrc.LineRange{{Start: 1, End: 30}}},
		{Path: "lib/untouched_lines.py", Status: "modified", Ranges: []codesrc.LineRange{{Start: 50, End: 60}}},
	}}
	in := changeClassifier(change, "/work/clone")
	for _, tc := range []struct {
		name, evidence string
		want           bool
	}{
		{"relative jsp", "web/login.jsp:12 concatenates the query", true},
		{"absolute jsp", "see /work/clone/web/login.jsp:12-14", true},
		{"plus in path", "at src/routes/+page.server.ts:6", true},
		{"brackets, absolute", "C:/work/clone/app/[id]/route.ts:2 reads params", true},
		{"no extension", "`Dockerfile:2` runs as root", true},
		{"changed file, unchanged lines", "lib/untouched_lines.py:1 is old code", false},
		{"changed file, other lines", "web/login.jsp:99", false},
		{"tail of another name is no citation, so no location", "notweb/login.jsp:99", true},
		{"no location at all", "the design is weak", true},
		{"host and port only", "listens on localhost:3000", true},
		{"unlisted file with unreadable citation", "legacy/page.aspx:7", true},
		{"known file outside the change", "lib/old.py:1", false},
		{"same name in another directory", "cmd/main.go:12 parses the flag", false},
		{"root file behind the checkout directory", "/work/clone/main.go:12", true},
		{"same name deeper in the checkout", "/work/clone/cmd/main.go:12", false},
		{"root file behind another mount of the checkout", "/app/data/review/main.go:12", true},
	} {
		if got := in(&db.DBFinding{Evidence: tc.evidence}); got != tc.want {
			t.Errorf("%s: in change = %v, want %v", tc.name, got, tc.want)
		}
	}
}
