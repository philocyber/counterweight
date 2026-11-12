package traffic

import (
	"net"
	"os"
	"strings"

	"github.com/Philocyber/counterweight/locale"
)

// Loopback is trusted for same-host workers/browser clients. Remote clients
// require explicit operator CIDRs; HTTP headers never establish peer identity.
func proxyClientPolicy() (func(net.Addr) bool, error) {
	raw := "127.0.0.0/8,::1/128"
	if extra := strings.TrimSpace(os.Getenv("COUNTERWEIGHT_PROXY_CLIENT_CIDRS")); extra != "" {
		raw += "," + extra
	}
	var allowed []*net.IPNet
	for _, item := range strings.Split(raw, ",") {
		_, network, err := net.ParseCIDR(strings.TrimSpace(item))
		if err != nil {
			return nil, locale.NewError("COUNTERWEIGHT_PROXY_CLIENT_CIDRS must contain only comma-separated IP CIDRs")
		}
		allowed = append(allowed, network)
	}
	return func(peer net.Addr) bool {
		if peer == nil {
			return false
		}
		var ip net.IP
		if tcp, ok := peer.(*net.TCPAddr); ok {
			ip = tcp.IP // Zone is routing metadata, not part of an IPv6 identity.
		} else {
			host, _, err := net.SplitHostPort(peer.String())
			if err != nil {
				return false
			}
			ip = net.ParseIP(host)
		}
		if ip == nil {
			return false
		}
		for _, network := range allowed {
			if network.Contains(ip) {
				return true
			}
		}
		return false
	}, nil
}
