package server

import (
	"bytes"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/Philocyber/counterweight/db"
)

func TestManagerProxyDiagnosticsPreserveConfigurationWithoutLeaking(t *testing.T) {
	dsn := os.Getenv("COUNTERWEIGHT_PG_DSN")
	if !strings.Contains(dsn, "/counterweight_diagnostic_security?") {
		t.Skip("requires explicit disposable diagnostic database")
	}
	pg, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.Close() })
	for _, key := range []string{settingGlobalProxy, settingTrafficCapture} {
		prior, existed, err := pg.GetSetting(key)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if existed {
				_ = pg.SetSetting(key, prior)
			} else {
				_, _ = pg.Exec(`DELETE FROM settings WHERE key=$1`, key)
			}
		})
	}
	for _, key := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY"} {
		t.Setenv(key, "")
	}
	servers, err := pg.ListMCP()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	var previousBrowser *db.MCPServer
	for _, server := range servers {
		if server.Name == browserMCPName {
			found = true
			previousBrowser = server
		}
	}
	if previousBrowser != nil {
		t.Cleanup(func() { _, _ = pg.SaveMCP(previousBrowser) })
	}
	if !found {
		id, err := pg.SaveMCP(&db.MCPServer{Name: browserMCPName, Transport: "stdio", Command: "fixture-only-never-launched", Args: []byte(`[]`), Env: []byte(`{}`)})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = pg.DeleteMCP(id) })
	}
	if err := pg.SetBool(settingTrafficCapture, false); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"http://USER_SECRET:PASS_SECRET@127.0.0.1:8080/PATH_SECRET?token=QUERY_SECRET", "http://USER_SECRET:PASS_SECRET@127.0.0.1:8080/%zz?token=QUERY_SECRET"} {
		if err := pg.SetSetting(settingGlobalProxy, raw); err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		prior := log.Writer()
		log.SetOutput(&output)
		manager, err := NewManager(t.TempDir(), "127.0.0.1:0")
		log.SetOutput(prior)
		if err != nil {
			t.Fatal(err)
		}
		if manager.traffic == nil {
			_ = manager.Close()
			t.Fatal("recording fixture did not initialize; startup error path untested")
		}
		if manager.ProxyAddr() != raw {
			_ = manager.Close()
			t.Fatal("redaction changed active proxy")
		}
		servers, err := pg.ListMCP()
		if err != nil {
			_ = manager.Close()
			t.Fatal(err)
		}
		retained := false
		for _, server := range servers {
			if server.Name == browserMCPName {
				for _, arg := range decodeStrSlice(server.Args) {
					if arg == raw {
						retained = true
					}
				}
			}
		}
		_ = manager.Close()
		if !retained {
			t.Fatal("browser MCP lost its configured authenticated proxy")
		}
		if strings.Contains(output.String(), "SECRET") {
			t.Errorf("manager log leaked credentials: %s", output.String())
		}
		if !strings.Contains(output.String(), "Browser MCP uses capture proxy") {
			t.Fatal("browser sync was not logged")
		}
		if strings.Contains(raw, "%zz") && !strings.Contains(output.String(), "Invalid global proxy") {
			t.Fatal("startup validation error was not exercised")
		}
	}
}
