package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/jira"
	"github.com/Philocyber/counterweight/locale"
	"github.com/Philocyber/counterweight/privatefs"
	"github.com/Philocyber/counterweight/report"
)

// Jira Cloud integration: AppSec teams file findings as Jira issues, one epic per
// task with each finding as a child issue, and Counterweight keeps those issues
// updated as fixes are proposed, merged and re-checked. The Atlassian API token
// is kept in a private credential file, like the GitHub token: never in the
// database, the model context or logs. Only server code calls Jira.

var errJiraMissing = locale.NewError("Jira is not connected; add a Jira site, email and API token under Settings")

// jiraExportMu serializes exports so one finding or task never gets two issues
// from concurrent clicks. Exports are rare, so a single lock is enough.
var jiraExportMu sync.Mutex

// jiraSyncEvery is how often finding changes are pushed to linked issues.
var jiraSyncEvery = 5 * time.Minute

type jiraCredential struct {
	Site        string `json:"site"`
	Email       string `json:"email"`
	Token       string `json:"token"`
	AccountID   string `json:"account_id"`
	DisplayName string `json:"display_name"`
	SavedAt     string `json:"saved_at"`
}

// jiraConfig is the non-secret issue configuration, stored in settings.
type jiraConfig struct {
	// ProjectKey and IssueTypeID are the defaults offered when exporting; each
	// export may choose another Jira space (project) and issue type.
	ProjectKey  string `json:"project_key"`
	IssueTypeID string `json:"issue_type_id"`
	EpicTypeID  string `json:"epic_type_id"` // empty: the space's epic-level type
	// Priorities maps a severity (critical, high, medium, low, info) to a Jira priority id.
	Priorities map[string]string `json:"priorities"`
	// SLADays maps a severity to the days allowed for the fix; 0 sets no due date.
	SLADays         map[string]int `json:"sla_days"`
	Labels          []string       `json:"labels"`
	SecurityLevelID string         `json:"security_level_id"`
	// Sync comments on and transitions linked issues as findings change.
	Sync bool `json:"sync"`
}

// jiraStatus is what the UI may see; the token itself is never returned.
type jiraStatus struct {
	Configured bool       `json:"configured"`
	Site       string     `json:"site,omitempty"`
	Email      string     `json:"email,omitempty"`
	Account    string     `json:"account,omitempty"`
	Hint       string     `json:"hint,omitempty"`
	Config     jiraConfig `json:"config"`
}

const settingJiraConfig = "jira.config"

var jiraSeverities = []string{"critical", "high", "medium", "low", "info"}

func defaultJiraConfig() jiraConfig {
	return jiraConfig{
		Priorities: map[string]string{},
		SLADays:    map[string]int{"critical": 7, "high": 30, "medium": 90, "low": 180},
		Labels:     []string{},
		Sync:       true,
	}
}

func (s *Server) jiraCredentialPath() string { return filepath.Join(s.m.dir, jiraCredentialFile) }

func (s *Server) loadJiraCredential() (*jiraCredential, error) {
	raw, err := privatefs.ReadFile(s.jiraCredentialPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, errJiraMissing
	}
	if err != nil {
		return nil, err
	}
	var c jiraCredential
	if err := json.Unmarshal(raw, &c); err != nil || c.Token == "" || c.Site == "" {
		return nil, errJiraMissing
	}
	return &c, nil
}

func (s *Server) jiraClient() (*jira.Client, *jiraCredential, error) {
	cred, err := s.loadJiraCredential()
	if err != nil {
		return nil, nil, err
	}
	c, err := jira.New(jira.Config{Site: cred.Site, Email: cred.Email, Token: cred.Token})
	return c, cred, err
}

func (s *Server) loadJiraConfig() jiraConfig {
	cfg := defaultJiraConfig()
	if raw, ok, _ := s.m.pg.GetSetting(settingJiraConfig); ok && raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg)
	}
	if cfg.Priorities == nil {
		cfg.Priorities = map[string]string{}
	}
	if cfg.SLADays == nil {
		cfg.SLADays = map[string]int{}
	}
	if cfg.Labels == nil {
		cfg.Labels = []string{}
	}
	return cfg
}

var jiraKeyRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,49}$`)
var jiraIDRe = regexp.MustCompile(`^[0-9]{1,20}$`)

// cleanJiraLabel turns free text into a Jira label (labels cannot hold spaces).
func cleanJiraLabel(s string) string {
	s = strings.ToLower(strings.Join(strings.Fields(s), "-"))
	if r := []rune(s); len(r) > 64 {
		s = string(r[:64])
	}
	return s
}

func validateJiraConfig(cfg jiraConfig) (jiraConfig, error) {
	cfg.ProjectKey = strings.ToUpper(strings.TrimSpace(cfg.ProjectKey))
	if cfg.ProjectKey != "" && !jiraKeyRe.MatchString(cfg.ProjectKey) {
		return cfg, locale.NewError("Choose a valid Jira project")
	}
	for _, id := range []string{cfg.IssueTypeID, cfg.EpicTypeID, cfg.SecurityLevelID} {
		if id != "" && !jiraIDRe.MatchString(id) {
			return cfg, locale.NewError("Invalid Jira identifier")
		}
	}
	priorities := map[string]string{}
	sla := map[string]int{}
	for _, sev := range jiraSeverities {
		if id := strings.TrimSpace(cfg.Priorities[sev]); id != "" {
			if !jiraIDRe.MatchString(id) {
				return cfg, locale.NewError("Invalid Jira priority")
			}
			priorities[sev] = id
		}
		if d := cfg.SLADays[sev]; d < 0 || d > 3650 {
			return cfg, locale.NewError("Remediation targets must be between 0 and 3650 days")
		} else if d > 0 {
			sla[sev] = d
		}
	}
	cfg.Priorities, cfg.SLADays = priorities, sla
	labels := []string{}
	for _, l := range cfg.Labels {
		if l = cleanJiraLabel(l); l != "" && len(labels) < 10 {
			labels = append(labels, l)
		}
	}
	cfg.Labels = labels
	return cfg, nil
}

func (s *Server) jiraStatusOrEmpty() jiraStatus {
	st := jiraStatus{Config: s.loadJiraConfig()}
	cred, err := s.loadJiraCredential()
	if err != nil {
		return st
	}
	st.Configured, st.Site, st.Email, st.Account = true, cred.Site, cred.Email, cred.DisplayName
	if n := len(cred.Token); n > 4 {
		st.Hint = "…" + cred.Token[n-4:]
	}
	return st
}

func (s *Server) registerJiraRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/integrations/jira", s.getJiraHandler)
	mux.HandleFunc("PUT /api/integrations/jira/credentials", s.putJiraCredentialsHandler)
	mux.HandleFunc("DELETE /api/integrations/jira/credentials", s.deleteJiraCredentialsHandler)
	mux.HandleFunc("PUT /api/integrations/jira/config", s.putJiraConfigHandler)
	mux.HandleFunc("GET /api/integrations/jira/projects", s.jiraProjectsHandler)
	mux.HandleFunc("GET /api/integrations/jira/projects/{key}/meta", s.jiraProjectMetaHandler)
	mux.HandleFunc("POST /api/integrations/jira/findings/{id}/export", s.jiraExportFindingHandler)
	mux.HandleFunc("POST /api/integrations/jira/tasks/{id}/export", s.jiraExportTaskHandler)
	mux.HandleFunc("GET /api/integrations/jira/tasks/{id}/links", s.jiraTaskLinksHandler)
	mux.HandleFunc("POST /api/integrations/jira/sync", s.jiraSyncHandler)
}

func (s *Server) getJiraHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, s.jiraStatusOrEmpty())
}

func (s *Server) putJiraCredentialsHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Site  string `json:"site"`
		Email string `json:"email"`
		Token string `json:"token"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, "invalid JSON")
		return
	}
	// An empty token keeps the saved one for the same site, so the email can be
	// corrected; a different site must be given its token again.
	if strings.TrimSpace(req.Token) == "" {
		if cred, err := s.loadJiraCredential(); err == nil {
			if site, err := jira.NormalizeSite(req.Site); err != nil || site != cred.Site {
				writeErr(w, 400, "Enter the API token again when changing the Jira site")
				return
			}
			req.Token = cred.Token
		}
	}
	c, err := jira.New(jira.Config{Site: req.Site, Email: req.Email, Token: req.Token})
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	user, err := c.Myself(r.Context())
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	cred := jiraCredential{Site: c.Site(), Email: strings.TrimSpace(req.Email), Token: strings.TrimSpace(req.Token),
		AccountID: user.AccountID, DisplayName: user.DisplayName, SavedAt: time.Now().UTC().Format(time.RFC3339)}
	raw, err := json.Marshal(cred)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	if err := privatefs.PublishFile(s.jiraCredentialPath(), raw); err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, s.jiraStatusOrEmpty())
}

func (s *Server) deleteJiraCredentialsHandler(w http.ResponseWriter, _ *http.Request) {
	if err := removeIfExists(s.jiraCredentialPath()); err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, s.jiraStatusOrEmpty())
}

func (s *Server) putJiraConfigHandler(w http.ResponseWriter, r *http.Request) {
	var cfg jiraConfig
	if err := decode(r, &cfg); err != nil {
		writeErr(w, 400, "invalid JSON")
		return
	}
	cfg, err := validateJiraConfig(cfg)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	raw, _ := json.Marshal(cfg)
	if err := s.m.pg.SetSetting(settingJiraConfig, string(raw)); err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, s.jiraStatusOrEmpty())
}

func (s *Server) jiraProjectsHandler(w http.ResponseWriter, r *http.Request) {
	c, _, err := s.jiraClient()
	if err != nil {
		writeError(w, 400, err)
		return
	}
	projects, err := c.Projects(r.Context())
	if err != nil {
		writeError(w, 502, err)
		return
	}
	writeJSON(w, 200, projects)
}

// jiraHandledFields are the create-screen fields Counterweight fills or Jira fills itself.
var jiraHandledFields = map[string]bool{"summary": true, "issuetype": true, "project": true, "description": true,
	"reporter": true, "labels": true, "priority": true, "duedate": true, "security": true, "parent": true}

// jiraTypeMeta summarizes what the create screen of an issue type accepts.
type jiraTypeMeta struct {
	Priorities          []jira.AllowedValue `json:"priorities"`
	SecurityLevels      []jira.AllowedValue `json:"security_levels"`
	HasDueDate          bool                `json:"has_due_date"`
	HasLabels           bool                `json:"has_labels"`
	HasParent           bool                `json:"has_parent"`
	UnsupportedRequired []string            `json:"unsupported_required"`
}

func summarizeJiraFields(fields map[string]jira.Field) jiraTypeMeta {
	m := jiraTypeMeta{Priorities: []jira.AllowedValue{}, SecurityLevels: []jira.AllowedValue{}, UnsupportedRequired: []string{}}
	if f, ok := fields["priority"]; ok {
		m.Priorities = append(m.Priorities, f.AllowedValues...)
	}
	if f, ok := fields["security"]; ok {
		m.SecurityLevels = append(m.SecurityLevels, f.AllowedValues...)
	}
	_, m.HasDueDate = fields["duedate"]
	_, m.HasLabels = fields["labels"]
	_, m.HasParent = fields["parent"]
	for id, f := range fields {
		if f.Required && !f.HasDefault && !jiraHandledFields[id] {
			m.UnsupportedRequired = append(m.UnsupportedRequired, nz(f.Name, id))
		}
	}
	return m
}

func (s *Server) jiraProjectMetaHandler(w http.ResponseWriter, r *http.Request) {
	c, _, err := s.jiraClient()
	if err != nil {
		writeError(w, 400, err)
		return
	}
	key := strings.ToUpper(r.PathValue("key"))
	if !jiraKeyRe.MatchString(key) {
		writeErr(w, 400, "invalid project key")
		return
	}
	types, err := c.IssueTypes(r.Context(), key)
	if err != nil {
		writeError(w, 502, err)
		return
	}
	out := map[string]any{"issue_types": types}
	if id := r.URL.Query().Get("issue_type"); jiraIDRe.MatchString(id) {
		fields, err := c.CreateFields(r.Context(), key, id)
		if err != nil {
			writeError(w, 502, err)
			return
		}
		out["fields"] = summarizeJiraFields(fields)
	}
	writeJSON(w, 200, out)
}

// --- export -------------------------------------------------------------------

// jiraLinkView is a filed issue as the UI shows it.
type jiraLinkView struct {
	Key            string `json:"key"`
	URL            string `json:"url"`
	Created        bool   `json:"created"`
	ProjectKey     string `json:"project_key"`
	IssueTypeID    string `json:"issue_type_id,omitempty"`
	SecurityLevel  string `json:"security_level_id,omitempty"`
	StatusName     string `json:"status_name,omitempty"`
	StatusCategory string `json:"status_category,omitempty"`
}

func linkView(c *jira.Client, l *db.JiraLink, created bool) *jiraLinkView {
	if l == nil {
		return nil
	}
	return &jiraLinkView{Key: l.IssueKey, URL: c.BrowseURL(l.IssueKey), Created: created, ProjectKey: l.ProjectKey,
		IssueTypeID: l.IssueTypeID, SecurityLevel: l.SecurityLevel, StatusName: l.StatusName, StatusCategory: l.StatusCategory}
}

func (s *Server) publicURL(path string) string {
	base := strings.TrimRight(strings.TrimSpace(notifyPublicBaseURL(s.m.pg)), "/")
	if base == "" {
		return ""
	}
	return base + path
}

// jiraExport carries one export's client, configuration, target space and
// create-screen metadata, fetched once per export.
type jiraExport struct {
	s         *Server
	c         *jira.Client
	cred      *jiraCredential
	cfg       jiraConfig
	project   string                           // Jira space (project) key
	issueType string                           // issue type of finding issues
	security  string                           // issue security level id, "" for the space default
	meta      map[string]map[string]jira.Field // "space/type" -> create fields
}

// jiraTarget is the space and issue type a request asks for; empty fields
// fall back to the task's epic, then to the defaults in Settings.
type jiraTarget struct {
	ProjectKey  string `json:"project_key"`
	IssueTypeID string `json:"issue_type_id"`
	// SecurityLevelID, when present, is an explicit choice ("" = the space's
	// default); absent, the task's epic or the default from Settings applies.
	SecurityLevelID *string `json:"security_level_id"`
	Scope           string  `json:"scope"` // task exports: open (default) or all
}

func (s *Server) newJiraExport() (*jiraExport, error) {
	c, cred, err := s.jiraClient()
	if err != nil {
		return nil, err
	}
	return &jiraExport{s: s, c: c, cred: cred, cfg: s.loadJiraConfig(), meta: map[string]map[string]jira.Field{}}, nil
}

// target picks the export's Jira space and issue type. A task whose epic exists
// stays in the epic's space, so one assessment never splits across spaces.
func (e *jiraExport) target(req jiraTarget, epic *db.JiraLink) error {
	project := strings.ToUpper(strings.TrimSpace(req.ProjectKey))
	issueType := strings.TrimSpace(req.IssueTypeID)
	security := ""
	switch {
	case req.SecurityLevelID != nil:
		security = strings.TrimSpace(*req.SecurityLevelID)
	case epic != nil:
		security = epic.SecurityLevel
	}
	if epic != nil {
		if project != "" && project != epic.ProjectKey {
			return locale.Errorf("This task is filed in the Jira space %s; its findings go there too", epic.ProjectKey)
		}
		project = epic.ProjectKey
		if issueType == "" {
			issueType = epic.IssueTypeID
		}
	}
	if project == "" {
		project = e.cfg.ProjectKey
	}
	if project == e.cfg.ProjectKey {
		if issueType == "" {
			issueType = e.cfg.IssueTypeID
		}
		if req.SecurityLevelID == nil && epic == nil {
			security = e.cfg.SecurityLevelID
		}
	}
	if security != "" && !jiraIDRe.MatchString(security) {
		return locale.NewError("Invalid Jira security level")
	}
	e.security = security
	switch {
	case project == "":
		return locale.NewError("Choose the Jira space to file the issues in")
	case issueType == "":
		return locale.NewError("Choose the Jira issue type for the findings")
	case !jiraKeyRe.MatchString(project) || !jiraIDRe.MatchString(issueType):
		return locale.NewError("Invalid Jira space or issue type")
	}
	e.project, e.issueType = project, issueType
	return nil
}

// allowedValue reports whether a field on the create screen accepts id. Fields
// that list no options accept any id.
func allowedValue(meta map[string]jira.Field, field, id string) bool {
	f, ok := meta[field]
	if !ok || id == "" {
		return false
	}
	if len(f.AllowedValues) == 0 {
		return true
	}
	for _, v := range f.AllowedValues {
		if v.ID == id {
			return true
		}
	}
	return false
}

func (e *jiraExport) fields(ctx context.Context, typeID string) (map[string]jira.Field, error) {
	key := e.project + "/" + typeID
	if f, ok := e.meta[key]; ok {
		return f, nil
	}
	f, err := e.c.CreateFields(ctx, e.project, typeID)
	if err != nil {
		return nil, err
	}
	if m := summarizeJiraFields(f); len(m.UnsupportedRequired) > 0 {
		return nil, locale.Errorf("The Jira issue type requires fields Counterweight does not fill (%s); give them defaults in Jira or choose another issue type",
			strings.Join(m.UnsupportedRequired, ", "))
	}
	e.meta[key] = f
	return f, nil
}

// epicType is the configured epic type, or the project's epic-level type; ""
// when the project has none (issues are then filed without a parent).
func (e *jiraExport) epicType(ctx context.Context) (string, error) {
	if e.cfg.EpicTypeID != "" && e.project == e.cfg.ProjectKey {
		return e.cfg.EpicTypeID, nil
	}
	types, err := e.c.IssueTypes(ctx, e.project)
	if err != nil {
		return "", err
	}
	for _, t := range types {
		if t.HierarchyLevel == 1 {
			return t.ID, nil
		}
	}
	return "", nil
}

func panelKind(severity string) string {
	switch severity {
	case "critical", "high":
		return "error"
	case "medium":
		return "warning"
	}
	return "info"
}

// jiraDoc lays out a ticket: a summary panel, then one section per heading.
// The detailed report is dropped when the document exceeds Jira's field limit.
func jiraDoc(t report.Ticket, panel string) jira.Doc {
	build := func(skip string) jira.Doc {
		var facts []jira.Node
		for _, f := range t.Facts {
			text := jira.Text(f.Text)
			if f.Link != "" {
				text = jira.Text(f.Text, jira.Link(f.Link))
			}
			facts = append(facts, jira.Paragraph(jira.Text(f.Label+": ", jira.Strong()), text))
		}
		blocks := []jira.Node{jira.Panel(panel, facts...)}
		for _, sec := range t.Sections {
			blocks = append(blocks, jira.Heading(3, jira.Text(sec.Title)))
			if sec.Title == skip {
				blocks = append(blocks, jira.Paragraph(jira.Text("This section is too long for Jira; read it in Counterweight.")))
				continue
			}
			blocks = append(blocks, jira.FromMarkdown(sec.Markdown, 4)...)
		}
		blocks = append(blocks, jira.Rule(), jira.Paragraph(jira.Text(
			"Filed by Counterweight. Credentials and local paths are redacted. Counterweight comments here as the fix progresses.")))
		return jira.NewDoc(blocks...)
	}
	doc := build("")
	if jira.DocSize(doc) > 30000 {
		doc = build("Detailed report and reproduction")
	}
	return doc
}

// create files one issue and links it back to Counterweight.
func (e *jiraExport) create(ctx context.Context, typeID, severity, parentKey string, t report.Ticket, due string) (jira.Issue, error) {
	meta, err := e.fields(ctx, typeID)
	if err != nil {
		return jira.Issue{}, err
	}
	if _, ok := meta["description"]; !ok {
		return jira.Issue{}, locale.Errorf("Add the Description field to the create screen of this issue type in the Jira space %s", e.project)
	}
	fields := map[string]any{
		"project":     map[string]string{"key": e.project},
		"issuetype":   map[string]string{"id": typeID},
		"summary":     t.Summary,
		"description": jiraDoc(t, panelKind(severity)),
	}
	if _, ok := meta["labels"]; ok {
		labels := append([]string{}, t.Labels...)
		labels = append(labels, e.cfg.Labels...)
		fields["labels"] = labels
	}
	// Priorities and security levels come from Settings; each is applied only
	// when this space offers it, so one configuration works across spaces.
	if id := e.cfg.Priorities[severity]; allowedValue(meta, "priority", id) {
		fields["priority"] = map[string]string{"id": id}
	}
	if due != "" {
		if _, ok := meta["duedate"]; ok {
			fields["duedate"] = due
		}
	}
	// A requested visibility restriction is never dropped silently: tickets
	// carry reproduction steps, so an export that cannot restrict them fails.
	if e.security != "" {
		if !allowedValue(meta, "security", e.security) {
			return jira.Issue{}, locale.Errorf("The chosen security level is not available in the Jira space %s; choose another level or space", e.project)
		}
		fields["security"] = map[string]string{"id": e.security}
	}
	if parentKey != "" {
		if _, ok := meta["parent"]; ok {
			fields["parent"] = map[string]string{"key": parentKey}
		}
	}
	return e.c.CreateIssue(ctx, fields)
}

// record saves a created issue at once, even if the request is cancelled, so a
// retry never files it twice; the link back to Counterweight is added after.
func (e *jiraExport) record(ctx context.Context, l *db.JiraLink, backURL, globalID string) (*db.JiraLink, bool, error) {
	ctx = context.WithoutCancel(ctx)
	out, created, err := e.s.m.pg.InsertJiraLink(ctx, l)
	if err != nil {
		log.Printf("[jira] %s was created but could not be recorded: %v", l.IssueKey, err)
		return nil, false, err
	}
	if backURL != "" {
		if err := e.c.AddRemoteLink(ctx, l.IssueKey, globalID, backURL, "Open in Counterweight"); err != nil {
			log.Printf("[jira] link %s back to Counterweight: %v", l.IssueKey, err)
		}
	}
	return out, created, nil
}

// existing returns a recorded link whose issue still exists; a link to a
// deleted issue is forgotten so the next export files a new one.
func (e *jiraExport) existing(ctx context.Context, l *db.JiraLink) (*db.JiraLink, error) {
	if l == nil {
		return nil, nil
	}
	st, err := e.c.Status(ctx, l.IssueKey)
	if jira.IsNotFound(err) {
		return nil, e.s.m.pg.DeleteJiraLink(ctx, l.ID)
	}
	if err != nil {
		return nil, err
	}
	_ = e.s.m.pg.SetJiraLinkStatus(ctx, l.ID, st.Name, st.Category)
	l.StatusName, l.StatusCategory = st.Name, st.Category
	return l, nil
}

// jiraTaskInput loads what the epic and the tickets say about a task.
func (s *Server) jiraTaskInput(ctx context.Context, t *Task) (report.Input, error) {
	fs, _, err := s.m.pg.ListFindingsPage(db.FindingFilter{TaskID: t.ID, Sort: "severity"}, 1, 5000)
	if err != nil {
		return report.Input{}, err
	}
	in := report.Input{Title: t.Description, Goal: report.RedactLocalPaths(t.Goal), GeneratedAt: time.Now(), FindingRecords: fs,
		AssessmentType: taskAssessmentType(t), TaskID: t.ID, Status: s.resolvedTaskStatus(t), CodeReview: s.taskCodeReview(t)}
	if in.CodeReview != nil && !in.CodeReview.Inferred {
		if in.Lifecycle, err = s.findingLifecycles(ctx, fs); err != nil {
			return report.Input{}, err
		}
	}
	return in, nil
}

// currentEpic is the task's epic if it was filed and still exists in Jira.
func (e *jiraExport) currentEpic(ctx context.Context, t *Task) (*db.JiraLink, error) {
	taskID, err := strconv.ParseInt(t.ID, 10, 64)
	if err != nil {
		return nil, err
	}
	l, err := e.s.m.pg.JiraEpicLink(ctx, taskID, e.cred.Site)
	if err != nil {
		return nil, err
	}
	return e.existing(ctx, l)
}

// ensureEpic files the task's epic in the target space unless it exists. nil
// means the space has no epic-level issue type.
func (e *jiraExport) ensureEpic(ctx context.Context, t *Task, in report.Input, epic *db.JiraLink) (*db.JiraLink, bool, error) {
	if epic != nil {
		return epic, false, nil
	}
	taskID, err := strconv.ParseInt(t.ID, 10, 64)
	if err != nil {
		return nil, false, err
	}
	typeID, err := e.epicType(ctx)
	if err != nil || typeID == "" {
		return nil, false, err
	}
	backURL := e.s.publicURL("/function/tasks/detail/?id=" + t.ID + "&tab=report")
	issue, err := e.create(ctx, typeID, "", "", report.TaskEpic(in, backURL), "")
	if err != nil {
		return nil, false, err
	}
	return e.record(ctx, &db.JiraLink{Kind: "epic", TaskID: &taskID, Site: e.cred.Site, ProjectKey: e.project,
		IssueKey: issue.Key, IssueID: issue.ID, IssueTypeID: e.issueType, SecurityLevel: e.security}, backURL, "counterweight:task:"+t.ID)
}

// jiraFindingState is what has been reported to Jira about a finding: its
// review status and, for code reviews, its lifecycle stage.
func jiraFindingState(f *db.DBFinding, l *report.FindingLifecycle) string {
	stage := ""
	if l != nil {
		stage = l.Stage()
	}
	return nz(f.Status, db.FindingPending) + "|" + stage
}

// exportFinding files one finding under its task's epic. Callers hold jiraExportMu.
func (e *jiraExport) exportFinding(ctx context.Context, f *db.DBFinding, t *Task, in *report.Input, epic *db.JiraLink) (*db.JiraLink, bool, error) {
	l, err := e.s.m.pg.JiraFindingLink(ctx, f.ID, e.cred.Site)
	if err != nil {
		return nil, false, err
	}
	if l, err = e.existing(ctx, l); err != nil || l != nil {
		return l, false, err
	}
	var lp *report.FindingLifecycle
	if in != nil && in.Lifecycle != nil {
		lc := in.Lifecycle[f.ID]
		lp = &lc
	}
	severity := nz(f.Severity, "info")
	due, days := "", e.cfg.SLADays[severity]
	if days > 0 {
		due = time.Now().AddDate(0, 0, days).Format("2006-01-02")
	}
	backURL := e.s.publicURL("/function/findings/detail?id=" + strconv.FormatInt(f.ID, 10))
	ti := report.TicketInput{Finding: f, Lifecycle: lp, FindingURL: backURL, Due: due, SLADays: days}
	if in != nil {
		ti.CodeReview, ti.TaskID, ti.TaskTitle = in.CodeReview, in.TaskID, in.Title
	}
	parent := ""
	if epic != nil {
		parent = epic.IssueKey
	}
	issue, err := e.create(ctx, e.issueType, severity, parent, report.FindingTicket(ti), due)
	if err != nil {
		return nil, false, err
	}
	link := &db.JiraLink{Kind: "finding", FindingID: &f.ID, TaskID: f.TaskID, Site: e.cred.Site,
		ProjectKey: e.project, IssueKey: issue.Key, IssueID: issue.ID, LastState: jiraFindingState(f, lp)}
	return e.record(ctx, link, backURL, "counterweight:finding:"+strconv.FormatInt(f.ID, 10))
}

func (s *Server) jiraExportFindingHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, 400, "bad finding id")
		return
	}
	var req jiraTarget
	if err := decode(r, &req); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, 400, "invalid JSON")
		return
	}
	jiraExportMu.Lock()
	defer jiraExportMu.Unlock()
	ctx := r.Context()
	e, err := s.newJiraExport()
	if err != nil {
		writeError(w, 400, err)
		return
	}
	f, err := s.m.pg.GetFinding(id)
	if err != nil || f == nil {
		writeErr(w, 404, "finding not found")
		return
	}
	// Already filed: return the issue, whatever space the request names.
	if l, err := e.s.m.pg.JiraFindingLink(ctx, f.ID, e.cred.Site); err != nil {
		writeError(w, 500, err)
		return
	} else if l, err = e.existing(ctx, l); err != nil {
		writeError(w, 502, err)
		return
	} else if l != nil {
		writeJSON(w, 200, map[string]any{"issue": linkView(e.c, l, false), "epic": nil})
		return
	}
	var (
		in   *report.Input
		epic *db.JiraLink
		t    *Task
	)
	if f.TaskID != nil {
		t = s.m.ResolveTask(strconv.FormatInt(*f.TaskID, 10))
	}
	if t != nil {
		if epic, err = e.currentEpic(ctx, t); err != nil {
			writeError(w, 502, err)
			return
		}
	}
	if err := e.target(req, epic); err != nil {
		writeError(w, 400, err)
		return
	}
	if t != nil {
		ti, err := s.jiraTaskInput(ctx, t)
		if err != nil {
			writeError(w, 500, err)
			return
		}
		in = &ti
		if epic, _, err = e.ensureEpic(ctx, t, ti, epic); err != nil {
			writeError(w, 502, err)
			return
		}
	}
	l, created, err := e.exportFinding(ctx, f, t, in, epic)
	if err != nil {
		writeError(w, 502, err)
		return
	}
	writeJSON(w, 200, map[string]any{"issue": linkView(e.c, l, created), "epic": linkView(e.c, epic, false)})
}

type jiraTaskResult struct {
	FindingID int64         `json:"finding_id,string"`
	Title     string        `json:"title"`
	Severity  string        `json:"severity"`
	Issue     *jiraLinkView `json:"issue,omitempty"`
	Skipped   string        `json:"skipped,omitempty"`
	Error     string        `json:"error,omitempty"`
}

func (s *Server) jiraExportTaskHandler(w http.ResponseWriter, r *http.Request) {
	var req jiraTarget
	if err := decode(r, &req); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, 400, "invalid JSON")
		return
	}
	t := s.m.ResolveTask(r.PathValue("id"))
	if t == nil {
		writeErr(w, 404, "task not found")
		return
	}
	jiraExportMu.Lock()
	defer jiraExportMu.Unlock()
	ctx := r.Context()
	e, err := s.newJiraExport()
	if err != nil {
		writeError(w, 400, err)
		return
	}
	current, err := e.currentEpic(ctx, t)
	if err != nil {
		writeError(w, 502, err)
		return
	}
	if err := e.target(req, current); err != nil {
		writeError(w, 400, err)
		return
	}
	in, err := s.jiraTaskInput(ctx, t)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	epic, epicCreated, err := e.ensureEpic(ctx, t, in, current)
	if err != nil {
		writeError(w, 502, err)
		return
	}
	results := []jiraTaskResult{}
	for _, item := range in.FindingRecords {
		res := jiraTaskResult{FindingID: item.ID, Title: item.Name, Severity: nz(item.Severity, "info")}
		var lp *report.FindingLifecycle
		if in.Lifecycle != nil {
			lc := in.Lifecycle[item.ID]
			lp = &lc
		}
		if req.Scope != "all" && report.FindingClosed(item, lp) {
			if l, _ := s.m.pg.JiraFindingLink(ctx, item.ID, e.cred.Site); l != nil {
				res.Issue = linkView(e.c, l, false)
			} else {
				res.Skipped = "closed"
			}
			results = append(results, res)
			continue
		}
		f, err := s.m.pg.GetFinding(item.ID) // the list omits the detailed report
		if err != nil || f == nil {
			res.Error = "finding could not be loaded"
			results = append(results, res)
			continue
		}
		l, created, err := e.exportFinding(ctx, f, t, &in, epic)
		if err != nil {
			res.Error = err.Error()
		} else {
			res.Issue = linkView(e.c, l, created)
		}
		results = append(results, res)
	}
	writeJSON(w, 200, map[string]any{"epic": linkView(e.c, epic, epicCreated), "results": results})
}

// jiraTaskLinksHandler lists a task's filed issues; refresh=1 reads their
// current status from Jira.
func (s *Server) jiraTaskLinksHandler(w http.ResponseWriter, r *http.Request) {
	st := s.jiraStatusOrEmpty()
	out := map[string]any{"configured": st.Configured, "default_project": st.Config.ProjectKey,
		"default_issue_type": st.Config.IssueTypeID, "default_security_level": st.Config.SecurityLevelID,
		"epic": nil, "findings": map[string]*jiraLinkView{}}
	c, cred, err := s.jiraClient()
	if err != nil {
		writeJSON(w, 200, out)
		return
	}
	taskID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, 400, "bad task id")
		return
	}
	links, err := s.m.pg.JiraTaskLinks(r.Context(), taskID, cred.Site)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	refresh := r.URL.Query().Get("refresh") == "1"
	findings := map[string]*jiraLinkView{}
	for i, l := range links {
		if refresh && i < 200 {
			if st, err := c.Status(r.Context(), l.IssueKey); err == nil {
				l.StatusName, l.StatusCategory = st.Name, st.Category
				_ = s.m.pg.SetJiraLinkStatus(r.Context(), l.ID, st.Name, st.Category)
			}
		}
		switch {
		case l.Kind == "epic":
			out["epic"] = linkView(c, l, false)
		case l.FindingID != nil:
			findings[strconv.FormatInt(*l.FindingID, 10)] = linkView(c, l, false)
		}
	}
	out["findings"] = findings
	writeJSON(w, 200, out)
}

func (s *Server) jiraSyncHandler(w http.ResponseWriter, r *http.Request) {
	n, err := s.syncJira(r.Context())
	if err != nil {
		writeError(w, 502, err)
		return
	}
	writeJSON(w, 200, map[string]int{"updated": n})
}

// --- outbound sync --------------------------------------------------------------

// runJiraSync pushes finding changes to linked issues in the background.
func (s *Server) runJiraSync() {
	t := time.NewTicker(jiraSyncEvery)
	defer t.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
		}
		if _, err := s.syncJira(s.ctx); err != nil {
			log.Printf("[jira] sync: %v", err)
		}
	}
}

// jiraUpdate says what to post when a finding's reported state changes: a
// comment and, optionally, the status categories to move the issue to.
func jiraUpdate(prev string, f *db.DBFinding, l *report.FindingLifecycle) (string, []string) {
	prevStatus, prevStage, _ := strings.Cut(prev, "|")
	if l != nil {
		if stage := l.Stage(); stage != prevStage {
			switch stage {
			case report.StageValidated:
				return "Re-check passed: an independent verifier re-read the merged code and found the reported path closed.", []string{"done"}
			case report.StageStillPresent:
				return "The re-check after the merge found the issue still present. The fix needs more work.", []string{"indeterminate", "new"}
			case report.StageMerged:
				return "The fix was merged. Counterweight will re-check the merged code.", nil
			case report.StagePROpen:
				msg := "A fix was proposed in a pull request."
				if l.PRURL != "" {
					msg = "A fix was proposed in a pull request: " + l.PRURL
				}
				return msg, nil
			case report.StageFixProposed:
				return "A fix was written and passed an automatic patch review; it is waiting to be opened as a pull request.", nil
			case report.StageConfirmed:
				return "Independent verification confirmed this finding.", nil
			case report.StageFalsePositive:
				return "Independent verification rejected this finding as a false positive. Review it and close this issue if you agree.", nil
			case report.StageNeedsValidation:
				return "Verification was inconclusive; a person needs to validate this finding.", nil
			}
		}
	}
	status := nz(f.Status, db.FindingPending)
	if status == prevStatus {
		return "", nil
	}
	label := report.FindingStatusLabel(status)
	switch status {
	case db.FindingFixed, db.FindingResolved:
		return "The finding was marked " + strings.ToLower(label) + " in Counterweight.", []string{"done"}
	default:
		return "The finding's review status changed to " + strings.ToLower(label) + " in Counterweight.", nil
	}
}

// jiraSyncMu keeps the background and manual syncs from posting the same change twice.
var jiraSyncMu sync.Mutex

// jiraPermanent reports a Jira refusal that retrying will not fix (bad request,
// missing permission), as opposed to throttling or an outage.
func jiraPermanent(err error) bool {
	var e *jira.APIError
	return errors.As(err, &e) && e.Status >= 400 && e.Status < 500 && e.Status != http.StatusTooManyRequests
}

// syncJira posts finding changes to their linked issues and returns how many
// issues were updated. Unchanged findings make no Jira calls.
func (s *Server) syncJira(ctx context.Context) (int, error) {
	jiraSyncMu.Lock()
	defer jiraSyncMu.Unlock()
	c, cred, err := s.jiraClient()
	if err != nil {
		return 0, nil // not connected
	}
	if !s.loadJiraConfig().Sync {
		return 0, nil
	}
	links, err := s.m.pg.JiraFindingLinks(ctx, cred.Site)
	if err != nil {
		return 0, err
	}
	reviews := map[int64]bool{} // task id -> tracks a code-review lifecycle
	updated := 0
	var stateErrs []error
	for _, l := range links {
		if l.FindingID == nil {
			continue
		}
		f, err := s.m.pg.GetFinding(*l.FindingID)
		if err != nil {
			log.Printf("[jira] load finding %d for %s: %v", *l.FindingID, l.IssueKey, err)
			continue // retried on the next pass
		}
		if f == nil {
			continue
		}
		var lp *report.FindingLifecycle
		if f.TaskID != nil {
			review, seen := reviews[*f.TaskID]
			if !seen {
				if t := s.m.ResolveTask(strconv.FormatInt(*f.TaskID, 10)); t != nil {
					cr := s.taskCodeReview(t)
					review = cr != nil && !cr.Inferred
				}
				reviews[*f.TaskID] = review
			}
			if review {
				ls, err := s.findingLifecycles(ctx, []*db.DBFinding{f})
				if err != nil {
					if ctx.Err() != nil {
						return updated, ctx.Err()
					}
					log.Printf("[jira] lifecycle of finding %d for %s: %v", f.ID, l.IssueKey, err)
					continue // retried on the next pass; other links still sync
				}
				lc := ls[f.ID]
				lp = &lc
			}
		}
		// A status move that failed after its comment was posted is retried alone.
		if l.PendingMove != "" {
			if _, err := c.TransitionTo(ctx, l.IssueKey, strings.Split(l.PendingMove, ",")...); err == nil || jiraPermanent(err) {
				_ = s.m.pg.SetJiraLinkState(ctx, l.ID, l.LastState, "")
				l.PendingMove = ""
			}
		}
		state := jiraFindingState(f, lp)
		if state == l.LastState {
			continue
		}
		msg, move := jiraUpdate(l.LastState, f, lp)
		if msg != "" {
			doc := jira.NewDoc(jira.Paragraph(jira.Text("Counterweight update: ", jira.Strong()), jira.Text(msg)))
			if err := c.AddComment(ctx, l.IssueKey, doc); err != nil {
				switch {
				case jira.IsNotFound(err):
					_ = s.m.pg.DeleteJiraLink(ctx, l.ID) // deleted in Jira: stop following it
					continue
				case jiraPermanent(err):
					log.Printf("[jira] %s refused the update comment, not retrying: %v", l.IssueKey, err)
				default:
					log.Printf("[jira] comment on %s: %v", l.IssueKey, err)
					continue // retried on the next pass
				}
			}
		}
		pending := ""
		if len(move) > 0 {
			if _, err := c.TransitionTo(ctx, l.IssueKey, move...); err != nil {
				log.Printf("[jira] transition %s: %v", l.IssueKey, err)
				if !jiraPermanent(err) {
					pending = strings.Join(move, ",")
				}
			}
		}
		// The issue was already updated; a failed state write on this row is
		// reported after the pass instead of stopping the remaining links.
		if err := s.m.pg.SetJiraLinkState(context.WithoutCancel(ctx), l.ID, state, pending); err != nil {
			log.Printf("[jira] save sync state of %s: %v", l.IssueKey, err)
			stateErrs = append(stateErrs, err)
			if ctx.Err() != nil {
				return updated, errors.Join(append(stateErrs, ctx.Err())...)
			}
			continue
		}
		updated++
	}
	return updated, errors.Join(stateErrs...)
}

func nz(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}
