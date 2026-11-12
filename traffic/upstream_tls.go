package traffic

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"

	mproxy "github.com/lqqyt2423/go-mitmproxy/proxy"
)

// TLSOptions is startup configuration, never an agent-authored request field.
type TLSOptions struct {
	CAFile          string
	InsecureOrigins []string
}

type upstreamTLS struct {
	mproxy.BaseAddon
	roots      *x509.CertPool
	exceptions map[string]bool
}

func newUpstreamTLS(options []TLSOptions) (*upstreamTLS, error) {
	if len(options) > 1 {
		return nil, errors.New("only one recording TLS configuration is allowed")
	}
	policy := TLSOptions{}
	if len(options) == 1 {
		policy = options[0]
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		return nil, errors.New("cannot load recording upstream platform TLS trust")
	}
	if policy.CAFile != "" {
		pem, err := os.ReadFile(policy.CAFile)
		if err != nil || !roots.AppendCertsFromPEM(pem) {
			return nil, errors.New("cannot load recording upstream CA file")
		}
	}
	guard := &upstreamTLS{roots: roots, exceptions: make(map[string]bool)}
	for _, raw := range policy.InsecureOrigins {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		origin, err := tlsOrigin(strings.TrimSpace(raw))
		if err != nil {
			return nil, err
		}
		guard.exceptions[origin] = true
	}
	return guard, nil
}

func tlsOrigin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Hostname() == "" ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("recording TLS exception must be an exact HTTPS origin without credentials, path or query")
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	number, err := strconv.Atoi(port)
	if err != nil || number <= 0 || number > 65535 {
		return "", errors.New("invalid recording TLS origin port")
	}
	host := strings.ToLower(u.Hostname())
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	}
	return net.JoinHostPort(host, strconv.Itoa(number)), nil
}

func (g *upstreamTLS) verify(address string, state *tls.ConnectionState) error {
	origin, err := tlsOrigin("https://" + address)
	if err != nil || state == nil || len(state.PeerCertificates) == 0 {
		return errors.New("invalid recording upstream TLS handshake")
	}
	if g.exceptions[origin] {
		return nil
	}
	host, _, err := net.SplitHostPort(origin)
	if err != nil {
		return err
	}
	intermediates := x509.NewCertPool()
	for _, certificate := range state.PeerCertificates[1:] {
		intermediates.AddCert(certificate)
	}
	_, err = state.PeerCertificates[0].Verify(x509.VerifyOptions{
		Roots: g.roots, Intermediates: intermediates, DNSName: host,
	})
	return err
}

func (g *upstreamTLS) config(address string) *tls.Config {
	origin, err := tlsOrigin("https://" + address)
	if err != nil {
		return &tls.Config{RootCAs: x509.NewCertPool()} // invalid origin fails closed
	}
	return &tls.Config{RootCAs: g.roots, InsecureSkipVerify: g.exceptions[origin]}
}

func (g *upstreamTLS) TlsEstablishedServer(conn *mproxy.ConnContext) {
	if conn == nil || conn.ServerConn == nil {
		return
	}
	if g.verify(conn.ServerConn.Address, conn.ServerConn.TlsState()) != nil {
		// The SDK invokes this before creating the HTTP client or sending HTTP
		// bytes. Close both ends on failure; never classify it as passthrough.
		if conn.ServerConn.Conn != nil {
			_ = conn.ServerConn.Conn.Close()
		}
		if conn.ClientConn != nil && conn.ClientConn.Conn != nil {
			_ = conn.ClientConn.Conn.Close()
		}
	}
}

// ValidateTLSOptions permits configuration errors to fail before stores open.
func ValidateTLSOptions(options ...TLSOptions) error {
	_, err := newUpstreamTLS(options)
	return err
}
