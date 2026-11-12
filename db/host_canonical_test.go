package db

import "testing"

func TestAssetGateComparesCanonicalHosts(t *testing.T) {
	block := func(kind, pattern string) []AssetInterceptRule {
		return []AssetInterceptRule{{Kind: kind, Pattern: pattern, Enabled: true}}
	}
	cases := []struct {
		name    string
		rules   []AssetInterceptRule
		domains []string
		urls    []string
		blocked bool
	}{
		{"full-width letters", block("exact_domain", "blocked.example"), []string{"ｂｌｏｃｋｅｄ.example"}, nil, true},
		{"ideographic full stop", block("exact_domain", "blocked.example"), []string{"blocked。example"}, nil, true},
		{"halfwidth ideographic full stop", block("exact_domain", "blocked.example"), []string{"blocked｡example"}, nil, true},
		{"soft hyphen", block("exact_domain", "blocked.example"), []string{"block­ed.example"}, nil, true},
		{"U-label against A-label rule", block("exact_domain", "xn--mnchen-3ya.example"), []string{"münchen.example"}, nil, true},
		{"A-label against U-label rule", block("exact_domain", "münchen.example"), []string{"xn--mnchen-3ya.example"}, nil, true},
		{"trailing dot and case", block("exact_domain", "Blocked.Example"), []string{"blocked.example."}, nil, true},
		{"suffix rule against full-width suffix", block("fuzzy_domain", ".gov"), []string{"agency.ｇｏｖ"}, nil, true},
		{"suffix rule against full-width dot", block("fuzzy_domain", ".gov"), []string{"agency．gov"}, nil, true},
		{"partial U-label rule", block("fuzzy_domain", "münch"), []string{"xn--mnchen-3ya.example"}, nil, true},
		{"unrelated host", block("exact_domain", "blocked.example"), []string{"ｏｋ.example"}, nil, false},
		{"url with full-width host", block("exact_url", "https://blocked.example/admin"), nil, []string{"https://ｂｌｏｃｋｅｄ.example/admin"}, true},
		{"url with default port and case", block("exact_url", "https://blocked.example/admin"), nil, []string{"HTTPS://Blocked.Example:443/admin#frag"}, true},
		{"url with encoded path", block("exact_url", "https://blocked.example/~a"), nil, []string{"https://blocked.example/%7ea"}, true},
		{"url with userinfo", block("exact_url", "https://blocked.example/x"), nil, []string{"https://user:pw@blocked.example/x"}, true},
		{"url with other port", block("exact_url", "https://blocked.example/x"), nil, []string{"https://blocked.example:8443/x"}, false},
		{"partial url rule against canonical host", block("fuzzy_url", "blocked.example/admin"), nil, []string{"https://ｂｌｏｃｋｅｄ.example/admin/x"}, true},
		{"partial url rule with U-label", block("fuzzy_url", "münchen.example/admin"), nil, []string{"https://xn--mnchen-3ya.example/admin"}, true},
	}
	for _, c := range cases {
		d := EvaluateAssetGate(c.rules, nil, c.domains, nil, c.urls)
		if d.Allowed == c.blocked {
			t.Errorf("%s: allowed=%v, want blocked=%v (%s)", c.name, d.Allowed, c.blocked, d.Reason)
		}
	}
}

func TestAssetGateRefusesUnconvertibleHost(t *testing.T) {
	for _, tc := range []struct {
		domains, urls []string
	}{
		{domains: []string{"exa​mple .com ü"}},
		{domains: []string{"‮evil.example"}},
		{urls: []string{"https://‮evil.example/"}},
	} {
		// No rules at all: the host still cannot be compared, so it is refused.
		if d := EvaluateAssetGate(nil, nil, tc.domains, nil, tc.urls); d.Allowed {
			t.Errorf("%+v was allowed", tc)
		}
	}
	if d := EvaluateAssetGate(nil, nil, []string{"münchen.example", "api_1.example.com"}, nil, nil); !d.Allowed {
		t.Errorf("convertible and plain ASCII hosts must pass: %s", d.Reason)
	}
}

func TestCanonicalHost(t *testing.T) {
	for in, want := range map[string]string{
		"Example.COM.":         "example.com",
		"ｅｘａｍｐｌｅ。com":          "example.com",
		"münchen.de":           "xn--mnchen-3ya.de",
		"xn--mnchen-3ya.de":    "xn--mnchen-3ya.de",
		"ex­ample.com":         "example.com",
		"under_score.example.": "under_score.example",
	} {
		if got, err := CanonicalHost(in); err != nil || got != want {
			t.Errorf("%q: %q %v, want %q", in, got, err, want)
		}
	}
}
