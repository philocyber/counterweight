package server

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/Philocyber/counterweight/locale"
)

type bufferedProxyTunnel struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedProxyTunnel) Read(p []byte) (int, error) { return c.reader.Read(p) }

func (t *httpPeerTransport) dialHTTPProxyTunnel(ctx context.Context, selected *url.URL, target string, dial func(context.Context, string, string) (net.Conn, error), check func(context.Context) error) (net.Conn, error) {
	port := selected.Port()
	if port == "" {
		port = "80"
		if selected.Scheme == "https" {
			port = "443"
		}
	}
	conn, err := dial(ctx, "tcp", net.JoinHostPort(selected.Hostname(), port))
	if err != nil {
		return nil, err
	}
	owned := true
	defer func() {
		if owned {
			conn.Close()
		}
	}()
	rawConn := conn
	stop := context.AfterFunc(ctx, func() { rawConn.Close() })
	defer stop()
	deadline := time.Now().Add(30 * time.Second)
	if requested, ok := ctx.Deadline(); ok && requested.Before(deadline) {
		deadline = requested
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, err
	}
	if selected.Scheme == "https" {
		config := &tls.Config{}
		if t.base.TLSClientConfig != nil {
			config = t.base.TLSClientConfig.Clone()
		}
		config.ServerName = selected.Hostname()
		config.NextProtos = []string{"http/1.1"}
		secure := tls.Client(conn, config)
		if err := secure.HandshakeContext(ctx); err != nil {
			return nil, err
		}
		conn = secure
	}
	headers := t.base.ProxyConnectHeader.Clone()
	if t.base.GetProxyConnectHeader != nil {
		headers, err = t.base.GetProxyConnectHeader(ctx, selected, target)
		if err != nil {
			return nil, err
		}
		headers = headers.Clone()
	}
	if headers == nil {
		headers = make(http.Header)
	}
	if selected.User != nil {
		password, _ := selected.User.Password()
		headers.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(selected.User.Username()+":"+password)))
	}
	request := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: target}, Host: target, Header: headers}
	// Proxy resolution and its TLS handshake can outlive a policy change.
	if err := check(ctx); err != nil {
		return nil, &httpPeerPolicyError{err}
	}
	if err := request.Write(conn); err != nil {
		return nil, err
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, request)
	if err != nil {
		return nil, locale.NewError("HTTP proxy tunnel could not be established")
	}
	if t.base.OnProxyConnectResponse != nil {
		if err := t.base.OnProxyConnectResponse(ctx, selected, request, response); err != nil {
			return nil, err
		}
	}
	if response.StatusCode != http.StatusOK {
		return nil, locale.NewError("HTTP proxy tunnel could not be established")
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return nil, err
	}
	owned = false
	return &bufferedProxyTunnel{Conn: conn, reader: reader}, nil
}
