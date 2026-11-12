package notify

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

// These tests cover SSRF protection for local/cloud-metadata destinations and credential redaction in
// URL validation. TestMain enables loopback for ordinary httptest receivers; each SSRF test explicitly
// clears that opt-in to verify default rejection.

func TestMain(m *testing.M) {
	// Allow ordinary tests to reach local receivers; SSRF cases temporarily clear this setting.
	_ = os.Setenv(AllowLocalTargetsEnv, "1")
	os.Exit(m.Run())
}

// TestDialGuardRejectsLoopbackByDefault requires connection-layer rejection of loopback delivery under
// default configuration.
func TestDialGuardRejectsLoopbackByDefault(t *testing.T) {
	var hit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit = true
		_, _ = io.WriteString(w, `{"errcode":0}`)
	}))
	defer srv.Close()

	t.Setenv(AllowLocalTargetsEnv, "") // Disable the opt-in to restore default behavior.
	_, err := (dingTalkChannel{}).Send(context.Background(),
		map[string]any{"webhook": srv.URL + "/robot/send"}, Message{Items: []Item{{Severity: "high"}}})
	if err == nil {
		t.Fatal("Loopback delivery must be blocked by default")
	}
	if hit {
		t.Fatal("Request reached local service; guard failed")
	}
	// Explain how to opt in because local SMTP relays are legitimate deployments.
	if !strings.Contains(err.Error(), AllowLocalTargetsEnv) {
		t.Errorf("Rejection must explain explicit opt-in: %v", err)
	}
}

// TestDialGuardAllowsLoopbackWhenOptedIn preserves explicitly enabled local postfix/relay deployments.
func TestDialGuardAllowsLoopbackWhenOptedIn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"errcode":0,"errmsg":"ok"}`)
	}))
	defer srv.Close()

	t.Setenv(AllowLocalTargetsEnv, "1")
	if _, err := (dingTalkChannel{}).Send(context.Background(),
		map[string]any{"webhook": srv.URL + "/robot/send"}, Message{Items: []Item{{Severity: "high"}}}); err != nil {
		t.Fatalf("Delivery must work after explicit opt-in: %v", err)
	}
}

func TestIsBlockedDialIP(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "127.1.2.3", "::1",
		"169.254.169.254", // Cloud metadata endpoint, a primary reason for this guard.
		"169.254.1.1", "fe80::1",
		"0.0.0.0", "::",
		"224.0.0.1", "ff02::1",
		"::ffff:127.0.0.1", // Normalize IPv4-mapped addresses before checking to prevent bypasses.
		"",
	}
	for _, s := range blocked {
		if !isBlockedDialIP(net.ParseIP(s)) {
			t.Errorf("%s must be rejected", s)
		}
	}
	// Intentionally allow RFC1918 private networks for internal Mattermost/SMTP deployments. This
	// assertion forces a deliberate decision before tightening the policy and silently breaking those
	// deployments.
	allowed := []string{"10.0.0.5", "172.16.3.4", "192.168.1.10", "8.8.8.8", "2606:4700::1111"}
	for _, s := range allowed {
		if isBlockedDialIP(net.ParseIP(s)) {
			t.Errorf("%s must be allowed (private networks are legitimate destinations)", s)
		}
	}
}

// TestValidateHTTPURLRejectsLiteralPrivateTargets provides save-time feedback for blocked literal IPs
// instead of waiting for first delivery.
func TestValidateHTTPURLRejectsLiteralPrivateTargets(t *testing.T) {
	t.Setenv(AllowLocalTargetsEnv, "")
	for _, raw := range []string{
		"http://127.0.0.1:8080/hook",
		"http://169.254.169.254/latest/meta-data/",
		"http://[::1]:8080/hook",
	} {
		if err := validateHTTPURL(raw); err == nil {
			t.Errorf("%s must be rejected during configuration", raw)
		}
	}
	// Public and RFC1918 private addresses remain accepted; the dial policy also intentionally allows
	// RFC1918.
	for _, raw := range []string{"https://oapi.dingtalk.com/robot/send", "http://10.0.0.9/hook"} {
		if err := validateHTTPURL(raw); err != nil {
			t.Errorf("%s must pass validation: %v", raw, err)
		}
	}
}

// TestValidateHTTPURLErrorNeverLeaksCredentials covers the previously missed url.Parse failure path.
// Earlier redaction covered only http.Client.Do, while file/gopher/ftp tests parsed successfully and
// exercised scheme rejection. Their passing results did not prove malformed-URL errors safe.
func TestValidateHTTPURLErrorNeverLeaksCredentials(t *testing.T) {
	cases := []string{
		"http://127.0.0.1/%zz?access_token=" + leakProbeToken,         // Invalid percent escape.
		"https://a.example.com:port/x?access_token=" + leakProbeToken, // Nonnumeric port.
		"http://[::1?access_token=" + leakProbeToken,                  // Unmatched bracket.
	}
	for _, raw := range cases {
		// First prove the input actually fails url.Parse; otherwise the test could silently exercise a
		// different branch and provide false assurance.
		if _, err := url.Parse(raw); err == nil {
			t.Errorf("%q must fail parsing or this case does not cover the intended branch", raw)
			continue
		}
		err := validateHTTPURL(raw)
		if err == nil {
			t.Errorf("%q must fail validation", raw)
			continue
		}
		assertNoSecret(t, err.Error(), leakProbeToken)
	}
	// Also verify that channel-level wrapping does not expose the address.
	t.Setenv(AllowLocalTargetsEnv, "")
	err := (dingTalkChannel{}).Validate(map[string]any{"webhook": cases[0]})
	if err == nil {
		t.Fatal("Invalid URL must fail validation")
	}
	assertNoSecret(t, err.Error(), leakProbeToken)
}

// TestEmailDialGuardRejectsLoopbackByDefault covers SMTP's former bare-net.Dialer SSRF gap.
// Localhost/metadata connections could leak greeting lines through handshake errors and
// last_error/history, or reveal ports through timing. Explicitly clear TestMain's loopback opt-in;
// otherwise the test would pass with or without the guard, as earlier coverage did.
func TestEmailDialGuardRejectsLoopbackByDefault(t *testing.T) {
	f := newFakeSMTP(t)
	cfg := emailCfg(t, f, nil)

	t.Setenv(AllowLocalTargetsEnv, "") // Disable the opt-in to restore default behavior.
	_, err := (emailChannel{}).Send(context.Background(), cfg, singleMsg())
	if err == nil {
		t.Fatal("SMTP loopback delivery must be blocked by default")
	}
	// The Control hook must prevent connection establishment, so EHLO is never sent.
	if f.sawCommand("EHLO") || f.sawCommand("HELO") {
		t.Fatal("SMTP session established; guard failed")
	}
	// Explain the opt-in because local postfix relays are legitimate.
	if !strings.Contains(err.Error(), AllowLocalTargetsEnv) {
		t.Errorf("Rejection must explain explicit opt-in: %v", err)
	}
}

// TestEmailDialGuardAllowsLoopbackWhenOptedIn verifies explicitly enabled internal SMTP/local relays
// still deliver successfully.
func TestEmailDialGuardAllowsLoopbackWhenOptedIn(t *testing.T) {
	f := newFakeSMTP(t)
	cfg := emailCfg(t, f, nil)

	t.Setenv(AllowLocalTargetsEnv, "1")
	if _, err := (emailChannel{}).Send(context.Background(), cfg, singleMsg()); err != nil {
		t.Fatalf("Local SMTP must work after explicit opt-in: %v", err)
	}
	if !f.sawCommand("EHLO") {
		t.Fatal("No EHLO observed; session was not established")
	}
}
