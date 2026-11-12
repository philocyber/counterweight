package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/Philocyber/counterweight/codesrc"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/Philocyber/counterweight/locale"
)

// Assessment types: what kind of work a task is. general, web_app and network
// assess a running system (black box); code_review reads a cloned repository.
const (
	AssessmentGeneral    = "general"
	AssessmentWebApp     = "web_app"
	AssessmentNetwork    = "network"
	AssessmentCodeReview = "code_review"
)

var (
	ErrAssessmentTypeInvalid = locale.NewError("assessment_type must be general, web_app, network or code_review")
	// ErrTaskAlreadyStarted refuses an ordinary type change once the task ran:
	// its records were produced under the old type.
	ErrTaskAlreadyStarted = locale.NewError("The assessment type can be changed only before the task starts")
	// ErrReclassifyNotStarted refuses a reclassification of a task that never ran;
	// an ordinary change applies there.
	ErrReclassifyNotStarted = locale.NewError("Reclassification is only for tasks that already ran; change the type without reclassify")
	// ErrReclassifyNotGeneral limits reclassification to tasks recorded as general.
	ErrReclassifyNotGeneral = locale.NewError("Only legacy general tasks can be reclassified")
	// ErrCodeTargetRequired means a code review has no repository to point at.
	ErrCodeTargetRequired = locale.NewError("A code review needs a repository")
)

// NormalizeAssessmentType validates a type, mapping empty to general so clients
// that do not send the field keep today's behaviour.
func NormalizeAssessmentType(raw string) (string, error) {
	switch v := strings.TrimSpace(raw); v {
	case "":
		return AssessmentGeneral, nil
	case AssessmentGeneral, AssessmentWebApp, AssessmentNetwork, AssessmentCodeReview:
		return v, nil
	default:
		return "", ErrAssessmentTypeInvalid
	}
}

// TaskCodeTarget is the repository a code review task reads. LocalPath is the
// server-side clone and never leaves the server through reports or exports.
type TaskCodeTarget struct {
	TaskID     int64     `json:"task_id"`
	Repository string    `json:"repository"` // owner/name
	URL        string    `json:"url"`
	Ref        string    `json:"ref"`
	CommitSHA  string    `json:"commit_sha"`
	Mode       string    `json:"mode"`
	LocalPath  string    `json:"-"`
	CreatedAt  time.Time `json:"created_at"`
	// Change scopes the review to one change (a pull request or a base/head
	// pair); nil reviews the whole repository.
	Change *codesrc.ChangeSet `json:"change,omitempty"`
}

// marshalChange returns the stored form of a change: the record without its
// diff text, which lives in its own column. A nil change stores NULL and an empty diff.
func marshalChange(c *codesrc.ChangeSet) (record any, diff string, err error) {
	if c == nil {
		return nil, "", nil
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return nil, "", err
	}
	return string(raw), c.Diff, nil
}

// unmarshalChange is the inverse of marshalChange; empty input is no change.
func unmarshalChange(raw []byte, diff string) (*codesrc.ChangeSet, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var c codesrc.ChangeSet
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, err
	}
	c.Diff = diff
	return &c, nil
}

// legacyCodeReview is the JSON once stored under TaskCodeReviewKey.
type legacyCodeReview struct {
	Repository string `json:"repository"`
	URL        string `json:"url"`
	Ref        string `json:"ref"`
	Commit     string `json:"commit"`
	Mode       string `json:"mode"`
}

func (l legacyCodeReview) target(taskID int64) *TaskCodeTarget {
	t := &TaskCodeTarget{TaskID: taskID, Repository: l.Repository, URL: l.URL, Ref: l.Ref, CommitSHA: l.Commit, Mode: l.Mode}
	if t.URL == "" {
		t.URL = "https://github.com/" + t.Repository
	}
	if t.Mode == "" {
		t.Mode = "balanced"
	}
	return t
}

func insertTaskCodeTarget(tx *sql.Tx, taskID int64, t *TaskCodeTarget) error {
	if t == nil || strings.TrimSpace(t.Repository) == "" {
		return ErrCodeTargetRequired
	}
	if _, err := codesrc.ParseRepo(t.Repository); err != nil {
		return err
	}
	parsedMode, err := codesrc.ParseMode(t.Mode)
	if err != nil {
		return err
	}
	mode := string(parsedMode)
	change, changeDiff, err := marshalChange(t.Change)
	if err != nil {
		return err
	}
	err = tx.QueryRow(`
INSERT INTO task_code_targets(task_id, repository, url, ref, commit_sha, mode, local_path, change, change_diff)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9)
ON CONFLICT (task_id) DO UPDATE SET repository=EXCLUDED.repository, url=EXCLUDED.url, ref=EXCLUDED.ref,
 commit_sha=EXCLUDED.commit_sha, mode=EXCLUDED.mode, local_path=EXCLUDED.local_path,
 change=EXCLUDED.change, change_diff=EXCLUDED.change_diff RETURNING created_at`,
		taskID, t.Repository, t.URL, t.Ref, t.CommitSHA, mode, t.LocalPath, change, changeDiff).Scan(&t.CreatedAt)
	if err == nil {
		t.TaskID = taskID
		t.Mode = mode
	}
	return err
}

// GetTaskCodeTarget returns the repository of a code review task, or nil. A
// target still kept in the legacy settings key (the startup migration has not
// moved it yet) is read from there.
func (d *DB) GetTaskCodeTarget(taskID int64) (*TaskCodeTarget, error) {
	var t TaskCodeTarget
	var change []byte
	var changeDiff sql.NullString
	err := d.QueryRow(`SELECT task_id, repository, url, ref, commit_sha, mode, local_path, created_at, change, change_diff
FROM task_code_targets WHERE task_id=$1`, taskID).Scan(&t.TaskID, &t.Repository, &t.URL, &t.Ref, &t.CommitSHA, &t.Mode, &t.LocalPath, &t.CreatedAt, &change, &changeDiff)
	if err == nil {
		if t.Change, err = unmarshalChange(change, changeDiff.String); err != nil {
			return nil, err
		}
		return &t, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	raw, ok, err := d.GetSetting(TaskCodeReviewKey(taskID))
	if err != nil || !ok {
		return nil, err
	}
	var legacy legacyCodeReview
	if json.Unmarshal([]byte(raw), &legacy) != nil || strings.TrimSpace(legacy.Repository) == "" {
		return nil, nil
	}
	return legacy.target(taskID), nil
}

// taskCodeTargets hydrates list responses in one query, including unmigrated
// targets, without issuing a settings lookup for every general task.
func (d *DB) taskCodeTargets(ids []int64) (map[int64]*TaskCodeTarget, error) {
	rows, err := d.Query(`SELECT task.id, row_to_json(target), COALESCE(target.local_path,''), COALESCE(legacy.value,'')
FROM tasks task
LEFT JOIN task_code_targets target ON target.task_id=task.id
LEFT JOIN settings legacy ON legacy.key='task_codereview.' || task.id::text
WHERE task.id=ANY($1::bigint[])`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]*TaskCodeTarget)
	for rows.Next() {
		var id int64
		var raw []byte
		var path, old string
		if err := rows.Scan(&id, &raw, &path, &old); err != nil {
			return nil, err
		}
		if len(raw) > 0 && string(raw) != "null" {
			var target TaskCodeTarget
			if err := json.Unmarshal(raw, &target); err != nil {
				return nil, err
			}
			target.LocalPath = path
			if target.Change != nil { // list responses carry the summary, not the file list
				target.Change.Files = nil
			}
			out[id] = &target
		} else if old != "" {
			var target legacyCodeReview
			if json.Unmarshal([]byte(old), &target) == nil && strings.TrimSpace(target.Repository) != "" {
				out[id] = target.target(id)
			}
		}
	}
	return out, rows.Err()
}

// migrateLegacyCodeReviewTargets moves every task_codereview.<id> settings key
// into task_code_targets, marks the task code_review with asset coverage off,
// and deletes each migrated key. Orphaned keys are removed; unreadable records
// are retained for recovery. Successful migrations are idempotent.
func (d *DB) migrateLegacyCodeReviewTargets() error {
	rows, err := d.Query(`SELECT key, value FROM settings WHERE key LIKE 'task\_codereview.%'`)
	if err != nil {
		return err
	}
	type entry struct {
		key, value string
		id         int64
	}
	var entries []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.key, &e.value); err != nil {
			rows.Close()
			return err
		}
		id, err := strconv.ParseInt(strings.TrimPrefix(e.key, "task_codereview."), 10, 64)
		if err != nil || id <= 0 {
			continue
		}
		e.id = id
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, e := range entries {
		if err := d.migrateLegacyCodeReviewTarget(e.id, e.key, e.value); err != nil {
			return locale.Errorf("migrate code review target of task %d: %w", e.id, err)
		}
	}
	return nil
}

func (d *DB) migrateLegacyCodeReviewTarget(taskID int64, key, value string) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var legacy legacyCodeReview
	valid := json.Unmarshal([]byte(value), &legacy) == nil && strings.TrimSpace(legacy.Repository) != ""
	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM tasks WHERE id=$1)`, taskID).Scan(&exists); err != nil {
		return err
	}
	if exists && valid {
		if _, err := tx.Exec(`INSERT INTO task_code_targets(task_id, repository, url, ref, commit_sha, mode)
VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (task_id) DO NOTHING`,
			taskID, legacy.Repository, legacy.target(taskID).URL, legacy.Ref, legacy.Commit, legacy.target(taskID).Mode); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE tasks SET assessment_type='code_review', assessment_type_explicit=true, coverage_enabled=false WHERE id=$1`, taskID); err != nil {
			return err
		}
	} else if exists && !valid {
		log.Printf("[migrate] retaining unreadable code review target of task %d", taskID)
		return nil
	}
	if _, err := tx.Exec(`DELETE FROM settings WHERE key=$1`, key); err != nil {
		return err
	}
	return tx.Commit()
}

// AssessmentTypeChange is a request to change a task's type. Reclassify relabels
// a task that already ran (only from general); Target is the repository when
// the new type is code_review.
type AssessmentTypeChange struct {
	Type       string
	Reclassify bool
	Target     *TaskCodeTarget
	// EngineStarted is the live runtime's view: the task's engine ran in this process.
	EngineStarted bool
}

// taskStartedSQL is true once a task has started: it is running or terminal, its clock
// was stamped, or its exploration has any activity (goal decomposition writes
// the first one when the engine admits the task). Queued tasks that never got
// a slot, and tasks paused before admission, have none.
const taskStartedSQL = `(status IN ('running','done','failed','timeout') OR first_run_at IS NOT NULL
 OR EXISTS (SELECT 1 FROM activity WHERE activity.exploration_id=tasks.exploration_id))`

// TaskStarted reports whether a task has started (see taskStartedSQL).
func (d *DB) TaskStarted(taskID int64) (bool, error) {
	var started bool
	err := d.QueryRow(`SELECT `+taskStartedSQL+` FROM tasks WHERE id=$1`, taskID).Scan(&started)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return started, err
}

// ChangeTaskAssessmentType applies the type-change rules atomically and returns
// the updated task (nil when it does not exist):
//   - ordinary change: only before the task starts; code_review needs a target;
//   - reclassification: only after the task ran, only from general; moving to
//     code_review records the repository and turns asset coverage off.
func (d *DB) ChangeTaskAssessmentType(taskID int64, change AssessmentTypeChange) (*Task, error) {
	newType, err := NormalizeAssessmentType(change.Type)
	if err != nil {
		return nil, err
	}
	tx, err := d.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var current string
	var started, explicit bool
	err = tx.QueryRow(`SELECT assessment_type, `+taskStartedSQL+`, assessment_type_explicit FROM tasks WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, taskID).Scan(&current, &started, &explicit)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	started = started || change.EngineStarted
	if newType != current {
		if change.Reclassify {
			switch {
			case !started:
				return nil, ErrReclassifyNotStarted
			case current != AssessmentGeneral || explicit:
				return nil, ErrReclassifyNotGeneral
			}
			if newType == AssessmentCodeReview {
				if err := insertTaskCodeTarget(tx, taskID, change.Target); err != nil {
					return nil, err
				}
			}
		} else {
			switch {
			case started:
				return nil, ErrTaskAlreadyStarted
			}
		}
	}
	if newType != current && !change.Reclassify && newType == AssessmentCodeReview {
		if err := insertTaskCodeTarget(tx, taskID, change.Target); err != nil {
			return nil, err
		}
	}
	if newType != AssessmentCodeReview {
		if _, err := tx.Exec(`DELETE FROM task_code_targets WHERE task_id=$1`, taskID); err != nil {
			return nil, err
		}
	}
	task, err := scanTask(tx.QueryRow(`UPDATE tasks SET assessment_type=$2, assessment_type_explicit=true,
 coverage_enabled=CASE WHEN $2='code_review' THEN false ELSE coverage_enabled END
WHERE id=$1 RETURNING `+taskCols, taskID, newType))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if err := d.hydrateTaskContext(task); err != nil {
		return nil, err
	}
	return task, nil
}
