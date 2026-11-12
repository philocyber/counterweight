package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Philocyber/counterweight/traffic"
)

func TestManagerRejectsInvalidRecordingTLSBeforeStores(t *testing.T) {
	for _, options := range []traffic.TLSOptions{
		{InsecureOrigins: []string{"http://fixture.test"}},
		{InsecureOrigins: []string{"https://user:SECRET@fixture.test"}},
		{CAFile: filepath.Join(t.TempDir(), "missing.pem")},
	} {
		dir := filepath.Join(t.TempDir(), "unopened")
		manager, err := NewManager(dir, "127.0.0.1:0", options)
		if manager != nil || err == nil || strings.Contains(err.Error(), "SECRET") {
			t.Fatalf("invalid TLS configuration was not rejected safely: manager=%v err=%v", manager != nil, err)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatal("TLS validation happened after creating stores")
		}
	}
}
