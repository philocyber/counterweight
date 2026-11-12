// Package authz runs a deterministic authorization check: an explicit policy
// table (personas x requests, with the expected outcome of every pair) is
// replayed against a target over HTTP and each answer is compared with the
// policy. No model takes part, so a run is repeatable and can gate a CI job.
//
// Credentials reach the runner only through environment variables named in the
// policy. They are sent to the pinned target and nowhere else: not to a model,
// not through the traffic recording proxy, not into the report.
package authz

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// Policy is one policy file. Build it with Parse or Load.
type Policy struct {
	Version  int                 `yaml:"version"`
	Target   Target              `yaml:"target"`
	Personas map[string]*Persona `yaml:"personas"`
	Cases    []*Case             `yaml:"cases"`

	order []string // persona names in file order
	raw   []byte
}

// Target is the only host the runner talks to.
type Target struct {
	BaseURL        string `yaml:"base_url" json:"base_url"`
	AllowPlainHTTP bool   `yaml:"allow_plain_http" json:"allow_plain_http,omitempty"`
	TimeoutSeconds int    `yaml:"timeout_seconds" json:"timeout_seconds,omitempty"`
}

// Persona is one identity the requests are sent as. A persona without
// headers, cookies and login is anonymous.
type Persona struct {
	// Role is the name shown in the output. Inside a task it defaults to the
	// role of the test account that has the persona's name.
	Role     string            `yaml:"role" json:"role,omitempty"`
	Headers  map[string]string `yaml:"headers" json:"headers,omitempty"`
	Cookies  map[string]string `yaml:"cookies" json:"cookies,omitempty"`
	Login    *Login            `yaml:"login" json:"login,omitempty"`
	Identity *Probe            `yaml:"identity" json:"identity,omitempty"`
}

func (p *Persona) credentialed() bool {
	return len(p.Headers) > 0 || len(p.Cookies) > 0 || p.Login != nil
}

// Login obtains a session before the run: cookies set by the response are
// kept, and Token, when present, copies a value of the JSON response into a
// request header.
type Login struct {
	Request Request    `yaml:"request" json:"request"`
	Status  Statuses   `yaml:"status" json:"status,omitempty"`
	Token   *TokenRule `yaml:"token" json:"token,omitempty"`
}

// TokenRule reads a token from a JSON login response (RFC 6901 pointer).
type TokenRule struct {
	JSONPointer string `yaml:"json_pointer" json:"json_pointer"`
	Header      string `yaml:"header" json:"header,omitempty"`
	Prefix      string `yaml:"prefix" json:"prefix,omitempty"`
}

// Probe proves that a session belongs to the persona it is configured for.
type Probe struct {
	Request      Request  `yaml:"request" json:"request"`
	Status       Statuses `yaml:"status" json:"status,omitempty"`
	BodyContains []string `yaml:"body_contains" json:"body_contains,omitempty"`
}

// Request is one HTTP request; Path is relative to the target base URL.
type Request struct {
	Method  string            `yaml:"method" json:"method"`
	Path    string            `yaml:"path" json:"path"`
	Headers map[string]string `yaml:"headers" json:"headers,omitempty"`
	JSON    any               `yaml:"json" json:"json,omitempty"`
	// An empty form is still a form body, so it is not left out.
	Form map[string]string `yaml:"form" json:"form"`
	Body string            `yaml:"body" json:"body,omitempty"`
}

// Case is one row of the matrix: a request and its expected outcome for every persona.
type Case struct {
	ID         string                  `yaml:"id" json:"id"`
	AllowWrite bool                    `yaml:"allow_write" json:"allow_write,omitempty"`
	DenyAll    bool                    `yaml:"deny_all" json:"deny_all,omitempty"`
	Request    Request                 `yaml:"request" json:"request"`
	Expect     map[string]*Expectation `yaml:"expect" json:"expect,omitempty"`
}

// Expectation states whether a persona may perform the request (exactly one
// of Allow and Deny) and which status codes say so.
type Expectation struct {
	Allow            Statuses `yaml:"allow" json:"allow,omitempty"`
	Deny             Statuses `yaml:"deny" json:"deny,omitempty"`
	BodyContains     []string `yaml:"body_contains" json:"body_contains,omitempty"`
	BodyNotContains  []string `yaml:"body_not_contains" json:"body_not_contains,omitempty"`
	LocationContains string   `yaml:"location_contains" json:"location_contains,omitempty"`
}

// evidence reports whether the expectation names something a denial shows
// and access does not: a text of the denial page or where it redirects to.
func (e *Expectation) evidence() bool {
	return len(e.BodyContains) > 0 || e.LocationContains != ""
}

// Statuses is a status code or a list of them.
type Statuses []int

func (s *Statuses) UnmarshalYAML(n *yaml.Node) error {
	nodes := []*yaml.Node{n}
	if n.Kind == yaml.SequenceNode {
		nodes = n.Content
	}
	*s = nil
	for _, x := range nodes {
		var code int
		// The tag check refuses 401.7, which would otherwise be read as 401.
		if x.Kind != yaml.ScalarNode || x.Tag != "!!int" || x.Decode(&code) != nil {
			return fmt.Errorf("line %d: a status must be a number or a list of numbers", x.Line)
		}
		*s = append(*s, code)
	}
	return nil
}

func (s Statuses) has(code int) bool {
	for _, c := range s {
		if c == code {
			return true
		}
	}
	return false
}

// EnvVar is an environment variable a policy reads. Secret marks the ones
// used as credentials (referenced under a persona's headers, cookies or login).
type EnvVar struct {
	Name   string
	Secret bool
}

// A reference is ${env:NAME} (an environment variable) or
// ${account:KEY.FIELD} (a field of a test account stored in a task).
const refBody = `env:[A-Za-z_][A-Za-z0-9_]*|account:[a-z0-9][a-z0-9_.-]*\.(?:username|password|token)`

var (
	nameRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,63}$`)
	refRe    = regexp.MustCompile(`\$\{(` + refBody + `)\}`)
	headerRe = regexp.MustCompile(`^[A-Za-z0-9!#$%&'*+.^_|~-]+$`)
	// A credential value is one or more references, in a header optionally
	// after a scheme word ("Bearer ${env:TOKEN}"). No other literal text.
	onlyRefs  = regexp.MustCompile(`^(?:\$\{(?:` + refBody + `)\})+$`)
	credValue = regexp.MustCompile(`^(?:[A-Za-z][A-Za-z0-9-]* )?(?:\$\{(?:` + refBody + `)\})+$`)
	// Field names of a login request whose value is a credential.
	secretField = regexp.MustCompile(`(?i)pass|secret|token|key|credential|authorization|cookie`)
)

// How a policy string is treated when it is resolved and reported.
const (
	targetString     = iota // target.base_url: shown in the report
	credentialString        // persona headers, cookies and login: always removed from output
	otherString             // anything else: removed from output when long enough to be a key
)

// Limits of one policy. They bound the work and the report of a run.
const (
	maxPersonas  = 20
	maxCases     = 200
	maxRoleRunes = 80
)

// minRedactLen is the length from which a variable used outside a persona is
// removed from the output too. Shorter values are identifiers ("7", "doc-a1");
// removing those would shred every message that contains the same characters.
const minRedactLen = 8

var methods = map[string]bool{"GET": false, "HEAD": false, "OPTIONS": false, "POST": true, "PUT": true, "PATCH": true, "DELETE": true}

// Load reads and validates a policy file.
func Load(path string) (*Policy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse validates a policy without reading the environment or the network.
// Every problem found is reported, one per line.
func Parse(data []byte) (*Policy, error) {
	p, err := decode(data)
	if err != nil {
		return nil, err
	}
	if err := p.validate(); err != nil {
		return nil, err
	}
	return p, nil
}

func decode(data []byte) (*Policy, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true) // a misspelled key in a security policy must not be ignored
	p := &Policy{}
	if err := dec.Decode(p); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("the policy is empty")
		}
		return nil, err
	}
	// Anything after a "---" would be ignored, so it is refused.
	if err := dec.Decode(new(yaml.Node)); !errors.Is(err, io.EOF) {
		return nil, errors.New("the file holds more than one YAML document; a policy is a single document")
	}
	var layout struct {
		Personas yaml.Node `yaml:"personas"`
	}
	if err := yaml.Unmarshal(data, &layout); err != nil {
		return nil, err
	}
	for i := 0; i+1 < len(layout.Personas.Content); i += 2 {
		key, value := layout.Personas.Content[i], layout.Personas.Content[i+1]
		name := key.Value
		per, ok := p.Personas[name]
		// The names read here must be the keys the decoder stored. A key such
		// as null is not, and would turn its persona into an anonymous one.
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || !ok || (per == nil && value.Tag != "!!null") {
			return nil, fmt.Errorf("line %d: a persona name must be plain text; put it in quotes", key.Line)
		}
		p.order = append(p.order, name)
		if per == nil {
			per = &Persona{} // "anonymous:" with no value
			p.Personas[name] = per
		}
		if l := per.Login; l != nil {
			if len(l.Status) == 0 {
				l.Status = Statuses{200}
			}
			if l.Token != nil && l.Token.Header == "" {
				l.Token.Header = "Authorization"
			}
		}
		if per.Identity != nil && len(per.Identity.Status) == 0 {
			per.Identity.Status = Statuses{200}
		}
	}
	p.raw = data
	return p, nil
}

// PersonaNames returns the personas in file order.
func (p *Policy) PersonaNames() []string { return append([]string(nil), p.order...) }

// Account is a test account stored in a task. A policy reads its fields with
// ${account:KEY.username}, ${account:KEY.password} and ${account:KEY.token},
// where KEY is the account's key. Role is the name shown in the output for the
// persona that has the same name as the key.
type Account struct {
	Role, Username, Password, Token string
}

// Ref is one reference of a policy: an environment variable (Env), or a
// field of a test account (Account and Field).
type Ref struct {
	Env, Account, Field string
}

func parseRef(body string) Ref {
	if name, ok := strings.CutPrefix(body, "env:"); ok {
		return Ref{Env: name}
	}
	body = strings.TrimPrefix(body, "account:")
	i := strings.LastIndex(body, ".")
	return Ref{Account: body[:i], Field: body[i+1:]}
}

// Refs lists the distinct references of the policy, in a stable order.
func (p *Policy) Refs() []Ref {
	seen := map[Ref]bool{}
	var out []Ref
	p.eachString(func(s string, _ int) string {
		for _, m := range refRe.FindAllStringSubmatch(s, -1) {
			if r := parseRef(m[1]); !seen[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
		return s
	})
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		return a.Env+"\x00"+a.Account+"\x00"+a.Field < b.Env+"\x00"+b.Account+"\x00"+b.Field
	})
	return out
}

// EnvVars lists the environment variables the policy reads, sorted by name.
func (p *Policy) EnvVars() []EnvVar {
	secret := map[string]bool{}
	p.eachString(func(s string, kind int) string {
		for _, m := range refRe.FindAllStringSubmatch(s, -1) {
			if r := parseRef(m[1]); r.Env != "" {
				secret[r.Env] = secret[r.Env] || kind == credentialString
			}
		}
		return s
	})
	out := make([]EnvVar, 0, len(secret))
	for name, s := range secret {
		out = append(out, EnvVar{Name: name, Secret: s})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// resolve returns a copy of the policy with every reference replaced, and
// the values to keep out of the output. It fails when a reference has no
// value, or when a credential would not be sent exactly as it is stored.
func (p *Policy) resolve(opt Options) (*Policy, []string, error) {
	rp, err := decode(p.raw)
	if err != nil {
		return nil, nil, err
	}
	getenv := opt.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	missingEnv, missingField, unknown, refused, unclean := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	var secrets []string
	rp.eachString(func(s string, kind int) string {
		return refRe.ReplaceAllStringFunc(s, func(text string) string {
			ref := parseRef(refRe.FindStringSubmatch(text)[1])
			var v, label string
			var credential, public bool
			if ref.Env != "" {
				if opt.NoEnv {
					refused[ref.Env] = true
					return ""
				}
				label, v, credential = ref.Env, getenv(ref.Env), kind == credentialString
				if v == "" {
					missingEnv[label] = true
				}
			} else {
				acc, ok := opt.Accounts[ref.Account]
				if !ok {
					unknown[ref.Account] = true
					return ""
				}
				label = ref.Account + "." + ref.Field
				v = map[string]string{"username": acc.Username, "password": acc.Password, "token": acc.Token}[ref.Field]
				// A username identifies the account in the output; it is not a secret.
				credential, public = ref.Field != "username", ref.Field == "username"
				if v == "" {
					missingField[label] = true
				}
			}
			switch {
			case v == "":
			case credential:
				// The HTTP client trims or drops such characters, so the value
				// on the wire would differ from the one removed from the output.
				if v != strings.TrimSpace(v) || strings.ContainsFunc(v, unicode.IsControl) {
					unclean[label] = true
				}
				secrets = append(secrets, v)
			case !public && kind == otherString && len(v) >= minRedactLen:
				secrets = append(secrets, v)
			}
			return v
		})
	})
	switch {
	case len(refused) > 0:
		return nil, nil, fmt.Errorf("a policy that runs inside a task cannot read the server's environment (%s); use the task's test accounts with ${account:KEY.password}", sortedNames(refused))
	case len(unknown) > 0 && opt.Accounts == nil:
		return nil, nil, fmt.Errorf("test accounts (%s) exist only when the policy runs inside a Counterweight task; on the command line use ${env:NAME}", sortedNames(unknown))
	case len(unknown) > 0:
		return nil, nil, fmt.Errorf("test accounts not defined in this task: %s", sortedNames(unknown))
	case len(missingEnv) > 0:
		return nil, nil, fmt.Errorf("environment variables not set: %s", sortedNames(missingEnv))
	case len(missingField) > 0:
		return nil, nil, fmt.Errorf("test account fields not set: %s", sortedNames(missingField))
	case len(unclean) > 0:
		return nil, nil, fmt.Errorf("credentials with leading or trailing whitespace or a control character (often a trailing newline in the stored secret): %s", sortedNames(unclean))
	}
	for _, acc := range opt.Accounts {
		for _, v := range []string{acc.Password, acc.Token} {
			if v != "" {
				secrets = append(secrets, v)
			}
		}
	}
	return rp, secrets, nil
}

func sortedNames(set map[string]bool) string {
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// eachString passes every string that may carry a reference to fn, with its
// kind, and stores what fn returns.
func (p *Policy) eachString(fn func(s string, kind int) string) {
	plain := func(s string) string { return fn(s, otherString) }
	secret := func(s string) string { return fn(s, credentialString) }
	p.Target.BaseURL = fn(p.Target.BaseURL, targetString)
	for _, name := range p.order {
		per := p.Personas[name]
		mapValues(per.Headers, secret)
		mapValues(per.Cookies, secret)
		if per.Login != nil {
			per.Login.Request.eachString(secret)
		}
		if per.Identity != nil {
			per.Identity.Request.eachString(plain)
			sliceValues(per.Identity.BodyContains, plain)
		}
	}
	for _, c := range p.Cases {
		if c == nil {
			continue
		}
		c.Request.eachString(plain)
		for _, e := range c.Expect {
			if e != nil {
				sliceValues(e.BodyContains, plain)
				sliceValues(e.BodyNotContains, plain)
				e.LocationContains = plain(e.LocationContains)
			}
		}
	}
}

func (r *Request) eachString(fn func(string) string) {
	r.Path = fn(r.Path)
	mapValues(r.Headers, fn)
	mapValues(r.Form, fn)
	r.Body = fn(r.Body)
	r.JSON = jsonValues(r.JSON, fn)
}

func mapValues(m map[string]string, fn func(string) string) {
	for k, v := range m {
		m[k] = fn(v)
	}
}

func sliceValues(s []string, fn func(string) string) {
	for i, v := range s {
		s[i] = fn(v)
	}
}

func jsonValues(v any, fn func(string) string) any {
	switch t := v.(type) {
	case string:
		return fn(t)
	case map[string]any:
		for k, x := range t {
			t[k] = jsonValues(x, fn)
		}
	case []any:
		for i, x := range t {
			t[i] = jsonValues(x, fn)
		}
	}
	return v
}

func (p *Policy) validate() error {
	var errs []error
	bad := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	if p.Version != 1 {
		bad("version: must be 1")
	}
	switch {
	case p.Target.BaseURL == "":
		bad("target.base_url: required")
	case !refRe.MatchString(p.Target.BaseURL):
		if _, err := parseBaseURL(p.Target.BaseURL, p.Target.AllowPlainHTTP); err != nil {
			bad("target.base_url: %v", err)
		}
	}
	if p.Target.TimeoutSeconds < 0 || p.Target.TimeoutSeconds > 300 {
		bad("target.timeout_seconds: must be between 1 and 300 (default 15)")
	}

	if strings.Contains(p.Target.BaseURL, "${account:") {
		bad("target.base_url: a field of a test account cannot be part of the address")
	}
	if len(p.order) == 0 {
		bad("personas: define at least one")
	}
	if len(p.order) > maxPersonas {
		bad("personas: at most %d", maxPersonas)
	}
	if len(p.Cases) > maxCases {
		bad("cases: at most %d", maxCases)
	}
	for _, name := range p.order {
		per, at := p.Personas[name], "personas."+name
		if !nameRe.MatchString(name) {
			bad("%s: a persona name uses lowercase letters, digits, '_', '-' and '.'", at)
		}
		// The role is shown in terminals and reports: plain text of a bounded length.
		if utf8.RuneCountInString(per.Role) > maxRoleRunes || strings.ContainsFunc(per.Role, unicode.IsControl) {
			bad("%s.role: at most %d characters, on one line", at, maxRoleRunes)
		}
		for _, kind := range []struct {
			label  string
			values map[string]string
		}{{"headers", per.Headers}, {"cookies", per.Cookies}} {
			for k, v := range kind.values {
				if kind.label == "headers" && !headerRe.MatchString(k) {
					bad("%s.headers: %q is not a header name", at, k)
				}
				if kind.label == "headers" && !credValue.MatchString(v) || kind.label == "cookies" && !onlyRefs.MatchString(v) {
					bad("%s.%s.%s: a credential must come from the environment, never from the policy file: write ${env:NAME}, in a header optionally after a scheme word such as Bearer", at, kind.label, k)
				}
			}
		}
		if l := per.Login; l != nil {
			l.Request.validate(at+".login.request", true, bad)
			literal := func(field string) {
				bad("%s.login.request: %s looks like a credential; it must come from the environment (${env:NAME}), never from the policy file", at, field)
			}
			for k, v := range l.Request.Headers {
				if secretField.MatchString(k) && !credValue.MatchString(v) {
					literal("header " + k)
				}
			}
			for k, v := range l.Request.Form {
				if secretField.MatchString(k) && !onlyRefs.MatchString(v) {
					literal("form field " + k)
				}
			}
			literalJSONSecrets(l.Request.JSON, func(k string) { literal("json field " + k) })
			checkStatuses(at+".login.status", l.Status, bad)
			if t := l.Token; t != nil {
				if !strings.HasPrefix(t.JSONPointer, "/") {
					bad("%s.login.token.json_pointer: must be a JSON pointer such as /access_token", at)
				}
				if !headerRe.MatchString(t.Header) {
					bad("%s.login.token.header: %q is not a header name", at, t.Header)
				}
			}
		}
		switch {
		case !per.credentialed() && per.Identity != nil:
			bad("%s.identity: a persona without credentials is anonymous and takes no identity check", at)
		case per.credentialed() && per.Identity == nil:
			bad("%s.identity: required, so that an expired or misassigned credential stops the run", at)
		case per.Identity != nil:
			id := per.Identity
			id.Request.validate(at+".identity.request", false, bad)
			if id.Request.Method != "GET" && id.Request.Method != "" {
				bad("%s.identity.request.method: must be GET", at)
			}
			checkStatuses(at+".identity.status", id.Status, bad)
			if len(id.BodyContains) == 0 {
				bad("%s.identity.body_contains: required; name a value only this persona's own profile shows", at)
			}
			checkMarkers(at+".identity.body_contains", id.BodyContains, bad)
		}
	}

	if len(p.Cases) == 0 {
		bad("cases: define at least one; an empty matrix proves nothing")
	}
	seen := map[string]bool{}
	for i, c := range p.Cases {
		if c == nil {
			bad("cases[%d]: empty", i)
			continue
		}
		at := fmt.Sprintf("cases[%d]", i)
		if !nameRe.MatchString(c.ID) {
			bad("%s.id: required; lowercase letters, digits, '_', '-' and '.'", at)
		} else {
			at = "case " + c.ID
			if seen[c.ID] {
				bad("%s: the id is used twice", at)
			}
			seen[c.ID] = true
		}
		c.Request.validate(at+": request", false, bad)
		if methods[c.Request.Method] && !c.AllowWrite {
			bad("%s: %s changes data on the target; set allow_write: true to confirm the request is safe to send", at, c.Request.Method)
		}
		allows := 0
		var allowStatuses Statuses
		for _, e := range c.Expect {
			if e != nil && len(e.Deny) == 0 {
				allowStatuses = append(allowStatuses, e.Allow...)
			}
		}
		for _, name := range p.order {
			e := c.Expect[name]
			if e == nil {
				bad("%s: no expectation for persona %s; every case states the outcome for every persona", at, name)
				continue
			}
			where := at + ": expect." + name
			switch {
			case len(e.Allow) > 0 && len(e.Deny) > 0:
				bad("%s: set allow or deny, not both", where)
			case len(e.Allow) == 0 && len(e.Deny) == 0:
				bad("%s: set allow or deny with the expected status", where)
			case len(e.Allow) > 0:
				allows++
				checkStatuses(where+".allow", e.Allow, bad)
			default:
				checkStatuses(where+".deny", e.Deny, bad)
				for _, s := range e.Deny {
					switch {
					case !grantsAccess(s, allowStatuses) || e.evidence():
					case len(e.BodyNotContains) == 0:
						bad("%s: a denial answered with %d cannot be told apart from access (it is a 2xx status, or what an allowed persona of this case gets); add body_contains, location_contains or body_not_contains", where, s)
					case len(allowStatuses) == 0:
						// body_not_contains is proven against an allowed persona's response; here there is none.
						bad("%s: a denial answered with %d cannot be told apart from access by body_not_contains alone when no persona is allowed; add body_contains or location_contains", where, s)
					}
				}
			}
			checkMarkers(where+".body_contains", e.BodyContains, bad)
			checkMarkers(where+".body_not_contains", e.BodyNotContains, bad)
		}
		for name := range c.Expect {
			if _, ok := p.Personas[name]; !ok {
				bad("%s: expect.%s names a persona that is not defined", at, name)
			}
		}
		switch {
		case allows == 0 && !c.DenyAll:
			bad("%s: no persona is allowed, so a broken endpoint would pass every denial; allow the owner, or set deny_all: true if nobody may reach it", at)
		case allows > 0 && c.DenyAll:
			bad("%s: deny_all is set but a persona is allowed", at)
		}
	}

	p.eachString(func(s string, _ int) string {
		if strings.Contains(refRe.ReplaceAllString(s, ""), "${") {
			bad("malformed reference in %q; write ${env:NAME}, or ${account:KEY.password} (also .username and .token) inside a task", s)
		}
		return s
	})
	return errors.Join(errs...)
}

// grantsAccess reports whether a status, seen by a persona that must be
// denied, looks like access: any 2xx, or a status the case's allowed personas get.
func grantsAccess(status int, allowStatuses Statuses) bool {
	return status/100 == 2 || allowStatuses.has(status)
}

// literalJSONSecrets calls found for every field of a login body whose name
// says credential and whose value is written in the policy file.
func literalJSONSecrets(v any, found func(key string)) {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			if s, ok := x.(string); ok && secretField.MatchString(k) && !onlyRefs.MatchString(s) {
				found(k)
			}
			literalJSONSecrets(x, found)
		}
	case []any:
		for _, x := range t {
			literalJSONSecrets(x, found)
		}
	}
}

// validate checks one request. login requests may carry their own
// Authorization header (client credentials); the others take their
// credentials from the persona only.
func (r *Request) validate(at string, login bool, bad func(string, ...any)) {
	if _, ok := methods[r.Method]; !ok {
		bad("%s.method: must be one of GET, HEAD, OPTIONS, POST, PUT, PATCH, DELETE", at)
	}
	if !strings.HasPrefix(r.Path, "/") || strings.HasPrefix(r.Path, "//") || strings.ContainsAny(r.Path, " \t\r\n") {
		bad("%s.path: must be a path on the target that starts with a single /", at)
	}
	bodies := 0
	if r.JSON != nil {
		bodies++
		if _, err := json.Marshal(r.JSON); err != nil {
			bad("%s.json: cannot be written as JSON (use text keys)", at)
		}
	}
	if r.Form != nil {
		bodies++
	}
	if r.Body != "" {
		bodies++
	}
	if bodies > 1 {
		bad("%s: set only one of json, form and body", at)
	}
	for k := range r.Headers {
		switch lk := strings.ToLower(k); {
		case !headerRe.MatchString(k):
			bad("%s.headers: %q is not a header name", at, k)
		case lk == "host", !login && (lk == "authorization" || lk == "cookie"):
			bad("%s.headers.%s: not allowed here; credentials belong to the persona", at, k)
		}
	}
}

func checkStatuses(at string, s Statuses, bad func(string, ...any)) {
	for _, c := range s {
		if c < 100 || c > 599 {
			bad("%s: %d is not an HTTP status", at, c)
		}
	}
}

func checkMarkers(at string, markers []string, bad func(string, ...any)) {
	for _, m := range markers {
		if m == "" {
			bad("%s: an empty text matches everything", at)
		}
	}
}

// parseBaseURL accepts an absolute http(s) URL without credentials, query or fragment.
func parseBaseURL(raw string, allowPlain bool) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, errors.New("must be an absolute URL such as https://staging.example.com")
	}
	switch {
	case u.Scheme == "http" && !allowPlain:
		return nil, errors.New("http:// sends the credentials unencrypted; use https://, or set target.allow_plain_http: true for a local fixture")
	case u.Scheme != "http" && u.Scheme != "https":
		return nil, errors.New("must start with https://")
	// The text is checked too: "https://host?" parses with an empty query, and
	// every path appended to it would then become a query of "/".
	case u.User != nil || strings.ContainsAny(raw, "?#"):
		return nil, errors.New("must not carry credentials, a query or a fragment")
	// "host." and "host" are the same name, but a rule written for one would
	// not match the other.
	case strings.HasSuffix(u.Hostname(), "."):
		return nil, errors.New("the host name must not end with a dot")
	}
	return u, nil
}
