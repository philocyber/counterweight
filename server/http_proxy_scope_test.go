package server

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Philocyber/counterweight/agent"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/locale"
	"github.com/jackc/pgx/v5"
)

func TestHTTPProxyDestinationIPRestrictions(t *testing.T) {
	if os.Getenv("COUNTERWEIGHT_DESTINATION_E2E") != "1" {
		t.Skip("requires explicit disposable transport database")
	}
	config, err := pgx.ParseConfig(os.Getenv("COUNTERWEIGHT_PG_DSN"))
	if err != nil || !strings.HasPrefix(config.Database, "counterweight_transport_") {
		t.Fatal("explicit disposable transport database required")
	}
	d, err := db.Open(os.Getenv("COUNTERWEIGHT_PG_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	// Isolate this policy matrix within the explicit disposable fixture and
	// restore every previously enabled IP rule before releasing the database.
	prior, err := d.ListAssetInterceptRules()
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range prior {
		if rule.Enabled && (rule.Kind == "exact_ip" || rule.Kind == "fuzzy_ip" || rule.Kind == "cidr") {
			if err := d.ToggleAssetInterceptRule(rule.ID, false); err != nil {
				t.Fatal(err)
			}
			defer d.ToggleAssetInterceptRule(rule.ID, true)
		}
	}
	task, err := d.CreateTask("proxy scope fixture", "test", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(task.ID)
	endpoint := "http://fixture.invalid/path?token=SYNTHETIC_PROXY_SECRET"
	for _, dest := range []string{endpoint, "http://203.0.113.1/", "http://203.0.113.2/", "http://203.0.113.2/redirect"} {
		if _, err := d.Assets().CreateTaskInterceptRule(task.ID, "allow", "exact_url", dest, "fixture", true); err != nil {
			t.Fatal(err)
		}
	}
	var requests atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, endpoint, http.StatusFound)
			return
		}
		w.Write([]byte("proxy fixture"))
	}))
	defer proxy.Close()
	s := &Server{m: &Manager{assets: d.Assets()}}
	ctx := agent.WithRunInfo(locale.WithLang(t.Context(), locale.En), agent.RunInfo{TaskID: task.ID})
	call := func(dest string, blocked bool) {
		t.Helper()
		before := requests.Load()
		raw, _ := json.Marshal(httpExec{URL: dest, Proxy: proxy.URL})
		res, err := s.runHTTPTool(ctx, raw, nil, nil)
		if err != nil || res.IsError != blocked {
			t.Fatalf("proxy policy result: %s %v", res.Flatten(), err)
		}
		if blocked {
			if requests.Load() != before || strings.Contains(res.Flatten(), "SYNTHETIC_PROXY_SECRET") {
				t.Fatal("blocked request reached proxy or leaked token")
			}
		} else if requests.Load() != before+1 {
			t.Fatal("permitted proxy request not forwarded")
		}
	}
	call(endpoint, false)
	for _, fixture := range []struct{ kind, pattern string }{{"exact_ip", "203.0.113.1"}, {"fuzzy_ip", "203.0.113."}, {"cidr", "203.0.113.0/24"}} {
		for _, global := range []bool{false, true} {
			var rule db.AssetInterceptRule
			if global {
				rule, err = d.CreateAssetInterceptRule(fixture.kind, fixture.pattern, "fixture", true)
			} else {
				rule, err = d.Assets().CreateTaskInterceptRule(task.ID, "block", fixture.kind, fixture.pattern, "fixture", true)
			}
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer func() {
					if global {
						d.DeleteAssetInterceptRule(rule.ID)
					} else {
						d.Assets().DeleteTaskInterceptRule(task.ID, rule.ID)
					}
				}()
				call(endpoint, true)
				if fixture.kind == "exact_ip" {
					call("http://203.0.113.1/", true)
					call("http://203.0.113.2/", false)
					before := requests.Load()
					raw, _ := json.Marshal(httpExec{URL: "http://203.0.113.2/redirect", Proxy: proxy.URL})
					res, err := s.runHTTPTool(ctx, raw, nil, nil)
					if err != nil || !res.IsError || requests.Load() != before+1 || strings.Contains(res.Flatten(), "SYNTHETIC_PROXY_SECRET") {
						t.Fatalf("redirect skipped proxy destination gate: %d %s %v", requests.Load()-before, res.Flatten(), err)
					}
				}
			}()
			call(endpoint, false)
		}
	}
	t.Run("locally_resolved_hostname_tunnel", func(t *testing.T) {
		var targetRequests, connects atomic.Int32
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			targetRequests.Add(1)
			if !strings.HasPrefix(r.Host, "localhost:") {
				t.Errorf("lost logical Host: %s", r.Host)
			}
			w.Write([]byte("pinned policy fixture"))
		}))
		defer target.Close()
		targetURL, _ := url.Parse(target.URL)
		proxy := httptest.NewServer(pinnedTunnelFixture(t, targetURL.Host, &connects))
		defer proxy.Close()
		endpoint := "http://localhost:" + targetURL.Port() + "/"
		if _, err := d.Assets().CreateTaskInterceptRule(task.ID, "allow", "exact_url", endpoint, "fixture", true); err != nil {
			t.Fatal(err)
		}
		// Localhost may resolve to both families; reject IPv6, then prove the
		// surviving IPv4 address travels through the configured tunnel.
		ipv6, err := d.Assets().CreateTaskInterceptRule(task.ID, "block", "cidr", "::1/128", "fixture", true)
		if err != nil {
			t.Fatal(err)
		}
		defer d.Assets().DeleteTaskInterceptRule(task.ID, ipv6.ID)
		raw, _ := json.Marshal(httpExec{URL: endpoint, Proxy: proxy.URL})
		res, err := s.runHTTPTool(ctx, raw, nil, nil)
		if err != nil || res.IsError || targetRequests.Load() != 1 || connects.Load() != 1 {
			t.Fatalf("permitted hostname tunnel failed: %s %v %d %d", res.Flatten(), err, targetRequests.Load(), connects.Load())
		}
		blocked, err := d.Assets().CreateTaskInterceptRule(task.ID, "block", "exact_ip", net.ParseIP("127.0.0.1").String(), "fixture", true)
		if err != nil {
			t.Fatal(err)
		}
		defer d.Assets().DeleteTaskInterceptRule(task.ID, blocked.ID)
		res, err = s.runHTTPTool(ctx, raw, nil, nil)
		if err != nil || !res.IsError || targetRequests.Load() != 1 || connects.Load() != 1 {
			t.Fatalf("new IP block reached tunnel: %s %v %d %d", res.Flatten(), err, targetRequests.Load(), connects.Load())
		}
	})
	failureDB, err := db.Open(os.Getenv("COUNTERWEIGHT_PG_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	store := failureDB.Assets()
	if err := failureDB.Close(); err != nil {
		t.Fatal(err)
	}
	dest, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CheckHTTPProxyDestination(locale.En, task.ID, dest); err == nil {
		t.Fatal("unavailable proxy policy failed open")
	}
}
