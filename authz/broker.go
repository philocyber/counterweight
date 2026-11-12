package authz

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// A Broker sends single requests as the personas of a policy for a caller that
// must not hold the credentials. The caller (an agent tool) names a persona
// and a request; the broker signs in with the stored credentials, sends the
// request to the policy's one target and returns the answer with the
// credentials removed.
//
// What the caller controls is the request and nothing else: the method, a
// path on the target, its own headers and a body. It cannot choose the host,
// cannot set an Authorization or Cookie header, and nothing it writes is
// resolved as a reference, so "${account:...}" in a request is plain text.

// maxAnswerBody is how much of a response body an answer carries.
const maxAnswerBody = 64 << 10

type Broker struct {
	source *Policy // as written, for the references of each persona
	policy *Policy // resolved
	r      *runner
	roles  map[string]string

	mu       sync.Mutex
	sessions map[string]*session
	closed   atomic.Bool
}

// Answer is what the caller gets back. It holds no credential of the policy.
type Answer struct {
	Status      int    `json:"status"`
	Location    string `json:"location,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	Body        string `json:"body"`
	Truncated   bool   `json:"truncated,omitempty"`
}

// NewBroker resolves the policy like a run does. It sends nothing: a persona
// signs in when its first request is sent.
func NewBroker(p *Policy, opt Options) (*Broker, error) {
	rp, secrets, err := p.resolve(opt)
	if err != nil {
		return nil, err
	}
	base, err := parseBaseURL(rp.Target.BaseURL, rp.Target.AllowPlainHTTP)
	if err != nil {
		return nil, fmt.Errorf("target.base_url: %w", err)
	}
	timeout := defaultTimeout
	if rp.Target.TimeoutSeconds > 0 {
		timeout = time.Duration(rp.Target.TimeoutSeconds) * time.Second
	}
	b := &Broker{
		source: p, policy: rp, roles: map[string]string{}, sessions: map[string]*session{},
		r: &runner{client: newClient(timeout, opt.CheckPeer), base: base, red: &redactor{extra: opt.Redact}, checkURL: opt.CheckURL},
	}
	for _, s := range secrets {
		b.r.red.add(s)
	}
	for _, name := range rp.order {
		b.roles[name] = rp.Personas[name].Role
		if b.roles[name] == "" {
			b.roles[name] = opt.Accounts[name].Role
		}
	}
	return b, nil
}

// Close revokes the broker: every later request is refused, also from a
// caller that obtained the broker before. The owner closes it when the
// accounts or the policy it was built from change.
func (b *Broker) Close() {
	b.closed.Store(true)
	b.r.client.CloseIdleConnections()
}

// ErrBrokerClosed is returned by Do after Close.
var ErrBrokerClosed = errors.New("the test accounts or the policy changed; send the request again")

// Target is the address every request goes to.
func (b *Broker) Target() string { return b.r.red.clean(b.r.base.String()) }

// Personas returns the persona names in policy order.
func (b *Broker) Personas() []string { return b.policy.PersonaNames() }

// Role returns the name shown for a persona, or "" when it has none.
func (b *Broker) Role(persona string) string { return b.roles[persona] }

// PersonaAccounts lists the test accounts a persona sends anything of: the
// ones referenced under its headers, cookies and login, and in the request
// of its identity check. An anonymous persona has none.
func (p *Policy) PersonaAccounts(name string) []string {
	per := p.Personas[name]
	if per == nil {
		return nil
	}
	seen := map[string]bool{}
	collect := func(s string) string {
		for _, m := range refRe.FindAllStringSubmatch(s, -1) {
			if r := parseRef(m[1]); r.Account != "" {
				seen[r.Account] = true
			}
		}
		return s
	}
	mapValues(per.Headers, collect)
	mapValues(per.Cookies, collect)
	if per.Login != nil {
		per.Login.Request.eachString(collect)
	}
	if per.Identity != nil {
		per.Identity.Request.eachString(collect)
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// PersonaAccounts lists the accounts behind a persona of the broker's policy.
func (b *Broker) PersonaAccounts(persona string) []string { return b.source.PersonaAccounts(persona) }

// URL returns the full address a request path would be sent to, or an error
// when the path would leave the target. It sends nothing.
func (b *Broker) URL(path string) (string, error) { return b.r.url(path) }

// Do sends one request as a persona and returns the answer. The first request
// of a persona signs it in and checks its identity, exactly as a run does; a
// credential that is expired or belongs to someone else is an error.
func (b *Broker) Do(ctx context.Context, persona string, q Request) (*Answer, error) {
	per := b.policy.Personas[persona]
	if per == nil {
		return nil, fmt.Errorf("the policy has no persona %q", persona)
	}
	if err := limitAgentHeaders(&q); err != nil {
		return nil, err
	}
	var problems []string
	q.validate("request", false, func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) })
	if len(problems) > 0 {
		return nil, errors.New(strings.Join(problems, "; "))
	}
	if b.closed.Load() {
		return nil, ErrBrokerClosed
	}
	s, err := b.session(ctx, persona, per)
	if err != nil {
		return nil, errors.New(b.r.red.clean(err.Error()))
	}
	if b.closed.Load() { // revoked while the persona was signing in
		return nil, ErrBrokerClosed
	}
	resp, err := b.r.send(ctx, &q, s)
	if err != nil {
		return nil, errors.New(b.r.red.clean(err.Error()))
	}
	// The whole body is cleaned before it is cut, so a credential is never cut
	// in half. Besides the broker's own credentials, known credential shapes
	// are masked: a sign-in or refresh route of the target can issue a new one.
	scrub := func(s string) string {
		s = b.r.red.clean(s)
		if b.r.red.extra != nil {
			s = b.r.red.extra(s)
		}
		return s
	}
	// A partial answer would let a credential be read in pieces that the cleaning above cannot match.
	if resp.status == http.StatusPartialContent || resp.contentRange != "" {
		return nil, errors.New("the target answered with a partial response, which is not returned")
	}
	body := scrub(strings.ToValidUTF8(string(resp.body), "?"))
	// Only the media type of the content type is passed on: its parameters are
	// free text the target controls.
	mediaType, _, err := mime.ParseMediaType(resp.contentType)
	if err != nil || len(mediaType) > 100 {
		mediaType = ""
	}
	a := &Answer{Status: resp.status, Location: scrub(resp.location), ContentType: scrub(mediaType), Truncated: resp.truncated}
	if len(body) > maxAnswerBody {
		body, a.Truncated = strings.ToValidUTF8(body[:maxAnswerBody], ""), true
	}
	a.Body = body
	return a, nil
}

// agentHeaders are the headers a caller may set besides the X- ones. Headers that choose which part or
// encoding of a response the target returns (Range, If-Range, Accept-Encoding and the like) are not on
// the list: credential cleaning only recognises whole values in a whole, plain body.
var agentHeaders = map[string]bool{
	"accept": true, "accept-language": true, "content-type": true, "origin": true, "referer": true,
}

// limitAgentHeaders refuses caller headers outside the allow-list and asks the target for an
// unencoded body. The caller's map is replaced, never modified.
func limitAgentHeaders(q *Request) error {
	headers := make(map[string]string, len(q.Headers)+1)
	for k, v := range q.Headers {
		if lk := strings.ToLower(k); !agentHeaders[lk] && !strings.HasPrefix(lk, "x-") {
			return fmt.Errorf("the header %s cannot be set; allowed: Accept, Accept-Language, Content-Type, Origin, Referer and X- headers", k)
		}
		headers[k] = v
	}
	headers["Accept-Encoding"] = "identity"
	q.Headers = headers
	return nil
}

// session returns the signed-in session of a persona, opening it on first use.
// A failed sign-in is not remembered: the next request tries again.
func (b *Broker) session(ctx context.Context, name string, per *Persona) (*session, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if s := b.sessions[name]; s != nil {
		return s, nil
	}
	s, err := b.r.open(ctx, per)
	if err != nil {
		return nil, err
	}
	b.sessions[name] = s
	return s, nil
}
