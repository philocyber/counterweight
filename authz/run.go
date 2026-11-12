package authz

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	maxBody        = 1 << 20 // response bytes read per request
	excerptBytes   = 300     // response bytes kept as evidence of a check that did not pass
	defaultTimeout = 15 * time.Second
)

// Options configures a run.
type Options struct {
	// Getenv reads the variables the policy names. Default: os.Getenv.
	Getenv func(string) string
	// Redact, when set, is applied to response excerpts after the run's own
	// credentials were removed, to mask other credential shapes.
	Redact func(string) string
	// Accounts are the test accounts of the task the policy runs in, by key.
	Accounts map[string]Account
	// NoEnv refuses ${env:NAME}. A server sets it: a policy must not be able to
	// send the server's own environment to a target.
	NoEnv bool
	// CheckURL, when set, is asked before every request whether its
	// destination may be contacted, and CheckPeer before every connection
	// whether the address it resolved to may be. A server uses them to apply
	// its blocked-target rules.
	CheckURL  func(*url.URL) error
	CheckPeer func(net.IP) error
}

type session struct {
	headers map[string]string
	cookies []*http.Cookie
}

type response struct {
	status       int
	body         []byte
	truncated    bool
	location     string // Location header of a redirect, which is never followed
	contentType  string
	contentRange string // Content-Range header, set on a partial answer
	cookies      []*http.Cookie
	elapsed      time.Duration
}

type runner struct {
	client   *http.Client
	base     *url.URL
	red      *redactor
	checkURL func(*url.URL) error
}

// Run executes the policy and returns the report. It never returns an error:
// whatever stops a run is recorded in the report, which then cannot pass.
func Run(ctx context.Context, p *Policy, opt Options) *Report {
	start := time.Now()
	sum := sha256.Sum256(p.raw)
	rep := &Report{Schema: ReportSchema, PolicySHA256: hex.EncodeToString(sum[:]), StartedAt: start.UTC()}
	defer func() { rep.finish(time.Since(start)) }()

	rp, secrets, err := p.resolve(opt)
	if err != nil {
		rep.Errors = append(rep.Errors, err.Error())
		return rep
	}
	base, err := parseBaseURL(rp.Target.BaseURL, rp.Target.AllowPlainHTTP)
	if err != nil {
		rep.Errors = append(rep.Errors, "target.base_url: "+err.Error())
		return rep
	}
	timeout := defaultTimeout
	if rp.Target.TimeoutSeconds > 0 {
		timeout = time.Duration(rp.Target.TimeoutSeconds) * time.Second
	}
	r := &runner{client: newClient(timeout, opt.CheckPeer), base: base, red: &redactor{extra: opt.Redact}, checkURL: opt.CheckURL}
	defer r.client.CloseIdleConnections()
	for _, s := range secrets {
		r.red.add(s)
	}
	rep.Target = r.red.clean(base.String())

	// Every persona must log in and prove its identity before any case runs.
	sessions := map[string]*session{}
	for _, name := range rp.order {
		per := rp.Personas[name]
		s, err := r.open(ctx, per)
		if err != nil {
			rep.Errors = append(rep.Errors, "persona "+name+": "+r.red.clean(err.Error()))
		}
		sessions[name] = s
		role := per.Role
		if role == "" {
			role = opt.Accounts[name].Role
		}
		rep.Personas = append(rep.Personas, PersonaResult{Name: name, Role: role, Authenticated: per.credentialed(), IdentityVerified: per.credentialed() && err == nil})
	}
	if len(rep.Errors) > 0 {
		return rep
	}

	for i, c := range rp.Cases {
		if err := ctx.Err(); err != nil {
			rep.Errors = append(rep.Errors, fmt.Sprintf("the run was stopped after %d of %d cases: %v", i, len(rp.Cases), err))
			break
		}
		// The report shows the path as written in the policy, references unresolved.
		rep.Cases = append(rep.Cases, r.runCase(ctx, c, p.Cases[i].Request.Path, rp.order, sessions))
	}

	// A session that stopped working during the run makes its results unreliable.
	for _, name := range rp.order {
		per := rp.Personas[name]
		if per.Identity == nil {
			continue
		}
		if err := r.verify(ctx, per.Identity, sessions[name]); err != nil {
			rep.Errors = append(rep.Errors, "persona "+name+": the session stopped working during the run: "+r.red.clean(err.Error()))
			rep.sessionLost(name)
		}
	}
	return rep
}

// newClient builds the only HTTP client of a run. It ignores proxy settings
// of the environment (a recording proxy would store the credentials) and
// never follows a redirect (the credentials stay on the pinned target, and a
// redirect is itself the answer to judge). Every request opens its own
// connection: on a reused one the transport silently resends a request the
// server dropped, and a check must be sent exactly once. checkPeer, when set,
// sees the address of every connection after name resolution, so a host name
// cannot lead to an address the caller refuses.
func newClient(timeout time.Duration, checkPeer func(net.IP) error) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	if checkPeer != nil {
		dialer.Control = func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			return checkPeer(net.ParseIP(host))
		}
	}
	return &http.Client{
		Timeout:       timeout,
		Transport:     &http.Transport{Proxy: nil, DialContext: dialer.DialContext, TLSHandshakeTimeout: 10 * time.Second, DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// open builds the session of a persona: static credentials, then login, then
// the identity check.
func (r *runner) open(ctx context.Context, per *Persona) (*session, error) {
	s := &session{headers: map[string]string{}}
	for k, v := range per.Headers {
		s.headers[k] = v
	}
	names := make([]string, 0, len(per.Cookies))
	for k := range per.Cookies {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		s.cookies = append(s.cookies, &http.Cookie{Name: k, Value: per.Cookies[k]})
	}
	if l := per.Login; l != nil {
		resp, err := r.send(ctx, &l.Request, nil)
		if err != nil {
			return s, fmt.Errorf("login request failed: %w", err)
		}
		if !l.Status.has(resp.status) {
			return s, fmt.Errorf("login answered %d, expected %s", resp.status, statusList(l.Status))
		}
		for _, c := range resp.cookies {
			r.red.add(c.Value)
			s.cookies = append(s.cookies, &http.Cookie{Name: c.Name, Value: c.Value})
		}
		switch t := l.Token; {
		case t != nil:
			token, err := jsonPointer(resp.body, t.JSONPointer)
			if err != nil {
				return s, fmt.Errorf("login token: %w", err)
			}
			r.red.add(token)
			s.headers[t.Header] = t.Prefix + token
		case len(resp.cookies) == 0:
			return s, errors.New("login set no cookie and the policy has no token rule, so there is no session")
		}
	}
	// A target may repeat a whole header or cookie pair in an encoded form
	// ("Bearer <token>" in Base64). Register the ones that carry a credential.
	for _, v := range s.headers {
		if r.red.clean(v) != v {
			r.red.add(v)
		}
	}
	for _, c := range s.cookies {
		r.red.add(c.Name + "=" + c.Value)
	}
	if per.Identity != nil {
		return s, r.verify(ctx, per.Identity, s)
	}
	return s, nil
}

func (r *runner) verify(ctx context.Context, id *Probe, s *session) error {
	resp, err := r.send(ctx, &id.Request, s)
	if err != nil {
		return fmt.Errorf("identity check failed: %w", err)
	}
	if !id.Status.has(resp.status) {
		return fmt.Errorf("identity check answered %d, expected %s (expired or wrong credential?)", resp.status, statusList(id.Status))
	}
	if m := firstMissing(resp.body, id.BodyContains); m != "" {
		return fmt.Errorf("identity check does not show %q: the credential belongs to someone else", r.red.clean(m))
	}
	return nil
}

func (r *runner) send(ctx context.Context, q *Request, s *session) (*response, error) {
	target, err := r.url(q.Path)
	if err != nil {
		return nil, err
	}
	if r.checkURL != nil {
		u, _ := url.Parse(target)
		if err := r.checkURL(u); err != nil {
			return nil, err
		}
	}
	var body io.Reader
	contentType := ""
	switch {
	case q.JSON != nil:
		b, err := json.Marshal(q.JSON)
		if err != nil {
			return nil, errors.New("the json body cannot be written as JSON")
		}
		body, contentType = bytes.NewReader(b), "application/json"
	case q.Form != nil:
		form := url.Values{}
		for k, v := range q.Form {
			form.Set(k, v)
		}
		body, contentType = strings.NewReader(form.Encode()), "application/x-www-form-urlencoded"
	case q.Body != "":
		body = strings.NewReader(q.Body)
	}
	req, err := http.NewRequestWithContext(ctx, q.Method, target, body)
	if err != nil {
		return nil, errors.New("the request could not be built")
	}
	req.Header.Set("User-Agent", "counterweight-authz")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range q.Headers {
		req.Header.Set(k, v)
	}
	if s != nil {
		for k, v := range s.headers {
			req.Header.Set(k, v)
		}
		for _, c := range s.cookies {
			req.AddCookie(c)
		}
	}
	start := time.Now()
	resp, err := r.client.Do(req)
	if err != nil {
		// The client's error quotes the full URL, with every variable of the
		// path resolved. Only its cause is kept.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, err
	}
	out := &response{status: resp.StatusCode, location: resp.Header.Get("Location"), contentType: resp.Header.Get("Content-Type"), contentRange: resp.Header.Get("Content-Range"), cookies: resp.Cookies(), elapsed: time.Since(start)}
	if len(b) > maxBody {
		b, out.truncated = b[:maxBody], true
	}
	out.body = b
	return out, nil
}

// url joins a request path to the base URL and refuses anything that would
// leave the pinned target.
func (r *runner) url(path string) (string, error) {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return "", errors.New("the request path must start with a single /")
	}
	u, err := url.Parse(strings.TrimRight(r.base.String(), "/") + path)
	if err != nil || u.Scheme != r.base.Scheme || u.Host != r.base.Host || u.User != nil {
		return "", errors.New("the request path does not stay on the target")
	}
	return u.String(), nil
}

func (r *runner) runCase(ctx context.Context, c *Case, pathTemplate string, order []string, sessions map[string]*session) CaseResult {
	cr := CaseResult{ID: c.ID, Method: c.Request.Method, Path: pathTemplate, Write: methods[c.Request.Method]}
	// Denied personas go first: an allowed write (a DELETE by the owner) must
	// not remove the resource before the denials are tried against it.
	var denied, allowed []string
	var allowStatuses Statuses
	for _, name := range order {
		if e := c.Expect[name]; len(e.Allow) > 0 {
			allowed = append(allowed, name)
			allowStatuses = append(allowStatuses, e.Allow...)
		} else {
			denied = append(denied, name)
		}
	}
	baseline := false
	var granted [][]byte // what the allowed personas that got through received
	for _, name := range append(denied, allowed...) {
		resp, err := r.send(ctx, &c.Request, sessions[name])
		check := r.judge(name, c.Expect[name], allowStatuses, resp, err)
		if check.Expect == "allow" && check.Result == Pass {
			baseline = true
			granted = append(granted, resp.body)
		}
		cr.Checks = append(cr.Checks, check)
	}
	for i := range cr.Checks {
		ch := &cr.Checks[i]
		if ch.Expect != "deny" || ch.Result != Pass {
			continue
		}
		e := c.Expect[ch.Persona]
		switch {
		// Without a persona that got through, a denial proves nothing: the
		// endpoint may be broken or gone for everyone.
		case len(allowed) > 0 && !baseline:
			ch.Result, ch.Kind = Inconclusive, "no_baseline"
			ch.Detail = "no allowed persona got through, so this denial cannot be told apart from a broken endpoint"
		// A denial whose status also means access rests on its content checks.
		// The absence of a text proves something only if access shows that text.
		case grantsAccess(ch.Status, allowStatuses) && !e.evidence():
			for _, m := range e.BodyNotContains {
				if !inAny(granted, m) {
					ch.Result, ch.Kind = Inconclusive, "unproven_marker"
					ch.Detail = r.red.clean(fmt.Sprintf("the status also means access, and %q is not in what an allowed persona received, so its absence proves nothing", r.red.clean(m)))
					break
				}
			}
		}
	}
	return cr
}

func inAny(bodies [][]byte, marker string) bool {
	for _, b := range bodies {
		if bytes.Contains(b, []byte(marker)) {
			return true
		}
	}
	return false
}

// judge compares one answer with the expectation. allowStatuses are the
// statuses that mean access for the case's allowed personas.
func (r *runner) judge(name string, e *Expectation, allowStatuses Statuses, resp *response, err error) CheckResult {
	want, mode := e.Deny, "deny"
	if len(e.Allow) > 0 {
		want, mode = e.Allow, "allow"
	}
	ch := CheckResult{Persona: name, Expect: mode, ExpectedStatus: want}
	set := func(result, kind, format string, a ...any) {
		// A text is cleaned before it is quoted: quoting rewrites a credential
		// that holds a quote or a backslash, and it would no longer be found.
		for i, v := range a {
			if s, ok := v.(string); ok {
				a[i] = r.red.clean(s)
			}
		}
		ch.Result, ch.Kind, ch.Detail = result, kind, r.red.clean(fmt.Sprintf(format, a...))
	}
	if err != nil {
		set(Inconclusive, "request_failed", "no answer: %v", err)
		return ch
	}
	ch.Status, ch.DurationMS, ch.ResponseBytes = resp.status, resp.elapsed.Milliseconds(), len(resp.body)
	got := resp.status
	switch {
	case want.has(got):
		missing, exposed := firstMissing(resp.body, e.BodyContains), firstPresent(resp.body, e.BodyNotContains)
		elsewhere := e.LocationContains != "" && !strings.Contains(resp.location, e.LocationContains)
		// When the status of a denial is one that also means access, a missing
		// sign of the denial is what access looks like.
		ambiguous := mode == "deny" && grantsAccess(got, allowStatuses)
		switch {
		case exposed != "":
			set(Fail, "data_exposed", "the response contains %q", exposed)
		case elsewhere && ambiguous:
			set(Fail, "access_granted", "the status matches a denial but the redirect does not go to %q, as when access is granted", e.LocationContains)
		case elsewhere:
			set(Fail, "unexpected_redirect", "the status matches but the redirect does not go to %q", e.LocationContains)
		case resp.truncated && (missing != "" || len(e.BodyNotContains) > 0):
			set(Inconclusive, "truncated", "the response is larger than %d bytes, so its content could not be checked in full", maxBody)
		case missing != "" && ambiguous:
			set(Fail, "access_granted", "the status matches a denial but the response does not contain %q, as when access is granted", missing)
		case missing != "":
			set(Fail, "content_missing", "the status matches but the response does not contain %q", missing)
		default:
			ch.Result = Pass
		}
	case got == http.StatusTooManyRequests || got >= 500:
		set(Inconclusive, "server_error", "the target answered %d, which says nothing about authorization", got)
	case mode == "deny" && grantsAccess(got, allowStatuses):
		set(Fail, "access_granted", "expected a denial (%s), the target answered %d", statusList(want), got)
	case mode == "allow" && (got == http.StatusUnauthorized || got == http.StatusForbidden || got == http.StatusNotFound):
		set(Fail, "access_denied", "expected access (%s), the target answered %d", statusList(want), got)
	default:
		set(Fail, "unexpected_status", "expected %s, the target answered %d", statusList(want), got)
	}
	if ch.Result != Pass {
		ch.Excerpt = r.red.excerpt(resp.body)
	}
	return ch
}

func firstMissing(body []byte, markers []string) string {
	for _, m := range markers {
		if !bytes.Contains(body, []byte(m)) {
			return m
		}
	}
	return ""
}

func firstPresent(body []byte, markers []string) string {
	for _, m := range markers {
		if bytes.Contains(body, []byte(m)) {
			return m
		}
	}
	return ""
}

func statusList(s Statuses) string {
	parts := make([]string, len(s))
	for i, c := range s {
		parts[i] = strconv.Itoa(c)
	}
	return strings.Join(parts, " or ")
}

// jsonPointer returns the non-empty string at an RFC 6901 pointer of a JSON document.
func jsonPointer(doc []byte, pointer string) (string, error) {
	var v any
	if err := json.Unmarshal(doc, &v); err != nil {
		return "", errors.New("the response is not JSON")
	}
	for _, tok := range strings.Split(pointer, "/")[1:] {
		tok = strings.ReplaceAll(strings.ReplaceAll(tok, "~1", "/"), "~0", "~")
		switch t := v.(type) {
		case map[string]any:
			v = t[tok]
		case []any:
			i, err := strconv.Atoi(tok)
			if err != nil || i < 0 || i >= len(t) {
				return "", fmt.Errorf("the response has nothing at %s", pointer)
			}
			v = t[i]
		default:
			return "", fmt.Errorf("the response has nothing at %s", pointer)
		}
	}
	s, ok := v.(string)
	if !ok || s == "" {
		return "", fmt.Errorf("the response has no text value at %s", pointer)
	}
	return s, nil
}

// redactor removes the credentials of a run from anything that is reported.
// A broker signs personas in while other requests are being answered, so it
// is safe for concurrent use.
type redactor struct {
	mu     sync.RWMutex
	values []string
	extra  func(string) string
}

func (r *redactor) add(v string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Also the forms the value takes on the wire and in an answer that repeats
	// it: trimmed in a header, escaped in a URL, in JSON, in HTML, in a quoted
	// text, in Base64 or hexadecimal. A target can still transform a value in a
	// way that is not listed; no list of forms can be complete.
	for _, raw := range []string{v, strings.TrimSpace(v)} {
		inJSON, _ := json.Marshal(raw)
		quoted := strconv.Quote(raw)
		for _, form := range []string{
			raw, url.QueryEscape(raw), url.PathEscape(raw), string(inJSON[1 : len(inJSON)-1]), quoted[1 : len(quoted)-1], html.EscapeString(raw),
			base64.StdEncoding.EncodeToString([]byte(raw)), base64.RawStdEncoding.EncodeToString([]byte(raw)),
			base64.URLEncoding.EncodeToString([]byte(raw)), base64.RawURLEncoding.EncodeToString([]byte(raw)),
			hex.EncodeToString([]byte(raw)), strings.ToUpper(hex.EncodeToString([]byte(raw))),
		} {
			if form != "" && !slices.Contains(r.values, form) {
				r.values = append(r.values, form)
			}
		}
	}
	// Longest first, so a credential that contains another is removed whole.
	sort.SliceStable(r.values, func(i, j int) bool { return len(r.values[i]) > len(r.values[j]) })
}

func (r *redactor) clean(s string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, v := range r.values {
		s = strings.ReplaceAll(s, v, "[REDACTED]")
	}
	return s
}

// excerpt returns the start of a response body with credentials removed. The
// whole body is cleaned before it is cut, so a credential is never cut in half.
func (r *redactor) excerpt(body []byte) string {
	s := r.clean(strings.ToValidUTF8(string(body), "?"))
	if r.extra != nil {
		s = r.extra(s)
	}
	if len(s) > excerptBytes {
		s = strings.ToValidUTF8(s[:excerptBytes], "") + "..."
	}
	return s
}
