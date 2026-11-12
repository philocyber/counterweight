package server

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/url"

	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/locale"
)

func (t *httpPeerTransport) roundTripPinnedProxy(req *http.Request, selected *url.URL) (*http.Response, error) {
	if selected.Scheme != "http" && selected.Scheme != "https" && selected.Scheme != "socks5" && selected.Scheme != "socks5h" {
		return nil, &httpPeerPolicyError{db.ErrHTTPProxyPeerRequired}
	}
	lookup := t.lookup
	if lookup == nil {
		lookup = net.DefaultResolver.LookupIPAddr
	}
	addresses, err := lookup(req.Context(), req.URL.Hostname())
	if err != nil {
		return nil, &httpPeerPolicyError{locale.NewError("HTTP proxy target resolution failed")}
	}
	var chosen *net.IPAddr
	var policyErr error
	for n := range addresses {
		if addresses[n].IP == nil || addresses[n].Zone != "" {
			continue
		}
		if err := t.check(req.Context(), addresses[n].IP); err != nil {
			policyErr = err
			continue
		}
		chosen = &addresses[n]
		break
	}
	if chosen == nil {
		if policyErr == nil {
			policyErr = locale.NewError("HTTP proxy target resolved to no approved addresses")
		}
		return nil, &httpPeerPolicyError{policyErr}
	}
	check := func(ctx context.Context) error {
		if t.destination != nil {
			if err := t.destination(req.WithContext(ctx)); err != nil {
				return err
			}
		}
		return t.check(ctx, chosen.IP)
	}
	if err := check(req.Context()); err != nil {
		return nil, &httpPeerPolicyError{err}
	}
	pinned := req.Clone(req.Context())
	pinnedURL := *req.URL
	pinned.URL = &pinnedURL
	if pinned.Host == "" {
		pinned.Host = req.URL.Host
	}
	authority := chosen.IP.String()
	if port := req.URL.Port(); port != "" {
		authority = net.JoinHostPort(authority, port)
	} else if chosen.IP.To4() == nil {
		authority = "[" + authority + "]"
	}
	pinned.URL.Host = authority
	transport := t.base.Clone()
	transport.Proxy = http.ProxyURL(selected) // Do not reselect on the numeric URL.
	transport.DisableKeepAlives = true
	transport.DialTLS, transport.DialTLSContext = nil, nil
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{}
	} else {
		transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	}
	transport.TLSClientConfig.ServerName = req.URL.Hostname()
	dial := t.base.DialContext
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		// This address is proxy infrastructure. Check the selected origin again
		// immediately before connecting, rather than applying origin IP rules to it.
		if err := check(ctx); err != nil {
			return nil, &httpPeerPolicyError{err}
		}
		if selected.Scheme == "http" || selected.Scheme == "https" {
			target := authority
			if req.URL.Port() == "" {
				port := "80"
				if req.URL.Scheme == "https" {
					port = "443"
				}
				target = net.JoinHostPort(chosen.IP.String(), port)
			}
			return t.dialHTTPProxyTunnel(ctx, selected, target, dial, check)
		}
		return dial(ctx, network, address)
	}
	if selected.Scheme == "http" || selected.Scheme == "https" {
		// CONNECT keeps the checked destination independent of the Host header.
		// Forward-form proxies otherwise replace Host with the numeric URI host.
		transport.Proxy = nil
	}
	defer transport.CloseIdleConnections()
	response, err := transport.RoundTrip(pinned)
	if response != nil {
		response.Request = req
	}
	return response, err
}
