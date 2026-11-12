// Package intercept implements the user-configurable tool-call interception layer.
// Rules are loaded from the database and cached in memory. Denials run first,
// then other rules in priority order (highest first) on every PreToolUse event. Three actions are supported:
//
//   - allow: immediately permits the call, skipping lower-priority rules.
//   - deny:  blocks the call and returns a message to the model.
//   - ask:   blocks the call, creates an intercept_pending record, writes an
//     activity to the active conversation, then waits for the user to approve or
//     deny via the /api/intercept/pending/{id}/decide endpoint.
//
// The timeout behaviour is configurable at runtime via SetTimeoutConfig.
package intercept

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/locale"
)

// ctxKey is the unexported context key type to avoid collisions.
type ctxKey int

// ConvIDKey stores the active conversation ID in a context.Context so the
// interceptor can associate "ask" pending records with the right conversation.
const ConvIDKey ctxKey = 0

// WithConvID returns a child context carrying convID.
func WithConvID(ctx context.Context, convID int64) context.Context {
	return context.WithValue(ctx, ConvIDKey, convID)
}

// ConvIDFromContext extracts the conversation ID (0 if absent).
func ConvIDFromContext(ctx context.Context) int64 {
	v, _ := ctx.Value(ConvIDKey).(int64)
	return v
}

// taskCtxKey is a separate unexported key type for task context values.
type taskCtxKey int

const (
	taskInfoCtxKey taskCtxKey = 1
	taskEmitCtxKey taskCtxKey = 2
)

type taskCtxInfo struct{ taskID, agentName string }

// WithTaskContext injects task metadata and an emit function into ctx so that
// HandleAsk can tag pending records and write intercept_request activities to
// the task's exploration stream (making them appear inline in session transcripts).
func WithTaskContext(ctx context.Context, taskID, agentName string, emit func(db.Activity)) context.Context {
	ctx = context.WithValue(ctx, taskInfoCtxKey, taskCtxInfo{taskID, agentName})
	if emit != nil {
		ctx = context.WithValue(ctx, taskEmitCtxKey, emit)
	}
	return ctx
}

func taskInfoFromCtx(ctx context.Context) (taskID, agentName string) {
	if v, ok := ctx.Value(taskInfoCtxKey).(taskCtxInfo); ok {
		return v.taskID, v.agentName
	}
	return "", ""
}

func taskEmitFromCtx(ctx context.Context) func(db.Activity) {
	f, _ := ctx.Value(taskEmitCtxKey).(func(db.Activity))
	return f
}

// compiledRule is an InterceptRule with the regex pre-compiled (nil for string rules).
type compiledRule struct {
	db.InterceptRule
	re *regexp.Regexp
}

// pendingManager tracks in-flight "ask" requests via per-request channels.
type pendingManager struct {
	mu sync.Mutex
	ch map[int64]chan bool
}

func newPendingManager() *pendingManager { return &pendingManager{ch: map[int64]chan bool{}} }

func (p *pendingManager) add(id int64) chan bool {
	ch := make(chan bool, 1)
	p.mu.Lock()
	p.ch[id] = ch
	p.mu.Unlock()
	return ch
}

func (p *pendingManager) resolve(id int64, allowed bool) {
	p.mu.Lock()
	ch, ok := p.ch[id]
	delete(p.ch, id)
	p.mu.Unlock()
	if ok {
		ch <- allowed
	}
}

func (p *pendingManager) remove(id int64) {
	p.mu.Lock()
	delete(p.ch, id)
	p.mu.Unlock()
}

// Reviewer runs the LLM fallback judge for one tool call and returns its verdict
// as a Decision (Action ∈ allow|ask|deny; empty Action means the reply could not
// be parsed). It is injected by the server layer so the intercept package stays
// free of any llm dependency. profileID == 0 means "use the active/default profile".
type Reviewer func(ctx context.Context, profileID int64, prompt string, input ReviewInput) (Decision, error)

// Interceptor loads intercept rules from the database and evaluates them on
// tool calls. It is safe for concurrent use.
type Interceptor struct {
	db           *db.DB
	mu           sync.RWMutex
	cached       []compiledRule  // sorted by priority DESC; nil means not loaded yet
	enabledTools map[string]bool // nil means not loaded yet
	pending      *pendingManager
	reviewer     Reviewer // nil = LLM fallback judge not wired
	taskApproval TaskApprovalHandler
	shell        shellLines // pending shell lines of calls made outside a traced run
}

// SetReviewer installs the LLM fallback judge callback. Passing nil disables it.
func (i *Interceptor) SetReviewer(r Reviewer) {
	i.mu.Lock()
	i.reviewer = r
	i.mu.Unlock()
}

// defaultEnabledTools is the hard-coded set of tools that enter the intercept
// rule system when no intercept_enabled_tools setting has been saved.
var defaultEnabledTools = []string{
	"Bash", "WebFetch", "web_search",
	"shell_open", "shell_send",
	"Write", "Edit", "MultiEdit",
}

// New creates an Interceptor backed by d. The rule cache is lazy-loaded on
// first use.
func New(d *db.DB) *Interceptor {
	return &Interceptor{db: d, pending: newPendingManager()}
}

// Invalidate clears the in-memory rule cache and the enabled-tools cache.
// The next call to Match or IsToolEnabled will reload from the database.
// Call this after any CRUD operation on rules or tool config.
func (i *Interceptor) Invalidate() {
	i.mu.Lock()
	i.cached = nil
	i.enabledTools = nil
	i.mu.Unlock()
}

// Granted reports whether this task has a standing approval for the call.
// A database error does not grant anything.
func (i *Interceptor) Granted(ctx context.Context, tools []string, input []byte) bool {
	if i == nil || i.db == nil {
		return false
	}
	taskID, _ := taskInfoFromCtx(ctx)
	if taskID == "" {
		return false
	}
	grants, err := i.taskGrants(taskID)
	if err != nil {
		return false
	}
	return GrantAllows(grants, tools, input)
}

// MatchingTaskGrant returns the standing approval that covers this call.
func (i *Interceptor) MatchingTaskGrant(ctx context.Context, tools []string, input []byte) (db.TaskToolGrant, bool) {
	if i == nil || i.db == nil {
		return db.TaskToolGrant{}, false
	}
	taskID, _ := taskInfoFromCtx(ctx)
	if taskID == "" {
		return db.TaskToolGrant{}, false
	}
	grants, err := i.taskGrants(taskID)
	if err != nil {
		return db.TaskToolGrant{}, false
	}
	return MatchingGrant(grants, tools, input)
}

// Read fresh so revocation cannot race an in-flight grant-cache load.
func (i *Interceptor) taskGrants(taskID string) ([]db.TaskToolGrant, error) {
	return i.db.ListTaskToolGrants(taskID)
}

func (i *Interceptor) loadLocked() error {
	rules, err := i.db.ListInterceptRules()
	if err != nil {
		return err
	}
	out := make([]compiledRule, 0, len(rules))
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		cr := compiledRule{InterceptRule: r}
		if r.MatchTarget == "tool_call" {
			if err := ValidateCallPattern(r.Pattern); err != nil {
				return err
			}
		}
		if r.MatchType == "regex" {
			re, err := regexp.Compile(r.Pattern)
			if err != nil {
				return fmt.Errorf("compile interception rule %d: %w", r.ID, err)
			}
			cr.re = re
		}
		out = append(out, cr)
	}
	// Denials cannot be bypassed by a broad allow rule. Keep priority within groups.
	sort.SliceStable(out, func(a, b int) bool { return out[a].Action == "deny" && out[b].Action != "deny" })
	// Load enabled-tools set from settings, falling back to hard-coded defaults.
	val, ok, err := i.db.GetSetting("intercept_enabled_tools")
	if err != nil {
		return err
	}
	var enabledTools map[string]bool
	if !ok {
		m := make(map[string]bool, len(defaultEnabledTools))
		for _, n := range defaultEnabledTools {
			m[n] = true
		}
		enabledTools = m
	} else {
		var names []string
		if err := json.Unmarshal([]byte(val), &names); err != nil {
			return err
		} else {
			m := make(map[string]bool, len(names))
			for _, n := range names {
				m[n] = true
			}
			enabledTools = m
		}
	}
	// Publish one complete snapshot. A partial load must never become a cached
	// empty policy or disabled interception set.
	i.cached, i.enabledTools = out, enabledTools
	return nil
}

func (i *Interceptor) rules() ([]compiledRule, error) {
	i.mu.RLock()
	if i.cached != nil {
		out := i.cached
		i.mu.RUnlock()
		return out, nil
	}
	i.mu.RUnlock()

	i.mu.Lock()
	defer i.mu.Unlock()
	if i.cached != nil {
		return i.cached, nil
	}
	if err := i.loadLocked(); err != nil {
		return nil, err
	}
	return i.cached, nil
}

// IsToolEnabled returns true if the named tool is in the intercept-enabled set
// (i.e. it should enter the rule-matching path). Uses the same double-check lock
// pattern as rules().
func (i *Interceptor) IsToolEnabled(name string) bool {
	i.mu.RLock()
	if i.enabledTools != nil {
		v := i.enabledTools[name]
		i.mu.RUnlock()
		return v
	}
	i.mu.RUnlock()

	i.mu.Lock()
	defer i.mu.Unlock()
	if i.enabledTools == nil {
		if err := i.loadLocked(); err != nil {
			// Unknown policy is not the same as deliberately disabled controls.
			// Enter Match, which returns a denial when loading fails.
			return true
		}
	}
	return i.enabledTools[name]
}

// GetEnabledTools returns the ordered list of tool names that are currently
// configured to enter the intercept rule system. When the setting has never been
// saved the hard-coded default list is returned.
func (i *Interceptor) GetEnabledTools() ([]string, error) {
	val, ok, err := i.db.GetSetting("intercept_enabled_tools")
	if err != nil {
		return nil, err
	}
	if !ok {
		out := make([]string, len(defaultEnabledTools))
		copy(out, defaultEnabledTools)
		return out, nil
	}
	var names []string
	if err := json.Unmarshal([]byte(val), &names); err != nil {
		return []string{}, nil
	}
	return names, nil
}

// SetEnabledTools persists the list of tool names that should enter the intercept
// rule system, then invalidates the cache so the next call picks up the new list.
func (i *Interceptor) SetEnabledTools(tools []string) error {
	b, err := json.Marshal(tools)
	if err != nil {
		return err
	}
	if err := i.db.SetSetting("intercept_enabled_tools", string(b)); err != nil {
		return err
	}
	i.Invalidate()
	return nil
}

// Decision is the outcome of a successful rule match.
type Decision struct {
	ModelInput       json.RawMessage
	ModelInputDigest string
	ModelFallback    bool
	RuleName         string
	ConfigDigest     string
	ProfileID        int64
	Action           string // "allow" | "deny" | "ask"
	GrantScope       string // once | command | input | tool | all, when a person or standing approval decided
	RememberAllowed  bool   // Legacy whole-tool prompts may be satisfied by task approvals.
	HumanRequired    bool   // A person must decide; neither task authorization nor the model reviewer may.
	Message          string
	RuleID           int64
	TimeoutEnabled   bool
	TimeoutSeconds   int
	TimeoutAction    string // "deny" | "allow"
}

// --- LLM fallback judge ---

// Judge settings keys (stored in the settings KV table). See docs §3.
const (
	settingJudgeEnabled          = "llm_judge_enabled"
	settingJudgeProfileID        = "llm_judge_profile_id"
	settingJudgePrompt           = "llm_judge_prompt"
	settingJudgeTimeoutSecs      = "llm_judge_timeout_seconds"
	settingJudgeFailAction       = "llm_judge_fail_action"
	settingJudgeAskTimeoutSecs   = "llm_judge_ask_timeout_seconds"
	settingJudgeAskTimeoutAction = "llm_judge_ask_timeout_action"
)

// Judge default values.
const (
	defaultJudgeTimeoutSecs      = 15
	defaultJudgeFailAction       = "ask"
	defaultJudgeAskTimeoutSecs   = 300
	defaultJudgeAskTimeoutAction = "deny"
)

// JudgeConfig is the resolved LLM-fallback-judge configuration. Prompt is always
// non-empty (falls back to DefaultJudgePrompt).
type JudgeConfig struct {
	Enabled           bool   `json:"enabled"`
	ProfileID         int64  `json:"profile_id"` // 0 = follow active/default
	Prompt            string `json:"prompt"`
	TimeoutSeconds    int    `json:"timeout_seconds"`
	FailAction        string `json:"fail_action"` // allow|ask|deny
	AskTimeoutSeconds int    `json:"ask_timeout_seconds"`
	AskTimeoutAction  string `json:"ask_timeout_action"` // allow|deny
}

// judgeConfig reads the judge configuration from settings, applying defaults for
// missing/invalid keys. Read fresh on each fallback judgement — the LLM call that
// follows dwarfs a few KV reads, and freshness avoids a cache-invalidation path.
func (i *Interceptor) judgeConfig() JudgeConfig {
	c, err := i.judgeConfigChecked()
	if err != nil {
		// The legacy UI accessor has no error return. Execution uses the checked
		// reader below and denies on failure rather than treating this as disabled.
		return JudgeConfig{Prompt: DefaultJudgePrompt, TimeoutSeconds: defaultJudgeTimeoutSecs, FailAction: defaultJudgeFailAction, AskTimeoutSeconds: defaultJudgeAskTimeoutSecs, AskTimeoutAction: defaultJudgeAskTimeoutAction}
	}
	return c
}

func (i *Interceptor) judgeConfigChecked() (JudgeConfig, error) {
	keys := []string{settingJudgeEnabled, settingJudgeProfileID, settingJudgePrompt, settingJudgeTimeoutSecs, settingJudgeFailAction, settingJudgeAskTimeoutSecs, settingJudgeAskTimeoutAction}
	rows, err := i.db.Query(`SELECT key, value FROM settings WHERE key = ANY($1::text[])`, keys)
	if err != nil {
		return JudgeConfig{}, err
	}
	defer rows.Close()
	values := make(map[string]string, len(keys))
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return JudgeConfig{}, err
		}
		values[key] = value
	}
	if err := rows.Err(); err != nil {
		return JudgeConfig{}, err
	}
	c := JudgeConfig{
		Enabled:           values[settingJudgeEnabled] == "true" || values[settingJudgeEnabled] == "1",
		ProfileID:         int64(settingInt(values, settingJudgeProfileID, 0)),
		TimeoutSeconds:    settingInt(values, settingJudgeTimeoutSecs, defaultJudgeTimeoutSecs),
		FailAction:        settingChoice(values, settingJudgeFailAction, defaultJudgeFailAction, "allow", "ask", "deny"),
		AskTimeoutSeconds: settingInt(values, settingJudgeAskTimeoutSecs, defaultJudgeAskTimeoutSecs),
		AskTimeoutAction:  settingChoice(values, settingJudgeAskTimeoutAction, defaultJudgeAskTimeoutAction, "allow", "deny"),
	}
	// Prompt: stored value if non-empty, else the built-in template.
	if v := values[settingJudgePrompt]; strings.TrimSpace(v) != "" {
		c.Prompt = v
	} else {
		c.Prompt = DefaultJudgePrompt
	}
	if c.TimeoutSeconds <= 0 {
		c.TimeoutSeconds = defaultJudgeTimeoutSecs
	}
	if c.AskTimeoutSeconds <= 0 {
		c.AskTimeoutSeconds = defaultJudgeAskTimeoutSecs
	}
	return c, nil
}

func settingInt(values map[string]string, key string, def int) int {
	v, ok := values[key]
	if !ok {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return n
}

func settingChoice(values map[string]string, key, def string, allowed ...string) string {
	v, ok := values[key]
	if !ok {
		return def
	}
	v = strings.TrimSpace(v)
	for _, a := range allowed {
		if v == a {
			return v
		}
	}
	return def
}

// GetJudgeConfig returns the resolved judge configuration for the API/UI. Prompt
// is the effective prompt (built-in template when unset), so the UI can prefill.
func (i *Interceptor) GetJudgeConfig() JudgeConfig { return i.judgeConfig() }

// SetJudgeConfig persists the judge configuration. An empty Prompt clears the
// override (the built-in template is used again).
func (i *Interceptor) SetJudgeConfig(c JudgeConfig) error {
	if err := i.db.SetBool(settingJudgeEnabled, c.Enabled); err != nil {
		return err
	}
	if err := i.db.SetSetting(settingJudgeProfileID, strconv.FormatInt(c.ProfileID, 10)); err != nil {
		return err
	}
	// Store the prompt only when it differs from the built-in template, so version
	// updates to DefaultJudgePrompt flow through for users who never customized it.
	promptToStore := ""
	if strings.TrimSpace(c.Prompt) != "" && !IsDefaultJudgePrompt(c.Prompt) {
		promptToStore = c.Prompt
	}
	if err := i.db.SetSetting(settingJudgePrompt, promptToStore); err != nil {
		return err
	}
	if err := i.db.SetSetting(settingJudgeTimeoutSecs, strconv.Itoa(c.TimeoutSeconds)); err != nil {
		return err
	}
	if err := i.db.SetSetting(settingJudgeFailAction, c.FailAction); err != nil {
		return err
	}
	if err := i.db.SetSetting(settingJudgeAskTimeoutSecs, strconv.Itoa(c.AskTimeoutSeconds)); err != nil {
		return err
	}
	if err := i.db.SetSetting(settingJudgeAskTimeoutAction, c.AskTimeoutAction); err != nil {
		return err
	}
	return nil
}

// Judge runs the LLM fallback judge for a tool call that matched no rule. It
// returns (Decision, true) when the judge produced a terminal verdict, or
// (Decision{}, false) when the fallback is disabled or not wired (caller then
// keeps the current behavior: allow). On model error or an unparseable reply it
// falls back to the configured FailAction. Ask verdicts carry the human-approval
// timeout so the existing HandleAsk consumes them unchanged.
func (i *Interceptor) Judge(ctx context.Context, tool string, arguments json.RawMessage) (Decision, bool) {
	cfg, err := i.judgeConfigChecked()
	if err != nil {
		return policyUnavailable(locale.FromContext(ctx)), true
	}
	i.mu.RLock()
	rv := i.reviewer
	i.mu.RUnlock()
	if !cfg.Enabled || rv == nil {
		return Decision{}, false
	}

	cctx := ctx
	if cfg.TimeoutSeconds > 0 {
		var cancel context.CancelFunc
		cctx, cancel = context.WithTimeout(ctx, time.Duration(cfg.TimeoutSeconds)*time.Second)
		defer cancel()
	}

	authorization, authorizationErr := i.TaskOperatorAuthorization(cctx)
	if authorizationErr != nil {
		return Decision{Action: "ask", Message: "Could not load the task's user authorization", TimeoutEnabled: true, TimeoutSeconds: cfg.AskTimeoutSeconds, TimeoutAction: "deny"}, true
	}
	cctx = WithOperatorAuthorization(cctx, authorization)
	input, contextErr := BuildReviewInput(cctx, tool, arguments)
	cfg.Prompt = EffectiveJudgePrompt(cfg.Prompt, locale.FromContext(cctx))
	var out Decision
	var modelInput []byte
	if contextErr != nil {
		// Invalid current arguments cannot be reviewed faithfully, regardless of
		// the configured model-failure strategy. A human must resolve the input.
		out = Decision{Action: "ask", ModelFallback: true, Message: locale.Text(locale.FromContext(ctx), "Review context is incomplete; human confirmation is required: ") + locale.ErrorMessage(locale.FromContext(ctx), contextErr)}
	} else {
		modelInput, _ = json.Marshal(input)
		out, err = rv(cctx, cfg.ProfileID, cfg.Prompt, input)
	}
	if err != nil {
		out = Decision{ProfileID: out.ProfileID, ModelFallback: true, Action: cfg.FailAction, Message: locale.Text(locale.FromContext(ctx), "Model review failed; applying failure policy: ") + locale.ErrorMessage(locale.FromContext(ctx), err)}
	}
	switch out.Action {
	case "allow", "ask", "deny":
		// valid verdict
	default:
		out = Decision{ProfileID: out.ProfileID, ModelFallback: true, Action: cfg.FailAction, Message: locale.Text(locale.FromContext(ctx), "Model output could not be parsed; applying failure policy")}
	}
	// A model verdict never carries a rule; keep RuleID 0 (→ NULL) for history.
	out.RuleID = 0
	if len(modelInput) > 0 {
		out.ModelInput = modelInput
		out.ModelInputDigest = digestInput(modelInput)
	}
	if out.ProfileID != 0 {
		cfg.ProfileID = out.ProfileID
	}
	out.ProfileID = cfg.ProfileID
	configJSON, _ := json.Marshal(cfg)
	out.ConfigDigest = digestInput(configJSON)
	if out.Message == "" {
		out.Message = locale.Text(locale.FromContext(ctx), "[Model] ") + judgeActionLabel(out.Action, locale.FromContext(ctx))
	} else if !strings.HasPrefix(out.Message, locale.Text(locale.FromContext(ctx), "[Model]")) {
		out.Message = locale.Text(locale.FromContext(ctx), "[Model] ") + out.Message
	}
	if out.Action == "ask" {
		out.TimeoutEnabled = true
		out.TimeoutSeconds = cfg.AskTimeoutSeconds
		out.TimeoutAction = cfg.AskTimeoutAction
	}
	return out, true
}

func judgeActionLabel(action string, langs ...locale.Lang) string {
	switch action {
	case "allow":
		return locale.Text(locale.First(langs), "Allow")
	case "deny":
		return locale.Text(locale.First(langs), "Block")
	case "ask":
		return locale.Text(locale.First(langs), "Request human approval")
	default:
		return action
	}
}

// Match evaluates denials first, then other rules by descending priority.
// Returns (Decision, true) for the first matching enabled rule, or
// (Decision{}, false) if no rule matches.
func (i *Interceptor) Match(toolName string, input []byte, langs ...locale.Lang) (Decision, bool) {
	return i.MatchNames([]string{toolName}, input, langs...)
}

// MatchNames retains the configured global priority order across the registered
// custom name and its selected executor control (Bash/WebFetch).
func (i *Interceptor) MatchNames(names []string, input []byte, langs ...locale.Lang) (Decision, bool) {
	rules, err := i.rules()
	if err != nil {
		return policyUnavailable(locale.First(langs)), true
	}
	if len(rules) == 0 {
		return Decision{}, false
	}
	analysis := analyzeOperation(names, input)
	subjects := analysis.subjects
	restrictiveInputPolicy := false
	for _, r := range rules {
		if r.MatchTarget == "tool_input" && (r.Action == "deny" || r.Action == "ask") {
			restrictiveInputPolicy = true
		}
		// A rule that allows a call for what its input holds was written for
		// a whole command. It does not speak for a call that only adds to a
		// line already pending.
		if analysis.continuesLine && r.MatchTarget == "tool_input" && r.Action == "allow" {
			continue
		}
		matched := false
		for _, name := range names {
			if ruleMatches(r, name, input) {
				matched = true
				break
			}
		}
		if !matched && r.MatchTarget == "tool_input" && (r.Action == "deny" || r.Action == "ask") {
			for _, subject := range subjects {
				if ruleMatches(r, "", []byte(subject)) {
					matched = true
					break
				}
			}
		}
		if matched {
			msg := r.Message
			if msg == "" {
				msg = defaultMessage(r.Action, r.Name, langs...)
			}
			configJSON, _ := json.Marshal(r.InterceptRule)
			return Decision{
				RuleName: r.Name, ConfigDigest: digestInput(configJSON), RememberAllowed: r.MatchTarget == "tool_name",
				Action:         r.Action,
				Message:        msg,
				RuleID:         r.ID,
				TimeoutEnabled: r.TimeoutEnabled,
				TimeoutSeconds: r.TimeoutSeconds,
				TimeoutAction:  r.TimeoutAction,
			}, true
		}
	}
	shellExecution := false
	for _, name := range names {
		if name == "Bash" || name == "shell_open" || name == "shell_send" {
			shellExecution = true
		}
	}
	if (shellExecution || analysis.runtimeExecutable || analysis.requestMethodUnknown || analysis.shellProgramUnknown) && restrictiveInputPolicy {
		// Input inspection cannot confine a general-purpose shell, even when its
		// executable and arguments are literal. Require a person independently
		// of the fallback judge and never inherit allow-on-timeout here.
		configJSON, _ := json.Marshal(rules)
		ruleName := "Unconfined shell execution"
		message := "Shell execution is not confined by input rules; human approval is required"
		if analysis.runtimeExecutable {
			ruleName = "Runtime-selected shell executable"
			message = "The command selects its executable at runtime; human approval is required"
		}
		if !analysis.runtimeExecutable && analysis.requestMethodUnknown {
			ruleName = "Unresolved HTTP request method"
			message = "Request method depends on runtime arguments or configuration; human approval is required"
		}
		if !analysis.runtimeExecutable && !analysis.requestMethodUnknown && analysis.shellProgramUnknown {
			ruleName = "Unresolved shell program"
			message = "The shell program cannot be inspected before execution; human approval is required"
		}
		return Decision{
			Action: "ask", RuleName: ruleName, ConfigDigest: digestInput(configJSON),
			HumanRequired:  analysis.runtimeExecutable || analysis.requestMethodDynamic || analysis.shellProgramUnknown,
			Message:        locale.Text(locale.First(langs), message),
			TimeoutEnabled: true, TimeoutSeconds: defaultJudgeAskTimeoutSecs, TimeoutAction: "deny",
		}, true
	}
	return Decision{}, false
}

func policyUnavailable(lang locale.Lang) Decision {
	return Decision{Action: "deny", Message: locale.Text(lang, "Interception policy is unavailable; tool execution is blocked")}
}

func ruleMatches(r compiledRule, toolName string, input []byte) bool {
	var subject string
	switch r.MatchTarget {
	case "tool_name":
		subject = toolName
	case "tool_call":
		return CallPatternMatches(r.Pattern, toolName, input)
	case "tool_input":
		subject = string(input)
	default:
		return false
	}
	if r.MatchType == "regex" {
		return r.re != nil && r.re.MatchString(subject)
	}
	return strings.Contains(subject, r.Pattern)
}

func defaultMessage(action, name string, langs ...locale.Lang) string {
	switch action {
	case "deny":
		return locale.Text(locale.First(langs), "Interception rule [") + name + locale.Text(locale.First(langs), "] prohibits this tool call")
	case "ask":
		return locale.Text(locale.First(langs), "Interception rule [") + name + locale.Text(locale.First(langs), "] requires user approval; please wait")
	default:
		return ""
	}
}

// Log records an allow/deny rule or model decision into intercept_pending as an ALREADY-decided
// row (status = "allowed" | "denied"), for observability. Unlike HandleAsk it does NOT
// block and needs no user action — it makes explicit review decisions visible on the history
// page (GET /api/intercept/history) and the task's intercept list. Best-effort: a DB
// error is swallowed so logging never changes the tool call's outcome. The pending list
// (status='pending') is unaffected, so it still shows only asks awaiting a decision.
func (i *Interceptor) Log(ctx context.Context, convID int64, dec Decision, toolName string, input []byte, status string) {
	taskID, agentName := taskInfoFromCtx(ctx)
	audit := auditFor(ctx, dec, input, status)
	id, err := i.db.CreateDecidedIntercept(dec.RuleID, convID, taskID, agentName, toolName, input, status, dec.Message, audit)
	if err == nil {
		i.bindResult(ctx, id, audit)
	}
}

// HandleAsk creates a pending approval record and blocks until the user decides
// (via /api/intercept/pending/{id}/decide) or the per-rule timeout elapses.
// Returns true if the user approved.
//
// convID == 0 means no active conversation (background pentest task). The
// Task-associated requests persist a task-wide hold when a handler is installed.
// Their decision survives cancellation and requires explicit task resume.
func (i *Interceptor) HandleAsk(ctx context.Context, convID int64, dec Decision, toolName string, input []byte) bool {
	ruleID := dec.RuleID
	taskID, agentName := taskInfoFromCtx(ctx)
	taskEmit := taskEmitFromCtx(ctx)

	audit := auditFor(ctx, dec, input, "pending")
	pendingID, err := i.db.CreateInterceptPending(ruleID, convID, taskID, agentName, toolName, input, dec.Message, audit)
	if err != nil {
		return false
	}

	// Persist and install a task-wide execution barrier before returning to the
	// harness. Cancellation must not resolve this approval as a denial.
	held := false
	if handler := i.taskApprovalHandler(); handler != nil && taskID != "" {
		var expires *time.Time
		if dec.TimeoutEnabled {
			secs := dec.TimeoutSeconds
			if secs <= 0 {
				secs = 60
			}
			at := time.Now().Add(time.Duration(secs) * time.Second)
			expires = &at
		}
		held, err = handler(taskID, pendingID, expires, dec.TimeoutAction)
		if err != nil {
			return false
		} // Fail closed; leave the request visible for recovery.
	}
	if !held {
		i.bindResult(ctx, pendingID, audit)
	}
	ch := i.pending.add(pendingID)
	defer i.pending.remove(pendingID)
	// Polling clients can decide after INSERT commits but before the channel is
	// registered. Re-read after registration so that decision cannot be lost;
	// later decisions will be delivered through ch.
	if saved, err := i.db.GetInterceptDetail(pendingID); !held && err == nil && saved != nil && saved.Status != "pending" {
		return saved.Status == "allowed" || (saved.Audit != nil && saved.Audit.EffectiveAction == "allow")
	}

	detail, _ := json.Marshal(map[string]any{
		"pending_id": pendingID,
		"tool":       toolName,
		"input":      json.RawMessage(input),
	})
	activity := db.Activity{
		Kind:    "intercept_request",
		Summary: fmt.Sprintf(locale.Text(locale.FromContext(ctx), "Tool %s requests approval (#%d)"), toolName, pendingID),
		Detail:  string(detail),
	}

	if convID != 0 {
		// Chat session: write inline card to the conversation stream.
		_, _ = i.db.AppendConvActivity(convID, activity)
	} else if taskEmit != nil {
		// Task worker: emit to the exploration activity stream so it appears
		// inline in the session transcript (the emit fn stamps NodeID + Worker).
		taskEmit(activity)
	}

	if held {
		return false
	}

	if !dec.TimeoutEnabled {
		// No timeout: wait indefinitely until user decides or worker stops.
		select {
		case allowed := <-ch:
			return allowed
		case <-ctx.Done():
			_, _ = i.db.ResolveIntercept(pendingID, "denied", "deny", locale.Text(locale.FromContext(ctx), "Work was cancelled"))
			_ = i.db.CompleteIntercept(pendingID, audit.RunID, audit.ToolUseID, "not_executed", locale.Text(locale.FromContext(ctx), "Work was cancelled before execution"), false)
			return false
		}
	}

	secs := dec.TimeoutSeconds
	if secs <= 0 {
		secs = 60
	}
	timer := time.NewTimer(time.Duration(secs) * time.Second)
	defer timer.Stop()
	select {
	case allowed := <-ch:
		return allowed
	case <-timer.C:
		allowed := dec.TimeoutAction == "allow"
		action := "deny"
		if allowed {
			action = "allow"
		}
		resolved, err := i.db.ResolveIntercept(pendingID, "timeout", action, locale.Text(locale.FromContext(ctx), "Approval timed out; applying timeout policy"))
		if err != nil {
			return false
		}
		if !resolved {
			detail, err := i.db.GetInterceptDetail(pendingID)
			return err == nil && detail != nil && (detail.Status == "allowed" || (detail.Audit != nil && detail.Audit.EffectiveAction == "allow"))
		}
		return allowed
	case <-ctx.Done():
		_, _ = i.db.ResolveIntercept(pendingID, "denied", "deny", locale.Text(locale.FromContext(ctx), "Work was cancelled"))
		_ = i.db.CompleteIntercept(pendingID, audit.RunID, audit.ToolUseID, "not_executed", locale.Text(locale.FromContext(ctx), "Work was cancelled before execution"), false)
		return false
	}
}

var ErrAlreadyDecided = locale.NewError("Approval was already resolved or does not exist; refresh the record")

// Decide resolves a pending request. Called by the HTTP decide endpoint.
func (i *Interceptor) Decide(pendingID int64, allowed bool, grants ...*db.TaskToolGrant) error {
	status := "denied"
	if allowed {
		status = "allowed"
	}
	action, reason := "deny", locale.Text(locale.ServerDefault(), "Human denied execution")
	if allowed {
		scope, tool := "once", ""
		if len(grants) > 0 && grants[0] != nil {
			scope, tool = grants[0].Scope, grants[0].ToolName
		}
		action, reason = "allow", HumanGrantReason(locale.ServerDefault(), scope, tool)
	}
	resolved, err := i.db.ResolveIntercept(pendingID, status, action, reason, grants...)
	if err != nil {
		return err
	}
	if !resolved {
		return ErrAlreadyDecided
	}
	if len(grants) > 0 {
		i.Invalidate()
	}
	i.pending.resolve(pendingID, allowed)
	return nil
}
