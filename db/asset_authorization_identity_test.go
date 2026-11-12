package db

import (
	"testing"

	"github.com/Philocyber/counterweight/locale"
)

func TestAssetAuthorizationUsesConcreteIdentity(t *testing.T) {
	allow := []AssetInterceptRule{rule("exact_domain", "allowed.example", true)}
	cases := []struct {
		name    string
		asset   Asset
		allowed bool
	}{
		{"root domain with unrelated URL", Asset{Type: "root_domain", Domain: "outside.example", URL: "https://allowed.example"}, false},
		{"subdomain with forged root", Asset{Type: "subdomain", Domain: "outside.example", RootDomain: "allowed.example"}, false},
		{"HTTP service with unrelated domain", Asset{Type: "service", URL: "https://outside.example", Domain: "allowed.example"}, false},
		{"endpoint with unrelated domain", Asset{Type: "endpoint", URL: "https://outside.example/path", Domain: "allowed.example"}, false},
		{"app with unrelated network witness", Asset{Type: "app", Domain: "allowed.example"}, false},
		{"IP with forged bound domain", Asset{Type: "ip", IP: "192.0.2.19", BoundDomains: []string{"allowed.example"}}, false},
		{"non-HTTP service with unrelated IP", Asset{Type: "service", Domain: "allowed.example", IP: "192.0.2.19"}, false},
		{"authorized root domain", Asset{Type: "root_domain", Domain: "allowed.example"}, true},
		{"authorized subdomain", Asset{Type: "subdomain", Domain: "allowed.example"}, true},
		{"authorized HTTP service retains enrichment", Asset{Type: "service", URL: "https://allowed.example:8443/path", IP: "192.0.2.19"}, true},
		{"authorized endpoint retains enrichment", Asset{Type: "endpoint", URL: "https://allowed.example/path", IP: "192.0.2.19"}, true},
		{"authorized domain-only service", Asset{Type: "service", Domain: "allowed.example"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if d := c.asset.EvaluateInterceptGateForLanguage(locale.En, nil, allow); d.Allowed != c.allowed {
				t.Fatalf("allowed=%v, want %v (%s)", d.Allowed, c.allowed, d.Reason)
			}
			if d := c.asset.EvaluateInterceptGateForLanguage(locale.En, nil, nil); !d.Allowed {
				t.Fatal("an unconfigured allowlist must remain disabled")
			}
		})
	}
	allow = append(allow, rule("exact_ip", "192.0.2.19", true))
	a := Asset{Type: "service", Domain: "allowed.example", IP: "192.0.2.19"}
	if d := a.EvaluateInterceptGateForLanguage(locale.En, nil, allow); !d.Allowed {
		t.Fatal("explicit authorization of both non-HTTP identities must work")
	}
	a.Domain = "outside.example"
	if d := a.EvaluateInterceptGateForLanguage(locale.En, nil, allow); d.Allowed {
		t.Fatal("an authorized IP must not authorize an unrelated explicit hostname")
	}
	a = Asset{Type: "service", URL: "https://allowed.example", IP: "192.0.2.19"}
	if d := a.EvaluateInterceptGateForLanguage(locale.En, []AssetInterceptRule{rule("exact_ip", a.IP, true)}, allow); d.Allowed {
		t.Fatal("enrichment metadata must still participate in block matching")
	}
}
