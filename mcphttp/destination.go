package mcphttp

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type endpointOrigin struct{ scheme, host, port string }

func destinationOrigin(raw string) (endpointOrigin, error) {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Opaque != "" || u.Hostname() == "" {
		return endpointOrigin{}, errors.New("mcp: endpoint must be an HTTP(S) URL without embedded credentials")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return endpointOrigin{}, errors.New("mcp: endpoint must use HTTP or HTTPS")
	}
	host := strings.ToLower(u.Hostname())
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	}
	port := u.Port()
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	} else {
		n, err := strconv.Atoi(port)
		if err != nil || n < 0 || n > 65535 {
			return endpointOrigin{}, errors.New("mcp: invalid endpoint port")
		}
		port = strconv.Itoa(n)
	}
	return endpointOrigin{scheme: scheme, host: host, port: port}, nil
}

func destinationRedirect(origin endpointOrigin) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("mcp: stopped after 10 redirects")
		}
		next, err := destinationOrigin(req.URL.String())
		if err != nil || next != origin {
			return errors.New("mcp: redirect outside configured origin blocked")
		}
		return nil
	}
}
