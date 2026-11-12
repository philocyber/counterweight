//go:build windows

package intercept

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// This deliberately sends a DELETE only to a synthetic local handler to prove
// the method supplied by a private rc file. No external target or user rc is used.
func TestNativeCurlConfigurationAndExpansionChangeMethod(t *testing.T) {
	curl := filepath.Join(os.Getenv("SystemRoot"), "System32", "curl.exe")
	if _, err := os.Stat(curl); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	rc := filepath.Join(home, ".curlrc")
	if err := os.WriteFile(rc, []byte("request = \"DELETE\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	methods := make(chan string, 4)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods <- r.Method
		w.Write([]byte("local fixture"))
	}))
	defer target.Close()
	for _, fixture := range []struct {
		command        string
		args           []string
		method, action string
	}{
		{"curl.exe " + target.URL, nil, "DELETE", "ask"},
		{"curl -s -q " + target.URL, []string{"-s", "-q"}, "DELETE", "ask"},
		{"curl -q " + target.URL, []string{"-q"}, "GET", ""},
		{"curl -q -K fixture.conf " + target.URL, []string{"-q", "-K", rc}, "DELETE", "ask"},
		{"curl -q --variable method=DELETE --expand-request '{{method}}' " + target.URL,
			[]string{"-q", "--variable", "method=DELETE", "--expand-request", "{{method}}"}, "DELETE", "ask"},
		{"curl -q --variable config=fixture.conf --expand-config '{{config}}' " + target.URL,
			[]string{"-q", "--variable", "config=" + rc, "--expand-config", "{{config}}"}, "DELETE", "ask"},
	} {
		input, _ := json.Marshal(map[string]string{"command": fixture.command})
		i := &Interceptor{cached: []compiledRule{semanticFixture(`method: 'DELETE'`, "deny")}}
		decision, hit := i.Match("Bash", input)
		want := fixture.action
		if want == "" {
			want = "ask"
			assertUnconfinedShellReview(t, i, input)
		}
		if !hit || decision.Action != want {
			t.Fatalf("configuration review: %q %+v %v", fixture.command, decision, hit)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		args := append(append([]string{}, fixture.args...), "--max-time", "3", target.URL)
		cmd := exec.CommandContext(ctx, curl, args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000}
		for _, entry := range os.Environ() {
			key, _, _ := strings.Cut(entry, "=")
			switch strings.ToUpper(key) {
			case "CURL_HOME", "HOME", "APPDATA", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY":
				continue
			}
			cmd.Env = append(cmd.Env, entry)
		}
		cmd.Env = append(cmd.Env, "CURL_HOME="+home, "HOME="+home, "APPDATA="+home, "NO_PROXY=*")
		output, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("native fixture failed: %v %s", err, output)
		}
		select {
		case method := <-methods:
			if method != fixture.method {
				t.Fatalf("native method %s, want %s", method, fixture.method)
			}
		default:
			t.Fatal("native curl did not reach local handler")
		}
	}
}
