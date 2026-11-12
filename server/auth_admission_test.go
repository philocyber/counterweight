package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLoginAdmissionRefillsAndBoundsDistributedAttempts(t *testing.T) {
	var admission authAdmission
	now := time.Unix(1, 0)
	for i := 0; i < 5; i++ {
		if !admission.allowLogin("192.0.2.1", now) {
			t.Fatal("normal initial login allowance exhausted early")
		}
	}
	if admission.allowLogin("192.0.2.1", now) {
		t.Fatal("sixth immediate attempt was accepted")
	}
	if !admission.allowLogin("192.0.2.1", now.Add(6*time.Second)) {
		t.Fatal("login did not recover after the retry interval")
	}
	var distributed authAdmission
	for i := 0; i < 20; i++ {
		if !distributed.allowLogin(fmt.Sprintf("192.0.2.%d", i), now) {
			t.Fatal("global burst exhausted early")
		}
	}
	// An exhausted global budget leaves a small reserve for peers with no recent
	// attempt, and that reserve is bounded too.
	for i := 0; i < 3; i++ {
		if !distributed.allowLogin(fmt.Sprintf("198.51.100.%d", i), now) {
			t.Fatalf("reserve did not admit clean peer %d", i)
		}
	}
	if distributed.allowLogin("198.51.100.9", now) {
		t.Fatal("rotating client addresses bypassed the global limit")
	}
	// Sign-in from the host itself keeps a bounded reserve of its own.
	for i := 0; i < 3; i++ {
		if !distributed.allowLogin("127.0.0.1", now) {
			t.Fatalf("the host's own sign-in %d was crowded out", i)
		}
	}
	if distributed.allowLogin("::1", now) {
		t.Fatal("the host's own reserve is not bounded")
	}
	if !distributed.allowLogin("198.51.100.9", now.Add(3*time.Second)) {
		t.Fatal("global admission did not refill")
	}
}

func TestLoginAdmissionPeerStorageIsBounded(t *testing.T) {
	var admission authAdmission
	now := time.Unix(1, 0)
	admission.peers = make(map[string]authBucket)
	for i := 0; i < maxAuthPeers; i++ {
		admission.peers[fmt.Sprint(i)] = authBucket{tokens: 5, last: now}
	}
	// An untracked peer still logs in (it has no recorded attempts), but the
	// table does not grow.
	if !admission.allowLogin("new peer", now) || len(admission.peers) > maxAuthPeers {
		t.Fatal("active peer table exceeded its budget or refused a new client")
	}
	if !admission.allowLogin("new peer", now.Add(10*time.Minute)) {
		t.Fatal("expired peer entries prevented new clients from logging in")
	}
}

func TestPasswordWorkConcurrencyBudget(t *testing.T) {
	var admission authAdmission
	var workers sync.WaitGroup
	accepted := make(chan bool, 32)
	release := make(chan struct{})
	for i := 0; i < cap(accepted); i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			ok := admission.beginPasswordWork()
			accepted <- ok
			if ok {
				<-release
				admission.endPasswordWork()
			}
		}()
	}
	count := 0
	for i := 0; i < cap(accepted); i++ {
		if <-accepted {
			count++
		}
	}
	close(release)
	workers.Wait()
	if count != 2 {
		t.Fatalf("admitted %d simultaneous password jobs, want 2", count)
	}
	if !admission.beginPasswordWork() {
		t.Fatal("completed work did not release admission")
	}
	admission.endPasswordWork()
}

func TestLoginRateLimitIgnoresForwardedIdentity(t *testing.T) {
	s := &Server{m: &Manager{}}
	for i := 0; i < 6; i++ {
		r := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"wrong","password":"test"}`))
		r.RemoteAddr = fmt.Sprintf("192.0.2.10:%d", 1000+i)
		r.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d", i))
		w := httptest.NewRecorder()
		s.authLogin(w, r)
		want := http.StatusUnauthorized
		if i == 5 {
			want = http.StatusTooManyRequests
			if w.Header().Get("Retry-After") == "" {
				t.Fatal("busy response lacks retry advice")
			}
		}
		if w.Code != want {
			t.Fatalf("request %d returned %d, want %d", i, w.Code, want)
		}
	}
}

func TestLoginAdmissionKeepsCleanPeerAdmittedAfterAttackersDrainBudget(t *testing.T) {
	var admission authAdmission
	now := time.Unix(1, 0)
	for i := 0; i < 23; i++ {
		admission.allowLogin(fmt.Sprintf("203.0.113.%d", i), now)
	}
	if admission.allowLogin("192.0.2.50", now) {
		t.Fatal("budget was not exhausted")
	}
	if !admission.allowLogin("192.0.2.50", now.Add(3*time.Second)) {
		t.Fatal("a clean peer was not admitted after the global refill")
	}
	var busy authAdmission
	for i := 0; i < 5; i++ {
		busy.allowLogin("192.0.2.60", now)
	}
	if busy.allowLogin("192.0.2.60", now) {
		t.Fatal("throttled peer was admitted")
	}
}

func TestLoginRequestsWithoutCredentialsAreNotCharged(t *testing.T) {
	s := &Server{m: &Manager{}}
	for i := 0; i < 100; i++ {
		r := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(""))
		r.RemoteAddr = fmt.Sprintf("192.0.2.%d:1000", i%250+1)
		w := httptest.NewRecorder()
		s.authLogin(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("empty request %d returned %d", i, w.Code)
		}
	}
	for i := 0; i < 30; i++ {
		r := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"x","password":"y"}`))
		r.RemoteAddr = fmt.Sprintf("198.51.%d.1:1000", i+1)
		w := httptest.NewRecorder()
		s.authLogin(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("wrong username %d returned %d", i, w.Code)
		}
	}
	for i := 0; i < 20; i++ {
		if !s.authGate.allowLogin(fmt.Sprintf("192.0.2.%d", i+1), time.Now()) {
			t.Fatal("credential-less traffic used up the shared login budget")
		}
	}
}

func TestLoginWaitsBrieflyForPasswordWorkSlot(t *testing.T) {
	var admission authAdmission
	if !admission.beginPasswordWork() || !admission.beginPasswordWork() {
		t.Fatal("could not take both slots")
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		admission.endPasswordWork()
	}()
	if !admission.waitPasswordWork(context.Background()) {
		t.Fatal("login was refused although a slot freed up shortly after")
	}
	admission.endPasswordWork()
	admission.endPasswordWork()
}

// Addresses of one network are one peer, so rotating inside it gains nothing.
func TestLoginPeerIsTheNetwork(t *testing.T) {
	peer := func(addr string) string {
		r := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
		r.RemoteAddr = addr
		return authPeer(r)
	}
	for _, same := range [][2]string{
		{"192.0.2.10:1", "192.0.2.200:2"},
		{"[2001:db8:1:2::1]:1", "[2001:db8:1:2:ffff:ffff:ffff:ffff]:2"},
		{"[::ffff:192.0.2.10]:1", "192.0.2.77:2"},
	} {
		if peer(same[0]) != peer(same[1]) {
			t.Errorf("%s and %s are different peers: %s %s", same[0], same[1], peer(same[0]), peer(same[1]))
		}
	}
	for _, other := range [][2]string{
		{"192.0.2.10:1", "192.0.3.10:1"},
		{"[2001:db8:1:2::1]:1", "[2001:db8:1:3::1]:1"},
		{"127.0.0.1:1", "127.0.0.2:1"},
	} {
		if peer(other[0]) == peer(other[1]) {
			t.Errorf("%s and %s are one peer: %s", other[0], other[1], peer(other[0]))
		}
	}
	if peer("127.0.0.1:9") != "127.0.0.1" || peer("[::1]:9") != "::1" || !loopbackName(peer("[::1]:9")) {
		t.Errorf("loopback peers: %q %q", peer("127.0.0.1:9"), peer("[::1]:9"))
	}
}

// A flood from ever-new networks uses up the shared allowance and its reserve.
// A browser that signed in before is still admitted, within a bound of its own,
// and also when others on its network used up that network's allowance.
func TestLoginAdmissionKeepsKnownBrowserAdmitted(t *testing.T) {
	var admission authAdmission
	now := time.Unix(1, 0)
	for i := 0; i < 23; i++ {
		if !admission.allowLogin(fmt.Sprintf("203.0.%d.0/24", i), now) {
			t.Fatalf("flood attempt %d was not admitted while allowance remained", i)
		}
	}
	if admission.allowLogin("198.51.100.0/24", now) {
		t.Fatal("an unknown client was admitted after the allowance was used up")
	}
	for i := 0; i < 3; i++ {
		if !admission.allowLogin("198.51.100.0/24", now, true) {
			t.Fatalf("known browser attempt %d was crowded out", i)
		}
	}
	if admission.allowLogin("192.0.2.0/24", now, true) {
		t.Fatal("the known-browser reserve is not bounded")
	}
	var shared authAdmission
	for i := 0; i < 5; i++ {
		shared.allowLogin("192.0.2.0/24", now)
	}
	if shared.allowLogin("192.0.2.0/24", now) {
		t.Fatal("the network's own allowance was not used up")
	}
	if !shared.allowLogin("192.0.2.0/24", now, true) {
		t.Fatal("a known browser was held to an allowance others on its network used up")
	}
}

// The token a browser keeps is bound to the server key and the password, expires,
// and reaches only the sign-in endpoint.
func TestKnownDeviceToken(t *testing.T) {
	s := &Server{m: &Manager{}, jwtKey: []byte(strings.Repeat("k", 32))}
	w := httptest.NewRecorder()
	s.rememberDevice(w, httptest.NewRequest(http.MethodPost, "/api/auth/login", nil), "hash-one")
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != knownDeviceCookie || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].Path != "/api/auth/login" {
		t.Fatalf("cookie: %+v", cookies)
	}
	with := func(value string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
		r.AddCookie(&http.Cookie{Name: knownDeviceCookie, Value: value})
		return r
	}
	good := cookies[0].Value
	if !s.knownDevice(with(good), "hash-one") {
		t.Fatal("a valid token was not accepted")
	}
	expired := knownDeviceToken(s.jwtKey, "hash-one", time.Now().Add(-time.Minute))
	other := &Server{m: &Manager{}, jwtKey: []byte(strings.Repeat("z", 32))}
	for name, ok := range map[string]bool{
		"after a password change": s.knownDevice(with(good), "hash-two"),
		"altered":                 s.knownDevice(with(good[:len(good)-2]+"AA"), "hash-one"),
		"expiry moved":            s.knownDevice(with("9"+good), "hash-one"),
		"expired":                 s.knownDevice(with(expired), "hash-one"),
		"another server key":      other.knownDevice(with(good), "hash-one"),
		"empty":                   s.knownDevice(with(""), "hash-one"),
		"no key":                  (&Server{m: &Manager{}}).knownDevice(with(good), "hash-one"),
	} {
		if ok {
			t.Errorf("token accepted: %s", name)
		}
	}
	if s.knownDevice(httptest.NewRequest(http.MethodPost, "/api/auth/login", nil), "hash-one") {
		t.Error("a request without the cookie counted as known")
	}
}
