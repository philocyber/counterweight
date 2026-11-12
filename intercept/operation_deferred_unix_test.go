//go:build linux

package intercept

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestNativeBashDeferredPayloadExecutesInertFixture(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct{ name, tail string }{
		{"trap", `trap "r'm' -rf /fixture" EXIT`},
		{"alias", `shopt -s expand_aliases; alias cleanup="r'm' -rf /fixture"; eval cleanup`},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			// A local function intercepts the rm name and only writes a marker.
			// No real rm executable or destructive operation runs in this fixture.
			program := `rm() { printf fixture >> "$FIXTURE_MARKER"; }; ` + fixture.tail
			input, _ := json.Marshal(map[string]string{"command": program})
			i := &Interceptor{cached: []compiledRule{semanticFixture(`\brm\s`, "deny")}}
			if decision, hit := i.Match("Bash", input); !hit || decision.Action != "deny" {
				t.Fatalf("deferred payload escaped: %+v %v", decision, hit)
			}
			marker := filepath.Join(t.TempDir(), "marker")
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bash, "--noprofile", "--norc", "-c", program)
			cmd.Env = []string{"PATH=/usr/bin:/bin", "FIXTURE_MARKER=" + marker}
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("inert bash fixture: %v %s", err, output)
			}
			if body, err := os.ReadFile(marker); err != nil || string(body) != "fixture" {
				t.Fatalf("deferred code did not run: %q %v", body, err)
			}
		})
	}
}
