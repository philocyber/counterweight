package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestSuperviseHelper is the child process: it exits with the next code from
// COUNTERWEIGHT_SUPERVISE_CODES, tracking its position in a counter file.
func TestSuperviseHelper(t *testing.T) {
	codes := os.Getenv("COUNTERWEIGHT_SUPERVISE_CODES")
	if codes == "" {
		t.Skip("helper process only")
	}
	counter := os.Getenv("COUNTERWEIGHT_SUPERVISE_COUNTER")
	raw, _ := os.ReadFile(counter)
	n, _ := strconv.Atoi(string(raw))
	_ = os.WriteFile(counter, []byte(strconv.Itoa(n+1)), 0o644)
	code, _ := strconv.Atoi(strings.Split(codes, ",")[n])
	os.Exit(code)
}

func TestSuperviseLoop(t *testing.T) {
	cases := []struct {
		name  string
		codes string
		runs  int
	}{
		{"exit zero stops", "0", 1},
		{"update restart relaunches", "75,0", 2},
		{"failure backs off and relaunches", "3,3,0", 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			counter := filepath.Join(t.TempDir(), "count")
			t.Setenv("COUNTERWEIGHT_SUPERVISE_CODES", tc.codes)
			t.Setenv("COUNTERWEIGHT_SUPERVISE_COUNTER", counter)
			if got := superviseLoop(os.Args[0], []string{"-test.run=^TestSuperviseHelper$"}, time.Millisecond); got != 0 {
				t.Fatalf("superviseLoop returned %d, want 0", got)
			}
			raw, _ := os.ReadFile(counter)
			if string(raw) != strconv.Itoa(tc.runs) {
				t.Fatalf("child ran %s times, want %d", raw, tc.runs)
			}
		})
	}
}
