package db

import "testing"

func TestExactIPUsesNetworkIdentity(t *testing.T) {
	cases := []struct {
		name, pattern, candidate string
		match                    bool
	}{
		{"expanded candidate", "2001:db8::1", "2001:0db8:0000:0000:0000:0000:0000:0001", true},
		{"expanded pattern", "2001:0DB8:0:0:0:0:0:1", "2001:db8::1", true},
		{"case and whitespace", " 2001:DB8::ABCD ", " 2001:db8::abcd ", true},
		{"IPv4 mapped candidate", "192.0.2.7", "::ffff:c000:207", true},
		{"IPv4 mapped pattern", "::ffff:192.0.2.7", "192.0.2.7", true},
		{"different IPv6 host", "2001:db8::1", "2001:db8::2", false},
		{"different IPv4 host", "192.0.2.7", "192.0.2.8", false},
		{"invalid identical strings", "not-an-ip", "not-an-ip", false},
		{"invalid candidate", "2001:db8::1", "2001:db8::1/path", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rules := []AssetInterceptRule{rule("exact_ip", c.pattern, true)}
			_, value, matched := MatchAssetInterceptRules(rules, nil, []string{c.candidate}, nil)
			if matched != c.match || matched && value != c.candidate {
				t.Fatalf("match=%v value=%q, want match=%v preserving %q", matched, value, c.match, c.candidate)
			}
			if got := EvaluateAssetGate(rules, rules, nil, []string{c.candidate}, nil); got.Allowed {
				t.Fatal("matching blocks or allowlist misses must deny")
			}
			if got := EvaluateAssetGate(nil, rules, nil, []string{c.candidate}, nil); got.Allowed != c.match {
				t.Fatalf("allowlist result=%v, want %v", got.Allowed, c.match)
			}
		})
	}

	// URL-only persisted services use the same exact-IP gate as raw insertions.
	a := &Asset{Type: "service", URL: "https://[2001:0DB8:0:0:0:0:0:1]:8443/path"}
	domains, ips, urls := a.interceptCandidates()
	if got := EvaluateAssetGate([]AssetInterceptRule{rule("exact_ip", "2001:db8::1", true)}, nil, domains, ips, urls); got.Allowed {
		t.Fatal("URL host spelling bypassed the exact-IP block")
	}
}
