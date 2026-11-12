package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Philocyber/counterweight/authz"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/locale"
	"github.com/Philocyber/counterweight/report"
)

// Authorization check of a task, from the console: the operator stores test
// accounts and a policy on the task, starts a run, and reads the result. The
// run is the deterministic runner of package authz; no model takes part.
//
// What keeps the stored credentials where they belong:
//   - they are sealed at rest and no endpoint returns them (db/task_authz.go);
//   - a policy inside a task cannot read the server's environment, only the
//     task's own accounts;
//   - the runner sends them to the policy's one target, through the same
//     blocked-target rules as the native HTTP tools, never through the
//     recording proxy;
//   - the stored report holds none of them (authz redacts, then the report
//     redaction is applied on top).
//
// These endpoints need a console session. An API token is refused on them
// like on every endpoint that automationScope does not name.

// authzRunLimit bounds one run, however many cases the policy has.
const authzRunLimit = 30 * time.Minute

// authzInFlight holds the tasks with a run in progress, per server.
var authzInFlight sync.Map // authzRunKey -> struct{}

type authzRunKey struct {
	s      *Server
	taskID int64
}

// authzCheck is what the console shows about a stored policy before a run.
type authzCheck struct {
	OK       bool     `json:"ok"`
	Errors   []string `json:"errors"`
	Personas []string `json:"personas"`
	Cases    int      `json:"cases"`
	Checks   int      `json:"checks"`
}

func (s *Server) registerTaskAuthzRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/tasks/{id}/authz", s.getTaskAuthz)
	mux.HandleFunc("POST /api/tasks/{id}/authz/accounts", s.createTaskAuthzAccount)
	mux.HandleFunc("PATCH /api/tasks/{id}/authz/accounts/{aid}", s.updateTaskAuthzAccount)
	mux.HandleFunc("DELETE /api/tasks/{id}/authz/accounts/{aid}", s.deleteTaskAuthzAccount)
	mux.HandleFunc("PUT /api/tasks/{id}/authz/policy", s.putTaskAuthzPolicy)
	mux.HandleFunc("POST /api/tasks/{id}/authz/runs", s.startTaskAuthzRun)
	mux.HandleFunc("GET /api/tasks/{id}/authz/runs/{rid}", s.getTaskAuthzRun)
}

// authzTask resolves the task of a request. It writes the error response and
// returns ok=false when the task does not exist or is a code review, which has
// no running application to check.
func (s *Server) authzTask(w http.ResponseWriter, r *http.Request) (pg *db.DB, taskID int64, ok bool) {
	if pg = s.pg(w); pg == nil {
		return nil, 0, false
	}
	taskID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || taskID <= 0 {
		writeError(w, http.StatusNotFound, db.ErrAuthzTaskNotFound)
		return nil, 0, false
	}
	task, err := pg.GetTask(taskID)
	if err != nil || task == nil {
		writeError(w, http.StatusNotFound, db.ErrAuthzTaskNotFound)
		return nil, 0, false
	}
	if task.AssessmentType == db.AssessmentCodeReview {
		writeErr(w, http.StatusBadRequest, locale.Text(responseLanguage(w), "An authorization check tests a running application; a code review task has none"))
		return nil, 0, false
	}
	return pg, taskID, true
}

func writeAuthzError(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	switch {
	case errors.Is(err, db.ErrAuthzTaskNotFound), errors.Is(err, db.ErrAuthzAccountNotFound), errors.Is(err, db.ErrAuthzRunNotFound):
		code = http.StatusNotFound
	case errors.Is(err, db.ErrAuthzAccountKey), errors.Is(err, db.ErrAuthzAccountLimit):
		code = http.StatusConflict
	case errors.Is(err, db.ErrAuthzAccountRole), errors.Is(err, db.ErrAuthzAccountField), errors.Is(err, db.ErrAuthzAccountSecret):
		code = http.StatusBadRequest
	case errors.Is(err, db.ErrAuthzPolicySize):
		code = http.StatusRequestEntityTooLarge
	}
	if code == http.StatusInternalServerError {
		// A driver error may quote a statement; the console gets a plain message.
		log.Printf("[authz] request failed: %v", err)
		writeErr(w, code, locale.Text(responseLanguage(w), "The authorization check data could not be read or saved"))
		return
	}
	writeError(w, code, err)
}

// checkAuthzPolicy validates a stored policy for a run inside the task: the
// policy itself, every reference against the task's accounts, and the target
// against the blocked-target rules. It returns the parsed policy when it is
// ready to run.
func (s *Server) checkAuthzPolicy(lang locale.Lang, taskID int64, text string, accounts []*db.AuthzAccount) (*authz.Policy, authzCheck) {
	check := authzCheck{Errors: []string{}, Personas: []string{}}
	p, err := authz.Parse([]byte(text))
	if err != nil {
		check.Errors = strings.Split(err.Error(), "\n")
		return nil, check
	}
	check.Personas = p.PersonaNames()
	check.Cases = len(p.Cases)
	check.Checks = len(check.Personas) * check.Cases

	byKey := map[string]*db.AuthzAccount{}
	for _, a := range accounts {
		byKey[a.Key] = a
	}
	for _, ref := range p.Refs() {
		a := byKey[ref.Account]
		switch {
		case ref.Env != "":
			check.Errors = append(check.Errors, locale.Text(lang, "${env:%s}: a policy inside a task cannot read the server's environment; store the value in a test account and write ${account:KEY.password}", ref.Env))
		case a == nil:
			check.Errors = append(check.Errors, locale.Text(lang, "${account:%s.%s}: this task has no test account with the key %s", ref.Account, ref.Field, ref.Account))
		case ref.Field == "username" && a.Username == "", ref.Field == "password" && !a.HasPassword, ref.Field == "token" && !a.HasToken:
			check.Errors = append(check.Errors, locale.Text(lang, "${account:%s.%s}: the test account %s has no %s", ref.Account, ref.Field, a.Role, ref.Field))
		}
	}
	if base, err := url.Parse(p.Target.BaseURL); err == nil {
		if err := s.authzDestination(lang, taskID, base); err != nil {
			check.Errors = append(check.Errors, "target.base_url: "+locale.ErrorMessage(lang, err))
		}
	}
	check.OK = len(check.Errors) == 0
	if !check.OK {
		return nil, check
	}
	return p, check
}

// authzDestination applies the blocked-target rules of the server and of the
// task to a destination. Without the rule store nothing is allowed.
func (s *Server) authzDestination(lang locale.Lang, taskID int64, u *url.URL) error {
	if s.m == nil || s.m.Assets() == nil {
		return locale.NewError("The blocked-target rules are not available, so the run cannot be checked against them")
	}
	return s.m.Assets().CheckHTTPDestination(lang, taskID, u)
}

func (s *Server) getTaskAuthz(w http.ResponseWriter, r *http.Request) {
	pg, taskID, ok := s.authzTask(w, r)
	if !ok {
		return
	}
	accounts, err := pg.ListAuthzAccounts(taskID)
	if err != nil {
		writeAuthzError(w, err)
		return
	}
	policy, updatedAt, err := pg.AuthzPolicy(taskID)
	if err != nil {
		writeAuthzError(w, err)
		return
	}
	runs, err := pg.ListAuthzRuns(taskID)
	if err != nil {
		writeAuthzError(w, err)
		return
	}
	out := map[string]any{"accounts": accounts, "policy": policy, "policy_updated_at": updatedAt, "check": nil, "document": nil, "runs": runs}
	if updatedAt != nil {
		_, check := s.checkAuthzPolicy(responseLanguage(w), taskID, policy, accounts)
		out["check"] = check
		out["document"] = authzDocument(policy)
	}
	_, out["running"] = authzInFlight.Load(authzRunKey{s, taskID})
	writeJSON(w, http.StatusOK, out)
}

// authzAccountBody is the body of a create or an update. A missing field is
// nil, so "keep the stored password" and "remove it" stay different.
type authzAccountBody struct {
	Role        *string `json:"role"`
	Username    *string `json:"username"`
	Password    *string `json:"password"`
	Token       *string `json:"token"`
	AgentAccess *bool   `json:"agent_access"`
}

func readAuthzAccount(w http.ResponseWriter, r *http.Request) (db.AuthzAccountInput, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var body authzAccountBody
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		// The decoder's message can quote the body, which may hold a password.
		writeErr(w, http.StatusBadRequest, locale.Text(responseLanguage(w), "The request is not a valid test account"))
		return db.AuthzAccountInput{}, false
	}
	return db.AuthzAccountInput{Role: body.Role, Username: body.Username, Password: body.Password, Token: body.Token, AgentAccess: body.AgentAccess}, true
}

// newTaskAuthzAccounts reads the test accounts sent with a new task and
// validates them before the task exists, so a refused account is answered
// with its own message. They are stored in the transaction that creates the
// task (db.TaskCreateOptions.AuthzAccounts).
func newTaskAuthzAccounts(bodies []authzAccountBody) ([]db.AuthzAccountInput, error) {
	accounts := make([]db.AuthzAccountInput, 0, len(bodies))
	for _, b := range bodies {
		accounts = append(accounts, db.AuthzAccountInput{Role: b.Role, Username: b.Username, Password: b.Password, Token: b.Token, AgentAccess: b.AgentAccess})
	}
	return accounts, db.CheckAuthzAccounts(accounts)
}

func (s *Server) createTaskAuthzAccount(w http.ResponseWriter, r *http.Request) {
	pg, taskID, ok := s.authzTask(w, r)
	if !ok {
		return
	}
	in, ok := readAuthzAccount(w, r)
	if !ok {
		return
	}
	account, err := pg.CreateAuthzAccount(taskID, in)
	if err != nil {
		writeAuthzError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, account)
}

func (s *Server) updateTaskAuthzAccount(w http.ResponseWriter, r *http.Request) {
	pg, taskID, ok := s.authzTask(w, r)
	if !ok {
		return
	}
	accountID, err := strconv.ParseInt(r.PathValue("aid"), 10, 64)
	if err != nil {
		writeAuthzError(w, db.ErrAuthzAccountNotFound)
		return
	}
	in, ok := readAuthzAccount(w, r)
	if !ok {
		return
	}
	account, err := pg.UpdateAuthzAccount(taskID, accountID, in)
	// Whatever changed, the agents' signed-in sessions of this task end now.
	s.dropAuthzBroker(taskID)
	if err != nil {
		writeAuthzError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, account)
}

func (s *Server) deleteTaskAuthzAccount(w http.ResponseWriter, r *http.Request) {
	pg, taskID, ok := s.authzTask(w, r)
	if !ok {
		return
	}
	accountID, err := strconv.ParseInt(r.PathValue("aid"), 10, 64)
	if err != nil {
		writeAuthzError(w, db.ErrAuthzAccountNotFound)
		return
	}
	err = pg.DeleteAuthzAccount(taskID, accountID)
	s.dropAuthzBroker(taskID)
	if err != nil {
		writeAuthzError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) putTaskAuthzPolicy(w http.ResponseWriter, r *http.Request) {
	pg, taskID, ok := s.authzTask(w, r)
	if !ok {
		return
	}
	// JSON escaping can double the text; the database layer enforces the real limit.
	r.Body = http.MaxBytesReader(w, r.Body, 2*db.MaxAuthzPolicyBytes+4096)
	var body struct {
		Policy *string `json:"policy"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Policy == nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeAuthzError(w, db.ErrAuthzPolicySize)
			return
		}
		writeErr(w, http.StatusBadRequest, locale.Text(responseLanguage(w), "The request needs the policy text"))
		return
	}
	updatedAt, err := pg.SaveAuthzPolicy(taskID, *body.Policy)
	s.dropAuthzBroker(taskID)
	if err != nil {
		writeAuthzError(w, err)
		return
	}
	accounts, err := pg.ListAuthzAccounts(taskID)
	if err != nil {
		writeAuthzError(w, err)
		return
	}
	_, check := s.checkAuthzPolicy(responseLanguage(w), taskID, *body.Policy, accounts)
	writeJSON(w, http.StatusOK, map[string]any{"policy_updated_at": updatedAt, "check": check, "document": authzDocument(*body.Policy)})
}

// authzDocument is the policy as data for the console's matrix editor, or nil
// when the text can only be edited as text.
func authzDocument(policy string) *authz.Document {
	doc, ok := authz.Outline([]byte(policy))
	if !ok {
		return nil
	}
	return doc
}

func (s *Server) startTaskAuthzRun(w http.ResponseWriter, r *http.Request) {
	pg, taskID, ok := s.authzTask(w, r)
	if !ok {
		return
	}
	lang := responseLanguage(w)
	policy, updatedAt, err := pg.AuthzPolicy(taskID)
	if err != nil {
		writeAuthzError(w, err)
		return
	}
	if updatedAt == nil {
		writeErr(w, http.StatusBadRequest, locale.Text(lang, "Save a policy before starting a run"))
		return
	}
	accounts, err := pg.ListAuthzAccounts(taskID)
	if err != nil {
		writeAuthzError(w, err)
		return
	}
	p, check := s.checkAuthzPolicy(lang, taskID, policy, accounts)
	if p == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": locale.Text(lang, "The policy has problems; fix them before starting a run"), "check": check})
		return
	}
	key := authzRunKey{s, taskID}
	if _, busy := authzInFlight.LoadOrStore(key, struct{}{}); busy {
		writeErr(w, http.StatusConflict, locale.Text(lang, "An authorization run is already in progress for this task"))
		return
	}
	secrets, err := pg.AuthzAccountSecrets(taskID)
	if err != nil {
		authzInFlight.Delete(key)
		writeAuthzError(w, err)
		return
	}
	opt := s.authzOptions(lang, taskID, secrets)
	go func() {
		defer authzInFlight.Delete(key)
		ctx, cancel := context.WithTimeout(context.Background(), authzRunLimit)
		defer cancel()
		rep := authz.Run(ctx, p, opt)
		b, err := json.Marshal(rep)
		if err == nil {
			_, err = pg.AddAuthzRun(taskID, rep.Result, rep.ExitCode, rep.StartedAt, b)
		}
		if err != nil {
			// Typically the task was deleted while the run was in progress.
			log.Printf("[authz] task %d: the run could not be stored: %v", taskID, err)
		}
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"running": true})
}

// attachAuthzSection adds the latest stored run of the task's authorization
// check to a report input. The stored report holds no credential.
func (s *Server) attachAuthzSection(in *report.Input, t *Task) {
	if in.CodeReview != nil || s.m == nil || s.m.pg == nil {
		return
	}
	id, err := strconv.ParseInt(t.ID, 10, 64)
	if err != nil {
		return
	}
	run, err := s.m.pg.LatestAuthzRun(id)
	if err != nil || run == nil {
		return
	}
	var rep authz.Report
	if json.Unmarshal(run.Report, &rep) != nil {
		return
	}
	in.Authz = &rep
}

func (s *Server) getTaskAuthzRun(w http.ResponseWriter, r *http.Request) {
	pg, taskID, ok := s.authzTask(w, r)
	if !ok {
		return
	}
	runID, err := strconv.ParseInt(r.PathValue("rid"), 10, 64)
	if err != nil {
		writeAuthzError(w, db.ErrAuthzRunNotFound)
		return
	}
	run, err := pg.AuthzRun(taskID, runID)
	if err != nil {
		writeAuthzError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}
