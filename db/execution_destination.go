package db

import (
	"net"
	"net/url"
	"strings"

	"github.com/Philocyber/counterweight/locale"
)

// Native transports can satisfy this condition by pinning a checked numeric
// target. Callers without that capability must refuse proxy-side DNS.
// ErrHTTPProxyPeerRequired requires a concrete approved target instead of
// delegating hostname resolution to a proxy while IP restrictions are enabled.
var ErrHTTPProxyPeerRequired = locale.NewError("Proxy-side DNS cannot enforce IP asset restrictions; use a direct request or a literal IP destination")

// CheckHTTPDestination evaluates the concrete request URL, without consulting
// intent IDs, coverage proposals, read grants or model-authored associations.
// Native HTTP checks direct peers separately and refuses proxy-side DNS when
// restrictive IP rules require a concrete peer. Arbitrary executors still need
// their own execution boundary.
func (s *AssetStore) CheckHTTPDestination(lang locale.Lang, taskID int64, destination *url.URL) error {
	if destination == nil || destination.Hostname() == "" || (destination.Scheme != "http" && destination.Scheme != "https") {
		return locale.NewError("HTTP execution requires a concrete HTTP or HTTPS destination")
	}
	block, err := s.db.ListAssetInterceptRules()
	if err != nil {
		return locale.Errorf("Execution destination policy could not be loaded: %w", err)
	}
	taskBlock, allow, err := s.TaskInterceptRulesSplit(taskID)
	if err != nil {
		return locale.Errorf("Execution destination policy could not be loaded: %w", err)
	}
	block = append(block, taskBlock...)
	host, err := CanonicalHost(destination.Hostname())
	if err != nil {
		return locale.Errorf("HTTP execution destination blocked by asset policy: %s",
			locale.Text(lang, "Host name cannot be converted to a canonical ASCII name: %q", destination.Hostname()))
	}
	var domains, ips []string
	if net.ParseIP(host) != nil {
		ips = []string{host}
	} else {
		domains = []string{host}
	}
	decision := EvaluateAssetGateForLanguage(lang, block, allow, domains, ips, []string{destination.String()})
	if !decision.Allowed {
		// Keep userinfo, query tokens and request bodies out of policy diagnostics.
		return locale.Errorf("HTTP execution destination blocked by asset policy: %s", strings.TrimSpace(decision.Reason))
	}
	return nil
}

// CheckHTTPPeer applies restrictive IP rules to a concrete target address.
// URL/domain authorization remains separate; a domain grant need not duplicate
// every legitimate DNS address as an IP allow rule.
func (s *AssetStore) CheckHTTPPeer(lang locale.Lang, taskID int64, ip net.IP) error {
	if ip == nil {
		return locale.NewError("HTTP execution requires a concrete HTTP or HTTPS destination")
	}
	block, err := s.db.ListAssetInterceptRules()
	if err != nil {
		return locale.Errorf("Execution destination policy could not be loaded: %w", err)
	}
	taskBlock, _, err := s.TaskInterceptRulesSplit(taskID)
	if err != nil {
		return locale.Errorf("Execution destination policy could not be loaded: %w", err)
	}
	decision := EvaluateAssetGateForLanguage(lang, append(block, taskBlock...), nil, nil, []string{ip.String()}, nil)
	if !decision.Allowed {
		return locale.Errorf("HTTP execution destination blocked by asset policy: %s", strings.TrimSpace(decision.Reason))
	}
	return nil
}

// CheckHTTPProxyDestination does not mistake the proxy's infrastructure IP for
// the origin. A remote resolver's result cannot be attested by a local lookup.
// Preserve hostname routing when no restrictive IP rule needs a concrete peer;
// otherwise refuse to silently bypass that rule through proxy-side DNS.
func (s *AssetStore) CheckHTTPProxyDestination(lang locale.Lang, taskID int64, destination *url.URL) error {
	if destination == nil || destination.Hostname() == "" || (destination.Scheme != "http" && destination.Scheme != "https") {
		return locale.NewError("HTTP execution requires a concrete HTTP or HTTPS destination")
	}
	if ip := net.ParseIP(destination.Hostname()); ip != nil {
		return s.CheckHTTPPeer(lang, taskID, ip)
	}
	block, err := s.db.ListAssetInterceptRules()
	if err != nil {
		return locale.Errorf("Execution destination policy could not be loaded: %w", err)
	}
	taskBlock, _, err := s.TaskInterceptRulesSplit(taskID)
	if err != nil {
		return locale.Errorf("Execution destination policy could not be loaded: %w", err)
	}
	for _, rule := range append(block, taskBlock...) {
		if rule.Enabled && (rule.Kind == "exact_ip" || rule.Kind == "fuzzy_ip" || rule.Kind == "cidr") {
			return ErrHTTPProxyPeerRequired
		}
	}
	return nil
}
