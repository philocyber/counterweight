package server

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/locale"
)

// A proxy alias is recording infrastructure only when its resolved numeric
// endpoint belongs to the actual listener. Pin that endpoint to avoid a second
// DNS lookup between classification and dial; retain URL credentials unchanged.
func (t *httpPeerTransport) checkedRecordingProxy(ctx context.Context, selected *url.URL) (*url.URL, bool, error) {
	recorder := t.recordingProxy
	if recorder == nil || selected.Scheme != "http" || recorder.Scheme != "http" {
		return selected, false, nil
	}
	port := selected.Port()
	if port == "" {
		port = "80"
	}
	recorderPort := recorder.Port()
	if recorderPort == "" {
		recorderPort = "80"
	}
	if port != recorderPort {
		return selected, false, nil
	}
	// Preserve exact configured endpoints in callers without a listener oracle.
	// Production supplies the actual recorder callback and rechecks on binding.
	if t.recordingPeer == nil {
		return selected, strings.EqualFold(selected.Host, recorder.Host), nil
	}
	var addresses []net.IPAddr
	if ip := net.ParseIP(selected.Hostname()); ip != nil {
		addresses = []net.IPAddr{{IP: ip}}
	} else {
		lookup := t.proxyLookup
		if lookup == nil {
			lookup = net.DefaultResolver.LookupIPAddr
		}
		var err error
		addresses, err = lookup(ctx, selected.Hostname())
		if err != nil {
			return nil, false, &httpPeerPolicyError{locale.NewError("HTTP proxy target resolution failed")}
		}
	}
	for _, address := range addresses {
		if address.Zone == "" && t.recordingPeer(address.IP, port) {
			pinned := *selected
			pinned.Host = net.JoinHostPort(address.IP.String(), port)
			return &pinned, true, nil
		}
	}
	return selected, false, nil
}

func (t *httpPeerTransport) roundTripRecordedProxy(req *http.Request, selected *url.URL) (*http.Response, error) {
	if selected.Scheme != "http" || t.bindRecording == nil {
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
	var chosen net.IP
	var policyErr error
	for _, address := range addresses {
		if address.IP == nil || address.Zone != "" || address.IP.IsUnspecified() {
			continue
		}
		if err := t.check(req.Context(), address.IP); err != nil {
			policyErr = err
			continue
		}
		chosen = append(net.IP(nil), address.IP...)
		break
	}
	if chosen == nil {
		if policyErr == nil {
			policyErr = locale.NewError("HTTP proxy target resolved to no approved addresses")
		}
		return nil, &httpPeerPolicyError{policyErr}
	}
	check := func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if t.destination != nil {
			if err := t.destination(req.WithContext(ctx)); err != nil {
				return err
			}
		}
		return t.check(ctx, chosen)
	}
	if err := check(req.Context()); err != nil {
		return nil, &httpPeerPolicyError{err}
	}
	transport := t.base.Clone()
	transport.Proxy = http.ProxyURL(selected)
	transport.DisableKeepAlives = true
	transport.DialTLS, transport.DialTLSContext = nil, nil
	dial := t.base.DialContext
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	port := selected.Port()
	if port == "" {
		port = "80"
	}
	expected := net.JoinHostPort(selected.Hostname(), port)
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if !strings.EqualFold(address, expected) {
			return nil, &httpPeerPolicyError{db.ErrHTTPProxyPeerRequired}
		}
		if err := check(req.Context()); err != nil {
			return nil, &httpPeerPolicyError{err}
		}
		conn, err := dial(ctx, network, address)
		if err != nil {
			return nil, err
		}
		bound, err := t.bindRecording(req.Context(), conn, req.URL, chosen, check)
		if err != nil {
			conn.Close()
			return nil, &httpPeerPolicyError{err}
		}
		return bound, nil
	}
	defer transport.CloseIdleConnections()
	return transport.RoundTrip(req)
}
