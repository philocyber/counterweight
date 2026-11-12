package proxy

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/lqqyt2423/go-mitmproxy/internal/helper"
)

var errUpstreamBinding = errors.New("checked upstream connection binding is unavailable")

func (p *Proxy) checkedUpstreamAddress(req *http.Request) (string, bool, error) {
	logical := helper.CanonicalAddr(req.URL)
	conn, ok := req.Context().Value(connContextKey).(*ConnContext)
	if p.Opts.UpstreamDialTarget == nil {
		if ok && conn != nil {
			conn.boundMu.Lock()
			bound := conn.upstreamBound
			conn.boundMu.Unlock()
			if bound {
				return "", true, errUpstreamBinding
			}
		}
		return logical, false, nil
	}
	if !ok || conn == nil {
		return "", false, errUpstreamBinding
	}
	// Keep the irreversible bound-state transition serialized with lookup.
	// A released binding must never silently become ordinary hostname dialing.
	conn.boundMu.Lock()
	defer conn.boundMu.Unlock()
	address, bound, err := p.Opts.UpstreamDialTarget(req, conn, logical)
	if err != nil {
		return "", bound, err
	}
	if !bound {
		if conn.upstreamBound {
			return "", true, errUpstreamBinding
		}
		return logical, false, nil
	}
	conn.upstreamBound = true
	host, port, err := net.SplitHostPort(address)
	ip := net.ParseIP(host)
	number, portErr := strconv.Atoi(port)
	if err != nil || ip == nil || ip.IsUnspecified() || portErr != nil || number < 1 || number > 65535 {
		return "", true, errUpstreamBinding
	}
	address = net.JoinHostPort(ip.String(), strconv.Itoa(number))
	scheme := req.URL.Scheme
	if req.Method == "CONNECT" {
		scheme = "https"
	}
	if conn.upstreamAddress != "" && (conn.upstreamAddress != address || !strings.EqualFold(conn.upstreamAuthority, logical) || !strings.EqualFold(conn.upstreamScheme, scheme)) {
		return "", true, errUpstreamBinding
	}
	conn.upstreamAddress, conn.upstreamAuthority, conn.upstreamScheme = address, logical, scheme
	return address, true, nil
}

func (p *Proxy) checkBoundRequest(req *http.Request, separate bool) error {
	_, bound, err := p.checkedUpstreamAddress(req)
	if err != nil {
		return err
	}
	if bound && (separate || strings.EqualFold(req.Header.Get("Upgrade"), "websocket")) {
		// Neither the alternate transport nor websocket dialer attests this
		// connection's peer binding. Refuse instead of resolving the name again.
		return errUpstreamBinding
	}
	if bound && req.Host != "" {
		hostAddress := helper.CanonicalAddr(&url.URL{Scheme: req.URL.Scheme, Host: req.Host})
		if !strings.EqualFold(hostAddress, helper.CanonicalAddr(req.URL)) {
			return errUpstreamBinding
		}
	}
	return nil
}
