package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/Philocyber/counterweight/db"
)

type httpPeerPolicyError struct{ cause error }

func (e *httpPeerPolicyError) Error() string { return e.cause.Error() }
func (e *httpPeerPolicyError) Unwrap() error { return e.cause }

type httpPeerTransport struct {
	base             *http.Transport
	check            func(context.Context, net.IP) error
	lookup           func(context.Context, string) ([]net.IPAddr, error)
	proxyLookup      func(context.Context, string) ([]net.IPAddr, error)
	destination      func(*http.Request) error
	proxyDestination func(*http.Request) error
	recordingProxy   *url.URL
	recordingPeer    func(net.IP, string) bool
	bindRecording    func(context.Context, net.Conn, *url.URL, net.IP, func(context.Context) error) (net.Conn, error)
}

func (t *httpPeerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.base.Proxy != nil {
		proxy, err := t.base.Proxy(req)
		if err != nil {
			return nil, err
		}
		if proxy != nil {
			// Proxy infrastructure is not the origin. Remote DNS remains available
			// without IP restrictions; otherwise a checked numeric tunnel is needed.
			if t.destination != nil {
				if err := t.destination(req); err != nil {
					return nil, &httpPeerPolicyError{err}
				}
			}
			if t.proxyDestination != nil {
				if err := t.proxyDestination(req); err != nil {
					if errors.Is(err, db.ErrHTTPProxyPeerRequired) {
						selected, recorded, aliasErr := t.checkedRecordingProxy(req.Context(), proxy)
						if aliasErr != nil {
							return nil, aliasErr
						}
						if recorded {
							if t.bindRecording != nil {
								return t.roundTripRecordedProxy(req, selected)
							}
							return nil, &httpPeerPolicyError{err}
						}
						return t.roundTripPinnedProxy(req, proxy)
					}
					return nil, &httpPeerPolicyError{err}
				}
			}
			return t.base.RoundTrip(req)
		}
	}
	direct := t.base.Clone()
	direct.Proxy = nil
	// A redirect must check policy on its actual new connection, including a
	// newly blocked IP. Retain TLS settings/SNI, but prevent custom TLS dialing
	// from bypassing the checked TCP path or reusing an earlier peer decision.
	direct.DisableKeepAlives = true
	direct.DialTLS, direct.DialTLSContext = nil, nil
	dial := t.base.DialContext
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	direct.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		checked := *t
		checked.check = func(ctx context.Context, ip net.IP) error {
			// DNS may take time. Re-read the URL grant after resolution, just
			// before checking/dialing each candidate, including redirect hops.
			if t.destination != nil {
				if err := t.destination(req.WithContext(ctx)); err != nil {
					return err
				}
			}
			return t.check(ctx, ip)
		}
		return checked.dial(ctx, network, address, dial)
	}
	defer direct.CloseIdleConnections()
	return direct.RoundTrip(req)
}

func (t *httpPeerTransport) dial(ctx context.Context, network, address string, dial func(context.Context, string, string) (net.Conn, error)) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	lookup := t.lookup
	if lookup == nil {
		lookup = net.DefaultResolver.LookupIPAddr
	}
	addresses, err := lookup(ctx, host)
	if err != nil {
		return nil, err
	}
	if network == "tcp4" || network == "tcp6" {
		var selected []net.IPAddr
		for _, ip := range addresses {
			if (ip.IP.To4() != nil) == (network == "tcp4") {
				selected = append(selected, ip)
			}
		}
		addresses = selected
	}
	if network != "tcp" || len(addresses) < 2 {
		return t.dialSerial(ctx, network, port, addresses, dial)
	}
	var primary, fallback []net.IPAddr
	firstIPv4 := addresses[0].IP.To4() != nil
	for _, ip := range addresses {
		if (ip.IP.To4() != nil) == firstIPv4 {
			primary = append(primary, ip)
		} else {
			fallback = append(fallback, ip)
		}
	}
	if len(fallback) == 0 {
		return t.dialSerial(ctx, network, port, primary, dial)
	}
	// Preserve dual-stack fallback while every candidate uses an approved
	// numeric address. At most two serial family dialers run concurrently.
	raceCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		conn net.Conn
		err  error
	}
	results := make(chan result)
	start := func(ips []net.IPAddr) {
		go func() {
			conn, err := t.dialSerial(raceCtx, network, port, ips, dial)
			select {
			case results <- result{conn, err}:
			case <-raceCtx.Done():
				if conn != nil {
					conn.Close()
				}
			}
		}()
	}
	start(primary)
	timer := time.NewTimer(300 * time.Millisecond)
	defer timer.Stop()
	fallbackStarted, failures := false, 0
	var first error
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			if !fallbackStarted {
				start(fallback)
				fallbackStarted = true
			}
		case result := <-results:
			if result.err == nil {
				return result.conn, nil
			}
			first = preferHTTPPeerError(first, result.err)
			failures++
			if failures == 2 {
				return nil, first
			}
			if !fallbackStarted {
				start(fallback)
				fallbackStarted = true
			}
		}
	}
}

func (t *httpPeerTransport) dialSerial(ctx context.Context, network, port string, addresses []net.IPAddr, dial func(context.Context, string, string) (net.Conn, error)) (net.Conn, error) {
	var first error
	for _, ip := range addresses {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := t.check(ctx, ip.IP); err != nil {
			first = preferHTTPPeerError(first, &httpPeerPolicyError{err})
			continue
		}
		peer := ip.IP.String()
		if ip.Zone != "" {
			peer += "%" + ip.Zone
		}
		// Dial this approved numeric address. The original hostname is never
		// resolved again between policy and connection establishment.
		conn, err := dial(ctx, network, net.JoinHostPort(peer, port))
		if err == nil {
			return conn, nil
		}
		if first == nil {
			first = err
		}
	}
	if first == nil {
		first = fmt.Errorf("HTTP destination resolved to no addresses")
	}
	return nil, first
}

func preferHTTPPeerError(first, next error) error {
	var policy *httpPeerPolicyError
	if first == nil || errors.As(next, &policy) {
		return next
	}
	return first
}
