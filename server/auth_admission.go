package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Philocyber/counterweight/locale"
)

const maxAuthPeers = 1024

type authBucket struct {
	tokens float64
	last   time.Time
}

func (b *authBucket) refill(now time.Time, burst float64, interval time.Duration) {
	if b.last.IsZero() {
		b.tokens = burst
	} else {
		b.tokens = min(burst, b.tokens+max(0, now.Sub(b.last).Seconds())/interval.Seconds())
	}
	b.last = now
}

// authAdmission bounds password guesses, peer bookkeeping, and concurrent
// bcrypt work. The global bucket also bounds attackers rotating source
// addresses. When that bucket is empty, a small reserve still admits a peer that
// has made no recent attempt, so traffic from a few other addresses delays a
// correct password for a bounded time instead of blocking it. A flood from
// ever-new networks can still keep that reserve empty, so two kinds of client
// have a reserve of their own that such a flood cannot reach: sign-in from the
// host itself, and a browser that signed in here before (see knownDevice).
type authAdmission struct {
	mu       sync.Mutex
	global   authBucket
	reserve  authBucket
	local    authBucket
	known    authBucket
	peers    map[string]authBucket
	password int
}

// allowLogin admits one password check from peer. Callers invoke it only for a
// request that is about to be verified, never for a request that carries no
// credential. known reports a browser that proved an earlier sign-in.
func (a *authAdmission) allowLogin(peer string, now time.Time, known ...bool) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.global.refill(now, 20, 3*time.Second)
	a.reserve.refill(now, 3, 20*time.Second)
	a.local.refill(now, 3, 20*time.Second)
	a.known.refill(now, 3, 20*time.Second)
	proven := len(known) > 0 && known[0]
	bucket, tracked, ok := a.peerBucket(peer, now)
	if !ok {
		// Others on the same network may have used up its allowance; a proven
		// browser is not held to it.
		if proven && a.known.tokens >= 1 {
			a.known.tokens--
			return true
		}
		return false
	}
	// A peer with a full bucket made no attempt in the last half minute. An
	// address that cannot be tracked because the table is full of recent peers
	// has no recorded attempts either.
	clean := !tracked || bucket.tokens >= 5
	switch {
	case a.global.tokens >= 1:
		a.global.tokens--
	case clean && a.reserve.tokens >= 1:
		a.reserve.tokens--
	case loopbackName(peer) && a.local.tokens >= 1:
		// Fresh addresses are all clean, so a flood from many of them can keep
		// the shared reserve empty. Sign-in from the host itself cannot be
		// crowded out that way.
		a.local.tokens--
	case proven && a.known.tokens >= 1:
		a.known.tokens--
	default:
		return false
	}
	if tracked {
		bucket.tokens--
		a.peers[peer] = bucket
	}
	return true
}

// allowPeer charges only the peer's own bucket. It serves requests that are
// refused without any password work, such as a wrong username, so they stay
// limited per address but never use up the shared budget.
func (a *authAdmission) allowPeer(peer string, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	bucket, tracked, ok := a.peerBucket(peer, now)
	if !ok {
		return false
	}
	if tracked {
		bucket.tokens--
		a.peers[peer] = bucket
	}
	return true
}

// peerBucket returns the refilled bucket for peer. ok is false when the peer has
// no token left. tracked is false when the table is full of recent peers, in
// which case the caller must not store the bucket. The caller holds a.mu.
func (a *authAdmission) peerBucket(peer string, now time.Time) (bucket authBucket, tracked, ok bool) {
	if a.peers == nil {
		a.peers = make(map[string]authBucket)
	}
	bucket, exists := a.peers[peer]
	if !exists && len(a.peers) >= maxAuthPeers {
		for key, previous := range a.peers {
			if now.Sub(previous.last) >= 10*time.Minute {
				delete(a.peers, key)
			}
		}
		if len(a.peers) >= maxAuthPeers {
			return authBucket{}, false, true
		}
	}
	bucket.refill(now, 5, 6*time.Second)
	a.peers[peer] = bucket
	return bucket, true, bucket.tokens >= 1
}

func (a *authAdmission) beginPasswordWork() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.password >= 2 {
		return false
	}
	a.password++
	return true
}

func (a *authAdmission) endPasswordWork() {
	a.mu.Lock()
	a.password--
	a.mu.Unlock()
}

// waitPasswordWork waits a short, bounded time for a password-work slot. Login
// attempts are already limited by allowLogin, so a brief wait keeps a correct
// password from being refused only because two other checks are running.
func (a *authAdmission) waitPasswordWork(ctx context.Context) bool {
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		if a.beginPasswordWork() {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return false
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func authPeer(r *http.Request) string {
	// Proxy headers are attacker-controlled without an explicit trusted-proxy
	// configuration. Use the direct TCP peer, independent of source port.
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil {
		// One network is one peer, so that rotating addresses inside it (an IPv6
		// /64 is a single subscriber) does not make a fresh client each time.
		if v4 := ip.To4(); v4 != nil && !ip.IsLoopback() {
			return v4.Mask(net.CIDRMask(24, 32)).String() + "/24"
		} else if v4 == nil && !ip.IsLoopback() {
			return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
		}
		return ip.String()
	}
	return "unknown"
}

const (
	knownDeviceCookie = "cw_known_device"
	knownDeviceTTL    = 180 * 24 * time.Hour
)

// knownDeviceToken is what a browser keeps after a successful sign-in. It proves
// only that this browser signed in before with the current password, and buys
// nothing but admission to the password check when strangers have used up the
// shared allowance. It is bound to the server key and the password, so it
// cannot be made without them and a password change retires it.
func knownDeviceToken(key []byte, hash string, expires time.Time) string {
	exp := strconv.FormatInt(expires.Unix(), 10)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("known-device\x00" + exp + "\x00" + hash))
	return exp + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// knownDevice reports whether the request carries a valid, unexpired token.
func (s *Server) knownDevice(r *http.Request, hash string) bool {
	c, err := r.Cookie(knownDeviceCookie)
	if err != nil || len(s.jwtKey) < 32 {
		return false
	}
	exp, _, _ := strings.Cut(c.Value, ".")
	unix, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || time.Now().Unix() >= unix {
		return false
	}
	return hmac.Equal([]byte(c.Value), []byte(knownDeviceToken(s.jwtKey, hash, time.Unix(unix, 0))))
}

// rememberDevice hands the browser its token after a successful sign-in. The
// cookie goes only to the sign-in endpoint and is not readable by scripts.
func (s *Server) rememberDevice(w http.ResponseWriter, r *http.Request, hash string) {
	if len(s.jwtKey) < 32 {
		return
	}
	expires := time.Now().Add(knownDeviceTTL)
	http.SetCookie(w, &http.Cookie{
		Name: knownDeviceCookie, Value: knownDeviceToken(s.jwtKey, hash, expires), Path: "/api/auth/login",
		Expires: expires, MaxAge: int(knownDeviceTTL.Seconds()), HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode,
	})
}

func authBusy(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "6")
	writeErr(w, http.StatusTooManyRequests, locale.Text(responseLanguage(w), "Too many authentication requests; try again shortly"))
}

func (s *Server) beginPasswordWork(w http.ResponseWriter) bool {
	if !s.authGate.beginPasswordWork() {
		authBusy(w)
		return false
	}
	return true
}

// waitPasswordWork is beginPasswordWork for a login: it queues briefly for a slot
// before answering that the server is busy.
func (s *Server) waitPasswordWork(w http.ResponseWriter, r *http.Request) bool {
	if !s.authGate.waitPasswordWork(r.Context()) {
		authBusy(w)
		return false
	}
	return true
}
