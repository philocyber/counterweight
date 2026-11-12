package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"

	"github.com/Philocyber/counterweight/agent"
	"github.com/Philocyber/counterweight/authz"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/locale"
	"github.com/Philocyber/counterweight/report"
)

// request_as_role lets the agents of a task test authentication and
// authorization with the task's stored test accounts without ever holding
// their credentials. An agent names a role and a request; the server signs in
// with the stored account (package authz, Broker), sends the request to the
// one target of the task's policy and returns the answer with the credentials
// removed.
//
// What bounds it:
//   - it exists only in a task that has an account the operator enabled for
//     agents, and only for the personas whose accounts are all enabled;
//   - the agent chooses a method, a path and a body, never the host and never
//     an Authorization or Cookie header;
//   - every request passes the operation review (approval gate and tool
//     rules) with its real destination, and the blocked-target rules of the
//     server and of the task, like the native HTTP tools;
//   - the credentials stay in this process: they are in no prompt, no tool
//     input, no tool result and no captured traffic.

const (
	authzAgentToolName = "request_as_role"
	// authzBrokerTTL is how long a signed-in broker of a task is kept. After
	// it the accounts sign in again, so a session that expired on the target
	// recovers without anybody restarting anything.
	authzBrokerTTL = 10 * time.Minute
)

// authzAgentToolAgents are the agents the tool is bound to by default.
var authzAgentToolAgents = []string{"worker", "pentest"}

type authzBrokerEntry struct {
	broker      *authz.Broker
	allowed     map[string]bool // personas an agent may act as
	fingerprint string          // of the policy and the accounts it was built from
}

// authzBrokers holds one broker per task with agent access, per server.
var authzBrokers = struct {
	sync.Mutex
	m map[authzRunKey]*authzBrokerEntry
}{m: map[authzRunKey]*authzBrokerEntry{}}

// authzOptions are the options of a run or a broker inside a task: the
// task's accounts, no server environment, and the blocked-target rules.
func (s *Server) authzOptions(lang locale.Lang, taskID int64, secrets map[string]db.AuthzAccountSecret) authz.Options {
	opt := authz.Options{
		Accounts: make(map[string]authz.Account, len(secrets)),
		NoEnv:    true,
		Redact:   report.RedactSecrets,
		CheckURL: func(u *url.URL) error { return s.authzDestination(lang, taskID, u) },
		CheckPeer: func(ip net.IP) error {
			if s.m == nil || s.m.Assets() == nil {
				return locale.NewError("The blocked-target rules are not available, so the run cannot be checked against them")
			}
			return s.m.Assets().CheckHTTPPeer(lang, taskID, ip)
		},
	}
	for k, a := range secrets {
		opt.Accounts[k] = authz.Account{Role: a.Role, Username: a.Username, Password: a.Password, Token: a.Token}
	}
	return opt
}

// authzBrokerBuild holds one mutex per task: the state of a task is read and
// its broker built by one call at a time, without making other tasks wait.
var authzBrokerBuild sync.Map // authzRunKey -> *sync.Mutex

// dropAuthzBroker revokes the broker of a task, if it has one. The handlers
// that change a task's accounts or policy call it, so the change does not
// wait for the agent's next request to take effect.
func (s *Server) dropAuthzBroker(taskID int64) {
	key := authzRunKey{s, taskID}
	authzBrokers.Lock()
	defer authzBrokers.Unlock()
	if e := authzBrokers.m[key]; e != nil {
		e.broker.Close()
		delete(authzBrokers.m, key)
	}
}

// authzBroker returns the broker of a task, building it when the policy or
// the accounts changed or the previous one expired. It fails when the task
// has no account enabled for agents or its policy is not ready to run.
func (s *Server) authzBroker(lang locale.Lang, taskID int64) (*authzBrokerEntry, error) {
	key := authzRunKey{s, taskID}
	build, _ := authzBrokerBuild.LoadOrStore(key, &sync.Mutex{})
	build.(*sync.Mutex).Lock()
	defer build.(*sync.Mutex).Unlock()

	pg := s.m.pg
	accounts, err := pg.ListAuthzAccounts(taskID)
	if err != nil {
		return nil, locale.NewError("The authorization check data could not be read or saved")
	}
	policy, updatedAt, err := pg.AuthzPolicy(taskID)
	if err != nil {
		return nil, locale.NewError("The authorization check data could not be read or saved")
	}
	sum := sha256.New()
	sum.Write([]byte(policy))
	enabled := false
	for _, a := range accounts {
		fmt.Fprintf(sum, "|%d %s %v %d", a.ID, a.Key, a.AgentAccess, a.UpdatedAt.UnixNano())
		enabled = enabled || a.AgentAccess
	}
	fingerprint := hex.EncodeToString(sum.Sum(nil))
	if !enabled {
		s.dropAuthzBroker(taskID)
		return nil, locale.NewError("No test account of this task is enabled for agents")
	}
	if updatedAt == nil {
		s.dropAuthzBroker(taskID)
		return nil, locale.NewError("This task has no authorization policy yet; the operator has to save one before roles can be used")
	}
	authzBrokers.Lock()
	current := authzBrokers.m[key]
	authzBrokers.Unlock()
	if current != nil && current.fingerprint == fingerprint {
		return current, nil
	}
	s.dropAuthzBroker(taskID)
	p, check := s.checkAuthzPolicy(lang, taskID, policy, accounts)
	if p == nil {
		problems := check.Errors
		if len(problems) > 3 {
			problems = problems[:3]
		}
		return nil, locale.Errorf("The task's authorization policy has problems the operator has to fix: %s", strings.Join(problems, "; "))
	}
	secrets, err := pg.AuthzAccountSecrets(taskID)
	if err != nil {
		return nil, locale.NewError("The authorization check data could not be read or saved")
	}
	broker, err := authz.NewBroker(p, s.authzOptions(lang, taskID, secrets))
	if err != nil {
		return nil, err
	}
	e := &authzBrokerEntry{broker: broker, allowed: map[string]bool{}, fingerprint: fingerprint}
	for _, name := range broker.Personas() {
		ok := true
		for _, account := range broker.PersonaAccounts(name) {
			ok = ok && secrets[account].AgentAccess
		}
		e.allowed[name] = ok
	}
	authzBrokers.Lock()
	authzBrokers.m[key] = e
	authzBrokers.Unlock()
	// A broker holds opened credentials: it is dropped when its time is up,
	// whether or not anybody calls the tool again.
	time.AfterFunc(authzBrokerTTL, func() {
		authzBrokers.Lock()
		defer authzBrokers.Unlock()
		if authzBrokers.m[key] == e {
			e.broker.Close()
			delete(authzBrokers.m, key)
		}
	})
	return e, nil
}

// roles lists the personas an agent may act as, in policy order.
func (e *authzBrokerEntry) roles() []map[string]any {
	var out []map[string]any
	for _, name := range e.broker.Personas() {
		if !e.allowed[name] {
			continue
		}
		role := map[string]any{"role": name, "signed_in": len(e.broker.PersonaAccounts(name)) > 0}
		if label := e.broker.Role(name); label != "" {
			role["name"] = label
		}
		out = append(out, role)
	}
	return out
}

func (e *authzBrokerEntry) roleNames() string {
	var names []string
	for name, ok := range e.allowed {
		if ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// authzAgentToolMeta is the model-facing description and schema of the tool.
func authzAgentToolMeta(lang locale.Lang) (string, map[string]any) {
	desc := locale.Text(lang, "Send one HTTP request to the application under test as one of this task's stored test roles, to test authentication and authorization (access control, IDOR, privilege escalation) without handling credentials. The server signs in with the stored account and adds the session; you never receive the password, token or cookie, and they are removed from the answer.\n") +
		locale.Text(lang, "Call it with no method and no path to list the available roles and the target address.\n") +
		locale.Text(lang, "To send a request give role, method and path (a path on the target that starts with /), optionally headers, and at most one of json, form or body. You cannot choose another host and cannot set Authorization, Cookie or Host headers. Redirects are not followed: a 3xx answer is returned with its Location.\n") +
		locale.Text(lang, "Send the same request as different roles to compare what each one gets. These requests are not stored as captured traffic, so quote the returned status and body as evidence when you report a finding.")
	return desc, objSchema(map[string]any{
		"role":    strParam(locale.Text(lang, "Role key from the role list, for example anonymous or admin_tenant_a")),
		"method":  strParam(locale.Text(lang, "GET, HEAD, OPTIONS, POST, PUT, PATCH or DELETE")),
		"path":    strParam(locale.Text(lang, "Path on the target, starting with /; may carry a query string")),
		"headers": map[string]any{"type": "object", "description": locale.Text(lang, "Extra request headers, as an object of text values")},
		"json":    map[string]any{"description": locale.Text(lang, "Request body sent as JSON")},
		"form":    map[string]any{"type": "object", "description": locale.Text(lang, "Request body sent as a URL-encoded form, as an object of text values")},
		"body":    strParam(locale.Text(lang, "Request body sent as written")),
	})
}

type authzOperationTool struct{ actool.CoreTool }

// RequiresOperationReview makes the runtime review the resolved request (with
// its real destination) instead of the raw parameters.
func (*authzOperationTool) RequiresOperationReview() bool { return true }

// requestAsRole handles one call of the tool. The operation review must be
// bound by the caller, as the tool's Run does; called directly, the request is
// sent without that review.
func (s *Server) requestAsRole(ctx context.Context, in json.RawMessage, tc *actool.ToolContext) (actool.Result, error) {
	lang := locale.FromContext(ctx)
	fail := func(err error) (actool.Result, error) {
		return actool.Errorf(locale.ErrorMessage(lang, err)), nil
	}
	taskID := agent.RunInfoFrom(ctx).TaskID
	if taskID <= 0 || s.m == nil || s.m.pg == nil {
		return fail(locale.NewError("request_as_role works inside a task only"))
	}
	var a struct {
		Role    string            `json:"role"`
		Method  string            `json:"method"`
		Path    string            `json:"path"`
		Headers map[string]string `json:"headers"`
		JSON    any               `json:"json"`
		Form    map[string]string `json:"form"`
		Body    string            `json:"body"`
	}
	if len(in) > 0 {
		if err := json.Unmarshal(in, &a); err != nil {
			return fail(locale.NewError("The parameters of request_as_role are not valid; headers and form take text values"))
		}
	}
	entry, err := s.authzBroker(lang, taskID)
	if err != nil {
		return fail(err)
	}
	if a.Method == "" && a.Path == "" {
		return jsonResult(map[string]any{"target": entry.broker.Target(), "roles": entry.roles()})
	}
	if !entry.allowed[a.Role] {
		return fail(locale.Errorf("Role %s is not available to agents in this task. Available roles: %s", a.Role, entry.roleNames()))
	}
	q := authz.Request{Method: strings.ToUpper(strings.TrimSpace(a.Method)), Path: a.Path, Headers: a.Headers, JSON: a.JSON, Form: a.Form, Body: a.Body}
	target, err := entry.broker.URL(q.Path)
	if err != nil {
		return fail(err)
	}
	body := q.Body
	switch {
	case q.JSON != nil:
		raw, _ := json.Marshal(q.JSON)
		body = string(raw)
	case q.Form != nil:
		form := url.Values{}
		for k, v := range q.Form {
			form.Set(k, v)
		}
		body = form.Encode()
	}
	// The gate sees the request as it will be sent, minus the credentials,
	// which this process adds afterwards and nobody reviews or records.
	if err := agent.ReviewOperation(ctx, "WebFetch", map[string]any{
		"kind": "http", "method": q.Method, "url": target, "headers": q.Headers, "body": body,
		"as_role": a.Role, "proxy": "", "use_recording_proxy": false,
	}, tc); err != nil {
		return fail(err)
	}
	// The review can wait for a person. If the operator changed the accounts
	// or the policy meanwhile, the approval was for a state that is gone.
	if current, err := s.authzBroker(lang, taskID); err != nil || current != entry || !current.allowed[a.Role] {
		return fail(authz.ErrBrokerClosed)
	}
	answer, err := entry.broker.Do(ctx, a.Role, q)
	if err != nil {
		return fail(locale.Errorf("Request failed: %w", err))
	}
	out, _ := json.Marshal(map[string]any{
		"role": a.Role, "status": answer.Status, "location": answer.Location, "content_type": answer.ContentType,
		"body": answer.Body, "truncated": answer.Truncated,
	})
	return actool.Text(actool.Capture(tc, string(out))), nil
}

func (s *Server) authzAgentTool(lang locale.Lang) actool.CoreTool {
	desc, schema := authzAgentToolMeta(lang)
	return &authzOperationTool{CoreTool: actool.Build(actool.Spec{
		Name: authzAgentToolName, Description: desc, Schema: schema,
		Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
			return permission.Allowed()
		},
		// Outside an agent session there is no review to bind, and the call is refused.
		Run: func(ctx context.Context, in json.RawMessage, tc *actool.ToolContext) (actool.Result, error) {
			return s.requestAsRole(agent.RequireOperationReview(ctx, authzAgentToolName), in, tc)
		},
	})}
}

// authzAgentTools returns the tool for a run whose task has an account the
// operator enabled for agents, and nothing otherwise.
func (s *Server) authzAgentTools(ctx context.Context) []actool.CoreTool {
	taskID := agent.RunInfoFrom(ctx).TaskID
	if taskID <= 0 || s.m == nil || s.m.pg == nil {
		return nil
	}
	if ok, err := s.m.pg.AuthzAgentAccess(taskID); err != nil || !ok {
		return nil
	}
	return []actool.CoreTool{s.authzAgentTool(locale.FromContext(ctx))}
}
