package db

import "testing"

func rule(kind, pattern string, enabled bool) AssetInterceptRule {
	return AssetInterceptRule{Kind: kind, Pattern: pattern, Enabled: enabled}
}

func TestMatchAssetInterceptRules(t *testing.T) {
	cases := []struct {
		name    string
		rules   []AssetInterceptRule
		domains []string
		ips     []string
		urls    []string
		want    bool
		wantVal string
	}{
		{"Built-in partial government-domain match", []AssetInterceptRule{rule("fuzzy_domain", ".gov", true)},
			[]string{"www.agency.gov"}, nil, nil, true, "www.agency.gov"},
		{"Partial education-domain match", []AssetInterceptRule{rule("fuzzy_domain", ".edu", true)},
			[]string{"mit.edu"}, nil, nil, true, "mit.edu"},
		{"Exact domain matching is case insensitive", []AssetInterceptRule{rule("exact_domain", "Example.com", true)},
			[]string{"example.com"}, nil, nil, true, "example.com"},
		{"Exact domain does not match subdomains", []AssetInterceptRule{rule("exact_domain", "example.com", true)},
			[]string{"a.example.com"}, nil, nil, false, ""},
		{"Exact domain matches the same name written with its root dot", []AssetInterceptRule{rule("exact_domain", "example.com", true)},
			[]string{"Example.com."}, nil, nil, true, "Example.com."},
		{"Exact domain rule written with a root dot matches the plain name", []AssetInterceptRule{rule("exact_domain", "example.com.", true)},
			[]string{"example.com"}, nil, nil, true, "example.com"},
		{"A root dot does not make a subdomain match", []AssetInterceptRule{rule("exact_domain", "example.com", true)},
			[]string{"a.example.com."}, nil, nil, false, ""},
		{"Exact IP match", []AssetInterceptRule{rule("exact_ip", "203.0.113.5", true)},
			nil, []string{"203.0.113.5"}, nil, true, "203.0.113.5"},
		{"Partial IP prefix match", []AssetInterceptRule{rule("fuzzy_ip", "203.0.113.", true)},
			nil, []string{"203.0.113.99"}, nil, true, "203.0.113.99"},
		{"CIDR match", []AssetInterceptRule{rule("cidr", "192.168.0.0/16", true)},
			nil, []string{"192.168.5.20"}, nil, true, "192.168.5.20"},
		{"CIDR miss", []AssetInterceptRule{rule("cidr", "192.168.0.0/16", true)},
			nil, []string{"10.0.0.1"}, nil, false, ""},
		{"Exact URL match", []AssetInterceptRule{rule("exact_url", "https://a.gov/login", true)},
			nil, nil, []string{"https://a.gov/login"}, true, "https://a.gov/login"},
		{"Partial URL path match", []AssetInterceptRule{rule("fuzzy_url", "/admin", true)},
			nil, nil, []string{"https://x.com/admin/panel"}, true, "https://x.com/admin/panel"},
		{"Disabled rule does not match", []AssetInterceptRule{rule("fuzzy_domain", ".gov", false)},
			[]string{"www.gov"}, nil, nil, false, ""},
		{"No rules means no match", nil, []string{"www.gov"}, nil, nil, false, ""},
		{"Empty pattern does not match", []AssetInterceptRule{rule("fuzzy_domain", "  ", true)},
			[]string{"www.gov"}, nil, nil, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, val, ok := MatchAssetInterceptRules(c.rules, c.domains, c.ips, c.urls)
			if ok != c.want {
				t.Fatalf("match = %v, want %v (rule=%+v)", ok, c.want, r)
			}
			if ok && val != c.wantVal {
				t.Fatalf("matched value = %q, want %q", val, c.wantVal)
			}
		})
	}
}

func TestEvaluateAssetGate(t *testing.T) {
	block := []AssetInterceptRule{rule("fuzzy_domain", ".gov", true)}
	allow := []AssetInterceptRule{rule("fuzzy_domain", "example.com", true)}

	// 1. A block match rejects the asset; the block reason takes precedence.
	if d := EvaluateAssetGate(block, allow, []string{"www.gov"}, nil, nil); d.Allowed {
		t.Fatal("A block-rule match must be denied")
	}

	// 2. No block match, but enabled allow rules with no match: reject.
	d := EvaluateAssetGate(block, allow, []string{"foo.other.com"}, nil, nil)
	if d.Allowed {
		t.Fatal("An allowlist miss must be denied")
	}
	if d.Reason == "" {
		t.Fatal("Denial must include a reason")
	}

	// 3. No block match and an allow match: allow.
	if d := EvaluateAssetGate(block, allow, []string{"api.example.com"}, nil, nil); !d.Allowed {
		t.Fatal("An allowlist match must be allowed")
	}

	// 4. No allow rules (allowlist disabled): allow when no block rule matches.
	if d := EvaluateAssetGate(block, nil, []string{"foo.other.com"}, nil, nil); !d.Allowed {
		t.Fatal("Without an allowlist, an unblocked asset must be allowed")
	}

	// 5. All allow rules disabled: treat the allowlist as disabled and allow.
	disabledAllow := []AssetInterceptRule{rule("fuzzy_domain", "example.com", false)}
	if d := EvaluateAssetGate(nil, disabledAllow, []string{"foo.other.com"}, nil, nil); !d.Allowed {
		t.Fatal("An entirely disabled allowlist must allow the asset")
	}

	// 6. Blocking takes precedence: matching both block and allow rules rejects the target.
	if d := EvaluateAssetGate(
		[]AssetInterceptRule{rule("fuzzy_domain", ".gov", true)},
		[]AssetInterceptRule{rule("fuzzy_domain", ".gov", true)},
		[]string{"www.gov"}, nil, nil,
	); d.Allowed {
		t.Fatal("Blocking must take precedence over allowing")
	}
}

func TestAssetInterceptCandidates(t *testing.T) {
	// A URL-only service asset's host becomes a domain candidate and can match fuzzy_domain.
	a := &Asset{Type: "service", URL: "https://portal.agency.gov:8443/app"}
	domains, _, urls := a.interceptCandidates()
	if len(urls) != 1 || urls[0] != a.URL {
		t.Fatalf("urls = %v", urls)
	}
	found := false
	for _, d := range domains {
		if d == "portal.agency.gov" {
			found = true
		}
	}
	if !found {
		t.Fatalf("URL host missing from domain candidates: %v", domains)
	}
	r, _, ok := MatchAssetInterceptRules([]AssetInterceptRule{rule("fuzzy_domain", ".gov", true)}, domains, nil, urls)
	if !ok {
		t.Fatalf("URL-only government service asset must match fuzzy_domain, rule=%+v", r)
	}

	// An IP URL host becomes an IP candidate and can match CIDR rules.
	b := &Asset{Type: "service", URL: "http://10.1.2.3/x"}
	_, ips, _ := b.interceptCandidates()
	if r, _, ok := MatchAssetInterceptRules([]AssetInterceptRule{rule("cidr", "10.0.0.0/8", true)}, nil, ips, nil); !ok {
		t.Fatalf("IP in URL must match CIDR, ips=%v rule=%+v", ips, r)
	}
}

// The built-in endings added after the first seed: they must catch government
// and military hosts, and ".gob." / ".gouv." must leave ordinary names alone.
func TestBuiltinGovernmentAndMilitaryEndings(t *testing.T) {
	rules := []AssetInterceptRule{
		{Enabled: true, Kind: "fuzzy_domain", Pattern: ".mil"},
		{Enabled: true, Kind: "fuzzy_domain", Pattern: ".gob."},
		{Enabled: true, Kind: "fuzzy_domain", Pattern: ".gouv."},
	}
	for _, host := range []string{"www.army.mil", "portal.ejercito.mil.ar", "www.argentina.gob.ar", "sede.agenciatributaria.gob.es", "www.service-public.gouv.fr"} {
		if _, _, ok := MatchAssetInterceptRules(rules, []string{host}, nil, nil); !ok {
			t.Errorf("%s is not blocked", host)
		}
	}
	for _, host := range []string{"goblin.example.com", "www.gobierno-abierto.example", "shop.gouvernail.example", "staging.example.com"} {
		if rule, _, ok := MatchAssetInterceptRules(rules, []string{host}, nil, nil); ok {
			t.Errorf("%s is blocked by %s", host, rule.Pattern)
		}
	}
}
