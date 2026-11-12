package traffic

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/Philocyber/counterweight/locale"

	mproxy "github.com/lqqyt2423/go-mitmproxy/proxy"
)

var errNativeHTTPBinding = locale.NewError("Recording proxy destination binding is unavailable")

type nativeHTTPBinding struct {
	ctx                                  context.Context
	authority, scheme, address, clientID string
	check                                func(context.Context) error
	revoked                              atomic.Bool
}

type nativeHTTPConn struct {
	net.Conn
	binding *nativeHTTPBinding
}

func (c *nativeHTTPConn) Close() error {
	// Keep a revoked entry until the proxy observes disconnection. Removing it
	// earlier could turn already-buffered requests into unbound DNS dialing.
	c.binding.revoked.Store(true)
	return c.Conn.Close()
}

// BindNativeHTTPConnection attaches a checked origin to an actual connection
// to this recorder before its caller writes HTTP bytes. The caller must close
// the returned connection and supply a live policy check for the original task.
// No binding identifier is carried in request headers or captured evidence.
func (t *Traffic) BindNativeHTTPConnection(ctx context.Context, conn net.Conn, origin *url.URL, peer net.IP, check func(context.Context) error) (net.Conn, error) {
	if conn == nil || origin == nil || peer == nil || peer.IsUnspecified() || check == nil || ctx == nil {
		return nil, errNativeHTTPBinding
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if origin.Scheme != "http" && origin.Scheme != "https" {
		return nil, errNativeHTTPBinding
	}
	remote, ok := conn.RemoteAddr().(*net.TCPAddr)
	local, localOK := conn.LocalAddr().(*net.TCPAddr)
	if !ok || !localOK || !t.OwnsRecordingPeer(remote.IP, strconv.Itoa(remote.Port)) {
		return nil, errNativeHTTPBinding
	}
	if t.stopping() || t.proxy == nil || t.proxy.Opts.ClientAllowed != nil && !t.proxy.Opts.ClientAllowed(local) {
		return nil, errNativeHTTPBinding
	}
	if origin.Hostname() == "" {
		return nil, errNativeHTTPBinding
	}
	port := origin.Port()
	if port == "" {
		port = "80"
		if origin.Scheme == "https" {
			port = "443"
		}
	}
	binding := &nativeHTTPBinding{ctx: ctx, scheme: origin.Scheme, authority: net.JoinHostPort(origin.Hostname(), port), address: net.JoinHostPort(peer.String(), port), check: check}
	if err := check(ctx); err != nil {
		return nil, err
	}
	t.nativeMu.Lock()
	defer t.nativeMu.Unlock()
	if t.nativeBindings == nil {
		t.nativeBindings = make(map[string]*nativeHTTPBinding)
	}
	key := local.String()
	// Bound registry growth, including connections whose disconnect callback
	// has not yet arrived. Never replace a binding for a still-owned socket.
	if t.nativeBindings[key] != nil || len(t.nativeBindings) >= 4096 {
		return nil, errNativeHTTPBinding
	}
	t.nativeBindings[key] = binding
	return &nativeHTTPConn{Conn: conn, binding: binding}, nil
}

func (t *Traffic) nativeListenerAddr(address net.Addr) {
	t.nativeMu.Lock()
	defer t.nativeMu.Unlock()
	t.nativeListener = nil
	if tcp, ok := address.(*net.TCPAddr); ok {
		copy := *tcp
		copy.IP = append(net.IP(nil), tcp.IP...)
		t.nativeListener = &copy
	}
}

// OwnsRecordingPeer compares infrastructure with this recorder's actual bound
// socket. DNS aliases alone cannot establish recorder identity. Wildcard binds
// accept loopback candidates only; client admission is checked again on binding.
func (t *Traffic) OwnsRecordingPeer(peer net.IP, port string) bool {
	if peer == nil || peer.IsUnspecified() || t.stopping() {
		return false
	}
	t.nativeMu.Lock()
	defer t.nativeMu.Unlock()
	listener := t.nativeListener
	if listener == nil || listener.Zone != "" || port != strconv.Itoa(listener.Port) {
		return false
	}
	if listener.IP.IsUnspecified() {
		return peer.IsLoopback()
	}
	return listener.IP.Equal(peer)
}

func (t *Traffic) hasNativeBinding(remote string) bool {
	t.nativeMu.Lock()
	defer t.nativeMu.Unlock()
	return t.nativeBindings[remote] != nil
}

func (t *Traffic) nativeUpstreamTarget(req *http.Request, conn *mproxy.ConnContext, logical string) (string, bool, error) {
	if conn == nil || conn.ClientConn == nil || conn.ClientConn.Conn == nil {
		return "", false, errNativeHTTPBinding
	}
	client := conn.ClientConn
	t.nativeMu.Lock()
	binding := t.nativeBindings[client.Conn.RemoteAddr().String()]
	if binding == nil {
		t.nativeMu.Unlock()
		return "", false, nil
	}
	id := client.Id.String()
	if binding.clientID != "" && binding.clientID != id {
		t.nativeMu.Unlock()
		return "", true, errNativeHTTPBinding
	}
	binding.clientID = id
	t.nativeMu.Unlock()
	scheme := req.URL.Scheme
	if req.Method == http.MethodConnect {
		scheme = "https"
	}
	if binding.revoked.Load() || !strings.EqualFold(logical, binding.authority) || !strings.EqualFold(scheme, binding.scheme) {
		return "", true, errNativeHTTPBinding
	}
	if err := binding.ctx.Err(); err != nil {
		return "", true, err
	}
	if err := binding.check(binding.ctx); err != nil {
		return "", true, err
	}
	return binding.address, true, nil
}

type nativeBindingAddon struct {
	mproxy.BaseAddon
	t *Traffic
}

func (a *nativeBindingAddon) ClientDisconnected(client *mproxy.ClientConn) {
	a.t.nativeMu.Lock()
	defer a.t.nativeMu.Unlock()
	key := client.Conn.RemoteAddr().String()
	binding := a.t.nativeBindings[key]
	if binding != nil && (binding.clientID == "" || binding.clientID == client.Id.String()) {
		binding.revoked.Store(true)
		delete(a.t.nativeBindings, key)
	}
}
