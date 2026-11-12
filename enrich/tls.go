package enrich

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
)

// ProxyTrust binds a local recording CA to the proxy that owns it. It never
// extends direct-target trust or trust for an unrelated egress proxy.
type ProxyTrust struct {
	URL    string
	CAFile string
}

type probeTransport struct {
	proxy func() string
	roots map[string]*x509.CertPool
	errs  map[string]error
}

func newProbeTransport(proxy func() string, trust []ProxyTrust) *probeTransport {
	t := &probeTransport{proxy: proxy, roots: make(map[string]*x509.CertPool), errs: make(map[string]error)}
	for _, item := range trust {
		u, err := url.Parse(item.URL)
		if err != nil || u.User != nil || u.Scheme != "http" ||
			!(u.Hostname() == "localhost" || net.ParseIP(u.Hostname()).IsLoopback()) {
			t.errs[item.URL] = errors.New("invalid recording proxy trust route")
			continue
		}
		roots, err := x509.SystemCertPool()
		if err != nil {
			t.errs[item.URL] = errors.New("cannot load platform TLS trust for recording proxy")
			continue
		}
		pem, err := os.ReadFile(item.CAFile)
		if err != nil || !roots.AppendCertsFromPEM(pem) {
			t.errs[item.URL] = errors.New("cannot load recording proxy TLS certificate")
			continue
		}
		t.roots[item.URL] = roots
	}
	return t
}

func (t *probeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	proxyURL := ""
	if t.proxy != nil {
		proxyURL = t.proxy()
	}
	if err := t.errs[proxyURL]; err != nil {
		return nil, err
	}
	tr := &http.Transport{
		TLSClientConfig:   &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: t.roots[proxyURL]},
		DisableKeepAlives: true, TLSHandshakeTimeout: httpTimeout,
	}
	if proxyURL != "" {
		u, err := url.Parse(proxyURL)
		if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "socks5") {
			return nil, errors.New("invalid enrichment proxy URL")
		}
		tr.Proxy = http.ProxyURL(u)
	}
	defer tr.CloseIdleConnections()
	return tr.RoundTrip(req)
}
