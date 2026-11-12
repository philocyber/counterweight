package db

import (
	"database/sql"
	"encoding/json"
	"github.com/Philocyber/counterweight/locale"
	"strconv"
	"time"
)

// Task is a row in the task registry (1:1 with an exploration).
type Task struct {
	ID            int64      `json:"id"`
	Name          string     `json:"name"` // Optional task name; empty means unnamed.
	CategoryID    *int64     `json:"category_id,omitempty"`
	CategoryName  string     `json:"category_name,omitempty"`
	Pinned        bool       `json:"pinned"`
	PinnedAt      *time.Time `json:"pinned_at,omitempty"`
	Description   string     `json:"description"`
	Goal          string     `json:"goal"`
	ExplorationID int64      `json:"exploration_id"`
	Status        string     `json:"status"`
	Paused        bool       `json:"paused"`
	Queued        bool       `json:"queued"`
	QueuedAt      *time.Time `json:"queued_at,omitempty"`
	QueueMode     string     `json:"queue_mode,omitempty"`
	LLMProfileID  *int64     `json:"llm_profile_id,omitempty"`
	// Task-level ordered LLM chain. LLMProfileID remains the compatibility alias
	// for ActiveLLMProfileID while older API clients still send one profile id.
	LLMProfileIDs      []int64    `json:"llm_profile_ids,omitempty"`
	ActiveLLMProfileID *int64     `json:"active_llm_profile_id,omitempty"`
	LLMChainRevision   int64      `json:"-"`
	LLMFailoverState   string     `json:"llm_failover_state,omitempty"`
	LLMFailoverReason  string     `json:"llm_failover_reason,omitempty"`
	SourceTaskIDs      []int64    `json:"source_task_ids,omitempty"`
	CompanyIDs         []int64    `json:"company_ids,omitempty"`
	ParentRef          string     `json:"parent_ref,omitempty"` // Parent task ID from orchestration spawn; nil means top-level.
	CreatedAt          time.Time  `json:"created_at"`
	CompletedAt        *time.Time `json:"completed_at,omitempty"` // Time of entering done/failed/timeout; nil for nonterminal tasks.
	// Task-level timeout; see the task-timeout and settlement design.
	TimeoutSeconds int        `json:"timeout_seconds"`        // 0 means unlimited.
	FirstRunAt     *time.Time `json:"first_run_at,omitempty"` // First actual start time, distinct from created_at; nil means never started.
	DeadlineAt     *time.Time `json:"deadline_at,omitempty"`  // first_run_at + timeout_seconds; nil means unlimited or not started.
	// Planner heartbeat in seconds: trigger after this interval from the last plan end/task start if nothing else triggers.
	// Minimum/default=300 (5min); CreateTask raises lower values to 300. See docs/planner-trigger-impl-plan.md.
	PlanHeartbeatSeconds int `json:"plan_heartbeat_seconds"`
	// CoverageEnabled controls asset coverage (default true). When false, do not compute/display coverage,
	// auto-accumulate task_scope(source=auto), expose add_task_scope/list_untested_assets to agents,
	// or inject the coverage block into the situation (scope remains). Company links
	// (task_scope kind=company) are independent and never affected. See db/task_scope.go.
	CoverageEnabled bool `json:"coverage_enabled"`
	// AssessmentType is general, web_app, network or code_review (see assessment_type.go).
	AssessmentType         string          `json:"assessment_type"`
	AssessmentTypeExplicit bool            `json:"-"`
	CodeTarget             *TaskCodeTarget `json:"code_target,omitempty"`
}

// TaskDeleteResult reports optional related-data cleanup performed in the same
// transaction as the task/exploration delete.
type TaskDeleteResult struct {
	AssetsDeleted     int64
	AssetsDetached    int64
	FindingsDeleted   int64
	LLMRecordsDeleted int64
}

// TaskDeletePreparation is produced inside the PostgreSQL deletion transaction
// after asset and anchor writers have been excluded. Prepare callbacks may use
// TrafficHosts to stage an external traffic deletion before PostgreSQL commits.
type TaskDeletePreparation struct {
	ExplorationID int64
	TrafficHosts  []string
}

// IsTerminal reports whether a task status is a terminal (finished) state.
// Single source of truth replacing scattered hardcoded done/failed checks.
func IsTerminal(status string) bool {
	return status == "done" || status == "failed" || status == "timeout"
}

// CreateTask creates an exploration + task in one transaction and returns the task.
// timeoutSeconds is the task-level wall-clock budget (0 = unlimited); deadline_at is
// stamped later at first real run (see engine), not here.
// MinPlanHeartbeatSeconds is the planner heartbeat minimum and default: 10 minutes.
// Raise lower values, including omitted zero, negatives, and misconfigured small values, to 10 minutes to protect the planner.
const MinPlanHeartbeatSeconds = 600

// MaxTaskSourceCount bounds the amount of live inherited context one task can
// pull into every planner/main-agent prompt. Inheritance is intentionally direct
// only; keeping the fan-in bounded also prevents a single create request from
// multiplying graph and asset-context queries without limit.
const MaxTaskSourceCount = 8

// MaxTaskCompanyCount bounds the number of company asset scopes attached to a
// task. Company scopes are prompt context, and their currently attributed
// assets are snapshotted into the task at creation time.
const MaxTaskCompanyCount = 32

const taskCompanyAssetSource = "company"

var (
	ErrTaskCompanyIDsInvalid = locale.NewError("invalid task company ids")
	ErrTaskCompanyNotFound   = locale.NewError("task company not found")
)

// NormalizeTaskCompanyIDs validates IDs and removes duplicates while retaining
// the user's first-seen order.
func NormalizeTaskCompanyIDs(ids []int64) ([]int64, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	seen := make(map[int64]struct{}, min(len(ids), MaxTaskCompanyCount))
	normalized := make([]int64, 0, min(len(ids), MaxTaskCompanyCount))
	for _, id := range ids {
		if id <= 0 {
			return nil, locale.Errorf("%w: company id must be positive", ErrTaskCompanyIDsInvalid)
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		normalized = append(normalized, id)
		if len(normalized) > MaxTaskCompanyCount {
			return nil, locale.Errorf("%w: got more than %d unique companies", ErrTaskCompanyIDsInvalid, MaxTaskCompanyCount)
		}
	}
	return normalized, nil
}

func normalizeHeartbeat(sec int) int {
	if sec < MinPlanHeartbeatSeconds {
		return MinPlanHeartbeatSeconds
	}
	return sec
}

func (d *DB) CreateTask(description, goal string, llmProfileID *int64, timeoutSeconds, planHeartbeatSeconds int) (*Task, error) {
	var ids []int64
	if llmProfileID != nil {
		ids = []int64{*llmProfileID}
	}
	return d.CreateTaskWithOptions(description, goal, TaskCreateOptions{
		LLMProfileIDs: ids, TimeoutSeconds: timeoutSeconds, PlanHeartbeatSeconds: planHeartbeatSeconds,
	})
}

// TaskCreateOptions contains the task data that must be committed atomically
// with the task/exploration row.
type TaskCreateOptions struct {
	// OperatorRequest is set only by human HTTP creation, never by model tools.
	OperatorRequest string

	// Language is persisted atomically in the existing settings table; empty preserves legacy callers.
	Language             locale.Lang
	Name                 string // Optional task name; empty means unnamed.
	CategoryID           *int64
	SourceTaskIDs        []int64
	CompanyIDs           []int64
	LLMProfileIDs        []int64
	TimeoutSeconds       int
	PlanHeartbeatSeconds int
	// CoverageEnabled controls asset coverage; nil defaults to true so callers unaware of the switch
	// (orchestration spawn and old APIs) preserve prior behavior. Web task creation may explicitly disable it.
	CoverageEnabled *bool
	// InterceptRules are task-level asset rules inserted into task_intercept_rules in the task-creation transaction.
	InterceptRules []TaskInterceptRuleInput
	// AuthzAccounts are test accounts of the authorization check, stored sealed in the same transaction
	// (task_authz.go). Not for code reviews.
	AuthzAccounts []AuthzAccountInput
	// AssessmentType defaults to general. code_review requires CodeTarget and
	// always runs without asset coverage; the other types must not carry one.
	AssessmentType string
	CodeTarget     *TaskCodeTarget
	// TokenBudget is the task's token limit (0 = none). nil takes the global
	// default from settings. CostBudget is a limit in CostCurrency (nil or 0 = none).
	TokenBudget  *int64
	CostBudget   *float64
	CostCurrency string
}

// CreateTaskWithOptions creates an exploration, task, direct source relations,
// and the ordered task LLM chain in one transaction.
func (d *DB) CreateTaskWithOptions(description, goal string, opts TaskCreateOptions) (*Task, error) {
	if opts.Language != "" && !locale.Supported(opts.Language) {
		return nil, locale.NewError("language must be en")
	}
	if len(opts.SourceTaskIDs) > MaxTaskSourceCount {
		return nil, locale.Errorf("too many source tasks: got %d, maximum is %d", len(opts.SourceTaskIDs), MaxTaskSourceCount)
	}
	companyIDs, err := NormalizeTaskCompanyIDs(opts.CompanyIDs)
	if err != nil {
		return nil, err
	}
	opts.CompanyIDs = companyIDs
	assessmentType, err := NormalizeAssessmentType(opts.AssessmentType)
	if err != nil {
		return nil, err
	}
	if (assessmentType == AssessmentCodeReview) != (opts.CodeTarget != nil) {
		return nil, ErrCodeTargetRequired
	}
	tx, err := d.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var expID int64
	if err := tx.QueryRow(`INSERT INTO explorations(description, goal) VALUES ($1,$2) RETURNING id`, description, goal).Scan(&expID); err != nil {
		return nil, err
	}
	// origin fact: the exploration graph's root, a KindFact node (state='origin')
	// holding the original task description. Goals/intents/findings descend from
	// it, and the seeded target asset is anchored to it as lineage (the asset graph
	// is global and shared, not isolated per task). Being a fact (not a special 'begin' kind) lets every
	// intent uniformly connect to a fact node, including the first ones.
	originPayload, _ := json.Marshal(map[string]any{
		"summary":     locale.Text(opts.Language, "Task starting point: ") + description + locale.Text(opts.Language, "; objective: ") + goal,
		"description": description,
		"goal":        goal,
	})
	if _, err := tx.Exec(`
INSERT INTO exploration_nodes(exploration_id, kind, payload, priority, state, origin)
VALUES ($1, 'fact', $2, 0, 'origin', 'system')`, expID, string(originPayload)); err != nil {
		return nil, err
	}
	if opts.TimeoutSeconds < 0 {
		opts.TimeoutSeconds = 0
	}
	opts.PlanHeartbeatSeconds = normalizeHeartbeat(opts.PlanHeartbeatSeconds)
	coverageEnabled := (opts.CoverageEnabled == nil || *opts.CoverageEnabled) && assessmentType != AssessmentCodeReview
	var categoryName string
	if opts.CategoryID != nil {
		if *opts.CategoryID <= 0 {
			return nil, locale.Errorf("%w: category id must be positive", ErrTaskCategoryInvalid)
		}
		if err := tx.QueryRow(`SELECT name FROM task_categories WHERE id=$1`, *opts.CategoryID).Scan(&categoryName); err != nil {
			if err == sql.ErrNoRows {
				return nil, ErrTaskCategoryNotFound
			}
			return nil, err
		}
	}
	var active *int64
	if len(opts.LLMProfileIDs) > 0 {
		id := opts.LLMProfileIDs[0]
		active = &id
	}
	t := &Task{
		Name: opts.Name, CategoryID: opts.CategoryID, CategoryName: categoryName,
		Description: description, Goal: goal, ExplorationID: expID,
		LLMProfileID: active, ActiveLLMProfileID: active,
		LLMProfileIDs:  append([]int64(nil), opts.LLMProfileIDs...),
		SourceTaskIDs:  append([]int64(nil), opts.SourceTaskIDs...),
		CompanyIDs:     append([]int64(nil), opts.CompanyIDs...),
		TimeoutSeconds: opts.TimeoutSeconds, PlanHeartbeatSeconds: opts.PlanHeartbeatSeconds,
		CoverageEnabled: coverageEnabled, AssessmentType: assessmentType, AssessmentTypeExplicit: true, CodeTarget: opts.CodeTarget,
	}
	// created_at is taken with clock_timestamp() while the id lock is held until
	// commit, so creation order matches commit order for the scheduler cursor.
	taskID, err := nextReusableTaskID(tx)
	if err != nil {
		return nil, err
	}
	if err := tx.QueryRow(`
INSERT INTO tasks(id, name, category_id, description, goal, exploration_id, llm_profile_id, active_llm_profile_id, timeout_seconds, plan_heartbeat_seconds, coverage_enabled, assessment_type, assessment_type_explicit, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$7,$8,$9,$10,$11,true,clock_timestamp())
RETURNING status, paused, created_at`, taskID, opts.Name, opts.CategoryID, description, goal, expID, active, opts.TimeoutSeconds, opts.PlanHeartbeatSeconds, coverageEnabled, assessmentType).Scan(&t.Status, &t.Paused, &t.CreatedAt); err != nil {
		return nil, err
	}
	t.ID = taskID
	if _, err := tx.Exec(`SELECT setval(pg_get_serial_sequence('tasks','id'), (SELECT MAX(id) FROM tasks))`); err != nil {
		return nil, err
	}
	if err := applyNewTaskBudgetTx(tx, t.ID, opts); err != nil {
		return nil, err
	}
	if opts.OperatorRequest != "" {
		if err := d.recordTaskOperatorInstruction(tx, t.ID, "task", opts.OperatorRequest); err != nil {
			return nil, err
		}
	}
	// Task ids are reused: a review-target row left by an earlier task with this id
	// must not mark the new task as a white-box review or carry its automation switches.
	if _, err := tx.Exec(`DELETE FROM settings WHERE key IN ($1,$2)`, TaskCodeReviewKey(t.ID), TaskAutomationKey(t.ID)); err != nil {
		return nil, err
	}
	// Standing approvals are keyed by the id text alone; a new task never inherits any.
	if _, err := tx.Exec(`DELETE FROM task_tool_grants WHERE task_id=$1`, strconv.FormatInt(t.ID, 10)); err != nil {
		return nil, err
	}
	if opts.CodeTarget != nil {
		if err := insertTaskCodeTarget(tx, t.ID, opts.CodeTarget); err != nil {
			return nil, err
		}
	}
	if opts.Language != "" {
		if _, err := tx.Exec(`INSERT INTO settings(key,value) VALUES ($1,$2) ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value,updated_at=now()`, "task_language."+strconv.FormatInt(t.ID, 10), string(opts.Language)); err != nil {
			return nil, locale.Errorf("persist task language: %w", err)
		}
	}
	if err := insertTaskRelations(tx, t.ID, opts.SourceTaskIDs); err != nil {
		return nil, err
	}
	if err := insertTaskCompanies(tx, t.ID, opts.CompanyIDs, opts.Language); err != nil {
		return nil, err
	}
	if err := insertTaskLLMProfiles(tx, t.ID, opts.LLMProfileIDs); err != nil {
		return nil, err
	}
	if err := insertTaskInterceptRules(tx, t.ID, opts.InterceptRules); err != nil {
		return nil, err
	}
	if err := insertTaskAuthzAccounts(tx, t.ID, opts.AuthzAccounts); err != nil {
		return nil, err
	}
	if len(opts.LLMProfileIDs) == 0 {
		t.LLMFailoverState = "default"
	} else {
		t.LLMFailoverState = "ready"
	}
	return t, tx.Commit()
}

// taskIDLock serializes allocation of the lowest free tasks.id. An advisory
// transaction lock avoids taking a table lock that would deadlock with the
// asset locks held during task deletion.
const taskIDLock int64 = 814274273

func nextReusableTaskID(tx *sql.Tx) (int64, error) {
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock($1)`, taskIDLock); err != nil {
		return 0, err
	}
	var id int64
	err := tx.QueryRow(`
SELECT COALESCE(MIN(gap.id), 1)
FROM (
  SELECT 1 AS id
  WHERE NOT EXISTS (SELECT 1 FROM tasks WHERE id = 1)
  UNION ALL
  SELECT task.id + 1
  FROM tasks task
  WHERE NOT EXISTS (SELECT 1 FROM tasks next WHERE next.id = task.id + 1)
) gap`).Scan(&id)
	return id, err
}

// releaseTaskIdentity detaches rows that keep a task id without a foreign key.
// The numeric id can then be reused without attaching the deleted task's
// history, grants, or asset ownership to the next task.
func releaseTaskIdentity(tx *sql.Tx, id int64, llmRecordsDeleted bool) error {
	taskKey := strconv.FormatInt(id, 10)
	for _, query := range []struct {
		sql  string
		args []any
	}{
		{`UPDATE skill_usage SET task_id=NULL WHERE task_id=$1`, []any{id}},
		{`UPDATE tool_usage SET task_id=NULL WHERE task_id=$1`, []any{id}},
		{`DELETE FROM intercept_pending WHERE task_id=$1`, []any{taskKey}},
		{`DELETE FROM task_tool_grants WHERE task_id=$1`, []any{taskKey}},
		// Follow-up turns of a conversation bound to this task must not bind to a
		// later task with the same id, and must not turn into an unbound chat
		// either: id 0 names no task, so such a conversation stays refused.
		{`UPDATE conversation_task_execution SET task_ids=(SELECT jsonb_agg(CASE WHEN v=to_jsonb($1::bigint) THEN '0'::jsonb ELSE v END) FROM jsonb_array_elements(task_ids) v) WHERE task_ids @> to_jsonb($1::bigint)`, []any{id}},
		{`UPDATE assets SET task_ids=array_remove(task_ids, $1) WHERE task_ids @> ARRAY[$1::bigint]`, []any{id}},
		{`UPDATE task_archives SET source_task_ids=array_remove(source_task_ids, $1) WHERE $1 = ANY(source_task_ids)`, []any{id}},
	} {
		if _, err := tx.Exec(query.sql, query.args...); err != nil {
			return err
		}
	}
	for _, table := range []string{"llm_usage", "llm_records"} {
		if table == "llm_records" && llmRecordsDeleted {
			continue
		}
		var present bool
		if err := tx.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, "public."+table).Scan(&present); err != nil {
			return err
		}
		if !present {
			continue
		}
		if _, err := tx.Exec(`UPDATE `+table+` SET task_id=NULL WHERE task_id=$1`, taskKey); err != nil {
			return err
		}
	}
	return nil
}

func insertTaskCompanies(tx *sql.Tx, taskID int64, companyIDs []int64, lang locale.Lang) error {
	if len(companyIDs) == 0 {
		return nil
	}
	// Company scope edits rebuild assets.company_id. Serialize the creation-time
	// snapshot with those edits so the task sees one committed attribution state.
	if err := lockCompanyScopeMutation(tx); err != nil {
		return err
	}
	var inserted int
	err := tx.QueryRow(`
WITH requested(company_id, position) AS (
    SELECT company_id, position
    FROM unnest($2::bigint[]) WITH ORDINALITY AS requested(company_id, position)
), inserted AS (
    INSERT INTO task_scope(task_id, kind, company_id, source, reason, read_grant)
    SELECT $1, 'company', companies.id, 'manual', $3, snapshot_task_read_grant('company',companies.id,NULL)
    FROM requested
    JOIN companies ON companies.id=requested.company_id
    ORDER BY requested.position
    RETURNING company_id
)
SELECT count(*) FROM inserted`, taskID, companyIDs, locale.Text(lang, "Company linked at task creation")).Scan(&inserted)
	if err != nil {
		return err
	}
	if inserted != len(companyIDs) {
		return locale.Errorf("%w: one or more companies do not exist", ErrTaskCompanyNotFound)
	}

	// Updating task_ids fires sync_task_asset_links, which first creates generic
	// source rows. The provenance upsert must therefore run afterwards so the
	// company name and creation-time reason remain visible to operators. Only
	// the assets in the grant just captured become the task's own: an asset an
	// agent attributed to the company is not handed to the task's agents.
	if _, err := tx.Exec(`
UPDATE assets
SET task_ids=CASE
    WHEN $1=ANY(task_ids) THEN task_ids
    ELSE array_append(task_ids, $1)
END
WHERE company_id=ANY($2::bigint[]) AND id IN (
    SELECT granted.asset_id::bigint
    FROM task_scope ts
    CROSS JOIN LATERAL jsonb_array_elements_text(COALESCE(ts.read_grant->'asset_ids','[]'::jsonb)) granted(asset_id)
    WHERE ts.task_id=$1 AND ts.kind='company')`, taskID, companyIDs); err != nil {
		return err
	}
	if _, err := tx.Exec(`
INSERT INTO task_asset_links(task_id, asset_id, source, source_summary)
SELECT $1, asset.id, $3, $4 || company.name
FROM assets asset
JOIN companies company ON company.id=asset.company_id
WHERE asset.company_id=ANY($2::bigint[])
  AND $1=ANY(asset.task_ids)
ON CONFLICT (task_id, asset_id) DO UPDATE
SET source=EXCLUDED.source,
    source_summary=EXCLUDED.source_summary,
    source_node_id=NULL`, taskID, companyIDs, taskCompanyAssetSource, locale.Text(lang, "Company linked at task creation: ")); err != nil {
		return err
	}
	return nil
}

func insertTaskRelations(tx *sql.Tx, taskID int64, sourceIDs []int64) error {
	seen := map[int64]bool{}
	for _, sourceID := range sourceIDs {
		if sourceID <= 0 || sourceID == taskID || seen[sourceID] {
			return locale.Errorf("invalid or duplicate source task id %d", sourceID)
		}
		seen[sourceID] = true
		res, err := tx.Exec(`INSERT INTO task_relations(task_id, source_task_id)
SELECT $1, id FROM tasks WHERE id=$2 AND deleted_at IS NULL`, taskID, sourceID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return locale.Errorf("source task %d not found", sourceID)
		}
	}
	return nil
}

func insertTaskLLMProfiles(tx *sql.Tx, taskID int64, profileIDs []int64) error {
	seen := map[int64]bool{}
	for position, profileID := range profileIDs {
		if profileID <= 0 || seen[profileID] {
			return locale.Errorf("invalid or duplicate LLM profile id %d", profileID)
		}
		seen[profileID] = true
		if _, err := tx.Exec(`INSERT INTO task_llm_profiles(task_id, profile_id, position) VALUES ($1,$2,$3)`, taskID, profileID, position); err != nil {
			return err
		}
	}
	return nil
}

const taskCols = `id, COALESCE(name,''), category_id,
COALESCE((SELECT category.name FROM task_categories category WHERE category.id=tasks.category_id),''),
description, goal, exploration_id, status, paused, queued, queued_at, COALESCE(queue_mode,''), llm_profile_id, active_llm_profile_id, COALESCE(parent_ref,''), pinned_at, created_at, completed_at, COALESCE(timeout_seconds,0), COALESCE(plan_heartbeat_seconds,300), COALESCE(coverage_enabled,true), first_run_at, deadline_at, assessment_type, assessment_type_explicit`

func scanTask(sc interface{ Scan(...any) error }) (*Task, error) {
	var t Task
	if err := sc.Scan(&t.ID, &t.Name, &t.CategoryID, &t.CategoryName, &t.Description, &t.Goal, &t.ExplorationID, &t.Status, &t.Paused, &t.Queued, &t.QueuedAt, &t.QueueMode, &t.LLMProfileID, &t.ActiveLLMProfileID, &t.ParentRef, &t.PinnedAt, &t.CreatedAt, &t.CompletedAt, &t.TimeoutSeconds, &t.PlanHeartbeatSeconds, &t.CoverageEnabled, &t.FirstRunAt, &t.DeadlineAt, &t.AssessmentType, &t.AssessmentTypeExplicit); err != nil {
		return nil, err
	}
	t.Pinned = t.PinnedAt != nil
	return &t, nil
}

// SetParentRef records a task's parent task ID from orchestration agent spawn_task.
func (d *DB) SetParentRef(id int64, parentRef string) error {
	_, err := d.Exec(`UPDATE tasks SET parent_ref=NULLIF($2,'') WHERE id=$1`, id, parentRef)
	return err
}

// ListTasks returns alive tasks with pinned tasks first, then newest ids.
func (d *DB) ListTasks() ([]*Task, error) {
	// BIGSERIAL IDs give stable unique order even for tasks created simultaneously.
	rows, err := d.Query(`SELECT ` + taskCols + ` FROM tasks WHERE deleted_at IS NULL
ORDER BY (pinned_at IS NOT NULL) DESC, pinned_at DESC NULLS LAST, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if err := d.hydrateTasksContext(out); err != nil {
		return nil, err
	}
	return out, nil
}

// TaskPatch updates task list metadata without changing execution state.
type TaskPatch struct {
	Name   *string
	Pinned *bool
}

// UpdateTask applies a partial task name/pin mutation and returns the updated row.
// Re-pinning an already pinned task preserves its original position.
func (d *DB) UpdateTask(id int64, patch TaskPatch) (*Task, error) {
	task, err := scanTask(d.QueryRow(`UPDATE tasks SET
	name = CASE WHEN $2::boolean THEN $3 ELSE name END,
	pinned_at = CASE
		WHEN $4::boolean IS NULL THEN pinned_at
		WHEN $4::boolean THEN COALESCE(pinned_at, now())
		ELSE NULL
	END
WHERE id=$1 AND deleted_at IS NULL
RETURNING `+taskCols, id, patch.Name != nil, patch.Name, patch.Pinned))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return task, nil
}

// GetTask returns one alive task (nil if not found/deleted).
func (d *DB) GetTask(id int64) (*Task, error) {
	t, err := scanTask(d.QueryRow(`SELECT `+taskCols+` FROM tasks WHERE id=$1 AND deleted_at IS NULL`, id))
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return nil, nil
		}
		return nil, err
	}
	if err := d.hydrateTaskContext(t); err != nil {
		return nil, err
	}
	return t, nil
}

// SetPaused persists a task's paused flag.
func (d *DB) SetPaused(id int64, paused bool) error {
	_, err := d.Exec(`UPDATE tasks SET paused=$1 WHERE id=$2`, paused, id)
	return err
}

// Enqueue places a task at the tail of the persistent admission queue. Repeating
// the operation while it is already queued keeps its original FIFO position.
func (d *DB) Enqueue(id int64, mode string) error {
	if mode != "bootstrap" && mode != "resume" {
		return locale.Errorf("invalid queue mode %q", mode)
	}
	_, err := d.Exec(`UPDATE tasks
SET queued=true,
    queued_at=CASE WHEN queued THEN COALESCE(queued_at, now()) ELSE now() END,
    queue_mode=CASE
        WHEN queue_mode='bootstrap' OR $2='bootstrap' THEN 'bootstrap'
        ELSE 'resume'
    END
WHERE id=$1`, id, mode)
	return err
}

// Dequeue removes the concurrency hold. clearMode is false when a user pauses a
// queued task, preserving whether its next admission must bootstrap or resume.
func (d *DB) Dequeue(id int64, clearMode bool) error {
	_, err := d.Exec(`UPDATE tasks
SET queued=false,
    queued_at=NULL,
    queue_mode=CASE WHEN $2 THEN '' ELSE queue_mode END
WHERE id=$1`, id, clearMode)
	return err
}

// SetQueued is the compatibility helper used by older callers and tests. New
// scheduling code should use Enqueue/Dequeue so FIFO metadata is explicit.
func (d *DB) SetQueued(id int64, queued bool) error {
	if queued {
		return d.Enqueue(id, "bootstrap")
	}
	return d.Dequeue(id, true)
}

// SetStatus updates a task's lifecycle status. Entering a terminal state
// (done/failed/timeout) stamps completed_at once (COALESCE keeps the first stamp
// stable); moving back to a non-terminal state clears it, so a re-run has no stale
// finish time.
func (d *DB) SetStatus(id int64, status string) error {
	_, err := d.Exec(`
UPDATE tasks
   SET status = $1,
       completed_at = CASE WHEN $1 IN ('done','failed','timeout') THEN COALESCE(completed_at, now()) ELSE NULL END
 WHERE id = $2`, status, id)
	return err
}

// SetTerminalStatusGuarded sets a terminal status only when the task is NOT already
// terminal, so a completed↔timeout race resolves to the first writer (won=true).
// Returns won=false (no error) when another terminal status already stuck — the
// caller then leaves it alone. Non-terminal transitions / re-run still use SetStatus.
func (d *DB) SetTerminalStatusGuarded(id int64, status string) (won bool, err error) {
	res, err := d.Exec(`
UPDATE tasks
   SET status = $1,
       completed_at = COALESCE(completed_at, now())
 WHERE id = $2 AND status NOT IN ('done','failed','timeout')`, status, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// StampFirstRun records a task's first-real-run moment and computes its absolute
// deadline (= now + timeoutSeconds). Idempotent: only stamps when first_run_at is
// still NULL, so restarts / re-entries keep the original clock. timeoutSeconds<=0
// leaves deadline_at NULL (unlimited). Returns the resulting deadline (nil = unlimited/unchanged).
func (d *DB) StampFirstRun(id int64, timeoutSeconds int) (*time.Time, error) {
	var deadline *time.Time
	err := d.QueryRow(`
UPDATE tasks
   SET first_run_at = COALESCE(first_run_at, now()),
       deadline_at  = CASE
           WHEN first_run_at IS NOT NULL THEN deadline_at            -- Already stamped: preserve
           WHEN $2 > 0 THEN now() + make_interval(secs => $2)
           ELSE NULL END
 WHERE id = $1
 RETURNING deadline_at`, id, timeoutSeconds).Scan(&deadline)
	return deadline, err
}

// DeleteTask preserves the historical behavior: remove the task and its
// exploration subgraph while retaining global assets.
func (d *DB) DeleteTask(id int64) error {
	_, err := d.DeleteTaskCascade(id, false, false)
	return err
}

// DeleteTaskCascade hard-deletes a task and its exploration subgraph
// (nodes/edges/activity via ON DELETE CASCADE). Optional standalone findings and
// assets owned only by this task are deleted in the same transaction; shared
// assets are retained and only have this task id detached. deleteLLMRecords is
// optional to preserve callers of the original two-option API.
func (d *DB) DeleteTaskCascade(id int64, deleteAssets, deleteFindings bool, deleteLLMRecords ...bool) (TaskDeleteResult, error) {
	deleteRecords := len(deleteLLMRecords) > 0 && deleteLLMRecords[0]
	return d.DeleteTaskCascadePrepared(id, deleteAssets, deleteFindings, deleteRecords, nil)
}

// DeleteTaskCascadePrepared coordinates reversible external deletion with the
// PostgreSQL cascade. When prepare is non-nil, host ownership is resolved and
// prepare is invoked inside this transaction while asset and anchor writers are
// excluded. The locks remain held through commit, closing the window where a
// host could become shared after its traffic had already been staged.
//
// prepare must only stage reversible work. Any returned error or later database
// error rolls PostgreSQL back; the caller remains responsible for rolling back
// external work that its callback staged successfully.
func (d *DB) DeleteTaskCascadePrepared(
	id int64,
	deleteAssets, deleteFindings, deleteLLMRecords bool,
	prepare func(TaskDeletePreparation) error,
) (TaskDeleteResult, error) {
	var result TaskDeleteResult
	tx, err := d.Begin()
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	var expID int64
	if err := tx.QueryRow(`SELECT exploration_id FROM tasks WHERE id=$1 FOR UPDATE`, id).Scan(&expID); err != nil {
		if err == sql.ErrNoRows {
			return result, nil // already gone
		}
		return result, err
	}

	// SHARE ROW EXCLUSIVE conflicts with every INSERT/UPDATE/DELETE on these
	// tables and with another coordinated deletion. Taking both in one fixed order
	// prevents phantoms (a distinct asset row for the same host) as well as new
	// ownership/anchor references until the deletion transaction commits.
	if deleteAssets || prepare != nil {
		if _, err := tx.Exec(`LOCK TABLE assets, exploration_anchors IN SHARE ROW EXCLUSIVE MODE`); err != nil {
			return result, err
		}
	}
	if prepare != nil {
		hosts, err := hostsForTaskDeletion(tx, id, expID)
		if err != nil {
			return result, err
		}
		if err := prepare(TaskDeletePreparation{
			ExplorationID: expID,
			TrafficHosts:  hosts,
		}); err != nil {
			return result, err
		}
	}
	if deleteAssets {
		// Ownership is the union of explicit task_ids and this exploration's anchors.
		// An asset is deletable only when no other live task references it through
		// either mechanism. This covers legacy anchor-only seeds without destroying
		// evidence anchored by another task.
		res, err := tx.Exec(`
WITH candidate_assets AS (
  SELECT id FROM assets WHERE task_ids @> ARRAY[$1::bigint]
  UNION
  SELECT ea.asset_id
  FROM exploration_anchors ea
  JOIN exploration_nodes n ON n.id=ea.node_id
  WHERE n.exploration_id=$2
),
deletable AS (
  SELECT a.id
  FROM assets a
  JOIN candidate_assets c ON c.id=a.id
  WHERE NOT EXISTS (
    SELECT 1 FROM tasks t
    WHERE t.id<>$1 AND t.deleted_at IS NULL AND t.id=ANY(a.task_ids)
  ) AND NOT EXISTS (
    SELECT 1
    FROM exploration_anchors ea
    JOIN exploration_nodes n ON n.id=ea.node_id
    JOIN tasks t ON t.exploration_id=n.exploration_id
    WHERE ea.asset_id=a.id AND t.id<>$1 AND t.deleted_at IS NULL
  )
)
DELETE FROM assets a USING deletable d WHERE a.id=d.id`, id, expID)
		if err != nil {
			return result, err
		}
		result.AssetsDeleted, _ = res.RowsAffected()

		res, err = tx.Exec(`UPDATE assets SET task_ids = array_remove(task_ids, $1) WHERE task_ids @> ARRAY[$1::bigint]`, id)
		if err != nil {
			return result, err
		}
		result.AssetsDetached, _ = res.RowsAffected()
	}
	if deleteFindings {
		res, err := tx.Exec(`DELETE FROM findings WHERE task_id = $1`, id)
		if err != nil {
			return result, err
		}
		result.FindingsDeleted, _ = res.RowsAffected()
	}
	if deleteLLMRecords {
		res, err := tx.Exec(`DELETE FROM llm_records WHERE COALESCE(task_id,'')=$1`, strconv.FormatInt(id, 10))
		if err != nil {
			return result, err
		}
		result.LLMRecordsDeleted, _ = res.RowsAffected()
	}
	// llm_usage (the token metering ledger) is intentionally NOT deleted with the
	// task — it is kept as historical accounting even after the task is gone.
	if _, err := tx.Exec(`DELETE FROM settings WHERE key IN ($1,$2,$3)`, "task_language."+strconv.FormatInt(id, 10), TaskCodeReviewKey(id), TaskAutomationKey(id)); err != nil {
		return TaskDeleteResult{}, err
	}
	if err := releaseTaskIdentity(tx, id, deleteLLMRecords); err != nil {
		return result, err
	}
	if _, err := tx.Exec(`DELETE FROM tasks WHERE id=$1`, id); err != nil {
		return result, err
	}
	if _, err := tx.Exec(`DELETE FROM explorations WHERE id=$1`, expID); err != nil {
		return result, err
	}
	return result, tx.Commit()
}

// TaskCodeReviewKey is the settings key holding a white-box review's target
// (repository, ref, commit, mode) as JSON. It is removed with the task.
func TaskCodeReviewKey(id int64) string {
	return "task_codereview." + strconv.FormatInt(id, 10)
}
