package proxy

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/url"
)

func targetAddress(u *url.URL) string {
	port := u.Port()
	if port == "" {
		port = "443"
	}
	return net.JoinHostPort(u.Hostname(), port)
}

func (p *Proxy) upstreamTLS(address string) *tls.Config {
	config := &tls.Config{InsecureSkipVerify: p.Opts.SslInsecure}
	if p.Opts.UpstreamTLSConfig != nil {
		if selected := p.Opts.UpstreamTLSConfig(address); selected != nil {
			config = selected.Clone()
		} else {
			config = &tls.Config{} // missing policy never enables the legacy bypass
		}
	}
	if config.ServerName == "" {
		host, _, err := net.SplitHostPort(address)
		if err == nil {
			config.ServerName = host
		}
	}
	return config
}

// Alternate requests can change origin; do not share an exception-bearing TLS
// config across those requests or inherit it for an HTTPS egress proxy.
type targetTLSTransport struct {
	proxy *Proxy
	base  *http.Transport
}

func (t *targetTLSTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	transport := t.base.Clone()
	transport.DisableKeepAlives = true
	transport.TLSClientConfig = &tls.Config{}
	if req.URL.Scheme == "https" {
		transport.TLSClientConfig = t.proxy.upstreamTLS(targetAddress(req.URL))
	}
	transport.TLSClientConfig.ServerName = "" // net/http selects target/proxy name independently
	var proxyURL *url.URL
	if transport.Proxy != nil {
		selected, err := transport.Proxy(req)
		if err != nil {
			return nil, err
		}
		proxyURL = selected
		transport.Proxy = http.ProxyURL(proxyURL)
	}
	transport.DialTLSContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		config := t.proxy.upstreamTLS(address)
		if proxyURL != nil && proxyURL.Scheme == "https" && address == targetAddress(proxyURL) {
			// The outer proxy handshake is independent of the target exception.
			config = &tls.Config{RootCAs: transport.TLSClientConfig.RootCAs, ServerName: proxyURL.Hostname()}
		}
		dial := transport.DialContext
		if dial == nil {
			dial = (&net.Dialer{}).DialContext
		}
		conn, err := dial(ctx, network, address)
		if err != nil {
			return nil, err
		}
		secure := tls.Client(conn, config)
		if err := secure.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return nil, err
		}
		return secure, nil
	}
	defer transport.CloseIdleConnections()
	return transport.RoundTrip(req)
}
