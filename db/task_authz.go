package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Philocyber/counterweight/locale"
)

// Authorization check of a task (package authz): the test accounts the
// operator stores for it, the policy text, and the reports of its runs.
//
// An account's password and token are sealed like every stored credential
// (secrets.go). They are opened in one place only, AuthzAccountSecrets, for
// the runner. No other reader returns them, so they cannot reach the console,
// a model prompt, a task archive or a report. The rows belong to the task and
// are deleted with it.

const (
	MaxAuthzAccounts    = 20
	MaxAuthzPolicyBytes = 256 << 10
	// authzRunsKept is how many reports a task keeps; older ones are dropped.
	authzRunsKept = 20

	maxAuthzRoleRunes = 80
	maxAuthzUsername  = 320
	maxAuthzSecret    = 8192
)

var (
	ErrAuthzTaskNotFound    = locale.NewError("Task not found")
	ErrAuthzAccountNotFound = locale.NewError("Test account not found")
	ErrAuthzRunNotFound     = locale.NewError("Authorization run not found")
	ErrAuthzAccountRole     = locale.NewError("The account needs a role name of at most 80 characters that contains a letter or a digit")
	ErrAuthzAccountKey      = locale.NewError("Another test account of this task has the same key; choose a different role name")
	ErrAuthzAccountLimit    = locale.NewError("This task already has the maximum number of test accounts")
	ErrAuthzAccountField    = locale.NewError("The username, password or token is too long")
	ErrAuthzAccountSecret   = locale.NewError("A password or token must not start or end with a space and must not contain line breaks or other control characters")
	ErrAuthzPolicySize      = locale.NewError("The policy is larger than 256 KiB")
)

// AuthzAccount is a test account as the console sees it: never its secrets,
// only whether each one is set.
type AuthzAccount struct {
	ID          int64  `json:"id"`
	Key         string `json:"key"` // persona name in the policy; fixed at creation
	Role        string `json:"role"`
	Username    string `json:"username"`
	HasPassword bool   `json:"has_password"`
	HasToken    bool   `json:"has_token"`
	// AgentAccess lets the task's agents send requests as this role through
	// the server. They never receive the secrets.
	AgentAccess bool      `json:"agent_access"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// AuthzAccountInput carries the fields of a create or an update. A nil field
// is left as it is; an empty Password or Token removes the stored one.
type AuthzAccountInput struct {
	Role        *string
	Username    *string
	Password    *string
	Token       *string
	AgentAccess *bool
}

// AuthzAccountSecret is an account with its secrets opened, for the runner only.
type AuthzAccountSecret struct {
	Role, Username, Password, Token string
	AgentAccess                     bool
}

// AuthzRun is a stored run. Report is set by AuthzRun and LatestAuthzRun only.
type AuthzRun struct {
	ID         int64           `json:"id"`
	Result     string          `json:"result"`
	ExitCode   int             `json:"exit_code"`
	Target     string          `json:"target"`
	StartedAt  time.Time       `json:"started_at"`
	DurationMS int64           `json:"duration_ms"`
	Counts     json.RawMessage `json:"counts"`
	Report     json.RawMessage `json:"report,omitempty"`
}

// AuthzAccountKey derives the key of an account from its role name: lowercase
// letters and digits, every other run of characters becoming one "_".
// "Admin (tenant A)" gives "admin_tenant_a". It returns "" when the name has
// no letter or digit to build a key from.
func AuthzAccountKey(role string) string {
	var b strings.Builder
	gap := false
	for _, r := range strings.ToLower(role) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if gap && b.Len() > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r)
			gap = false
		} else {
			gap = true
		}
		if b.Len() >= 40 {
			break
		}
	}
	return b.String()
}

func cleanAuthzRole(role string) (string, error) {
	role = strings.TrimSpace(role)
	if role == "" || utf8.RuneCountInString(role) > maxAuthzRoleRunes || strings.ContainsFunc(role, unicode.IsControl) || AuthzAccountKey(role) == "" {
		return "", ErrAuthzAccountRole
	}
	return role, nil
}

// checkAuthzSecret refuses a value the HTTP client would not send exactly as
// stored: it would then not be removed from the output either.
func checkAuthzSecret(v string) error {
	if len(v) > maxAuthzSecret {
		return ErrAuthzAccountField
	}
	if v != strings.TrimSpace(v) || strings.ContainsFunc(v, unicode.IsControl) {
		return ErrAuthzAccountSecret
	}
	return nil
}

func cleanAuthzUsername(username string) (string, error) {
	username = strings.TrimSpace(username)
	if len(username) > maxAuthzUsername || strings.ContainsFunc(username, unicode.IsControl) {
		return "", ErrAuthzAccountField
	}
	return username, nil
}

// CheckAuthzAccounts validates accounts that are about to be created together
// on a new task, without touching the database: the rules of
// CreateAuthzAccount, and that no two of them get the same key.
func CheckAuthzAccounts(inputs []AuthzAccountInput) error {
	if len(inputs) > MaxAuthzAccounts {
		return ErrAuthzAccountLimit
	}
	seen := map[string]bool{}
	for _, in := range inputs {
		if in.Role == nil {
			return ErrAuthzAccountRole
		}
		role, err := cleanAuthzRole(*in.Role)
		if err != nil {
			return err
		}
		if key := AuthzAccountKey(role); seen[key] {
			return ErrAuthzAccountKey
		} else {
			seen[key] = true
		}
		if in.Username != nil {
			if _, err := cleanAuthzUsername(*in.Username); err != nil {
				return err
			}
		}
		for _, secret := range []*string{in.Password, in.Token} {
			if secret == nil {
				continue
			}
			if err := checkAuthzSecret(*secret); err != nil {
				return err
			}
		}
	}
	return nil
}

func authzUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

const authzAccountCols = `id, key, role, username, COALESCE(password,'')<>'', COALESCE(token,'')<>'', agent_access, updated_at`

func scanAuthzAccount(row interface{ Scan(...any) error }) (*AuthzAccount, error) {
	var a AuthzAccount
	if err := row.Scan(&a.ID, &a.Key, &a.Role, &a.Username, &a.HasPassword, &a.HasToken, &a.AgentAccess, &a.UpdatedAt); err != nil {
		return nil, err
	}
	return &a, nil
}

// ListAuthzAccounts returns the test accounts of a task in creation order.
func (d *DB) ListAuthzAccounts(taskID int64) ([]*AuthzAccount, error) {
	rows, err := d.Query(`SELECT `+authzAccountCols+` FROM task_authz_accounts WHERE task_id=$1 ORDER BY id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*AuthzAccount{}
	for rows.Next() {
		a, err := scanAuthzAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// insertAuthzAccount adds an account inside tx and returns its id. The caller
// has made sure the task exists and has room for it.
func insertAuthzAccount(tx *sql.Tx, taskID int64, in AuthzAccountInput) (int64, error) {
	if in.Role == nil {
		return 0, ErrAuthzAccountRole
	}
	role, err := cleanAuthzRole(*in.Role)
	if err != nil {
		return 0, err
	}
	var id int64
	if err := tx.QueryRow(`INSERT INTO task_authz_accounts(task_id, key, role) VALUES ($1,$2,$3) RETURNING id`, taskID, AuthzAccountKey(role), role).Scan(&id); err != nil {
		if authzUniqueViolation(err) {
			return 0, ErrAuthzAccountKey
		}
		return 0, err
	}
	in.Role = nil // stored by the insert
	return id, updateAuthzAccount(tx, taskID, id, in)
}

// insertTaskAuthzAccounts stores the test accounts given with a new task in
// the transaction that creates it, so the task never exists without them.
func insertTaskAuthzAccounts(tx *sql.Tx, taskID int64, accounts []AuthzAccountInput) error {
	if err := CheckAuthzAccounts(accounts); err != nil {
		return err
	}
	for _, in := range accounts {
		if _, err := insertAuthzAccount(tx, taskID, in); err != nil {
			return err
		}
	}
	return nil
}

// CreateAuthzAccount adds a test account to a task. Its key comes from the role name.
func (d *DB) CreateAuthzAccount(taskID int64, in AuthzAccountInput) (*AuthzAccount, error) {
	tx, err := d.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// The lock on the task row orders concurrent creations, so the limit holds.
	var exists bool
	if err := tx.QueryRow(`SELECT true FROM tasks WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, taskID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrAuthzTaskNotFound
		}
		return nil, err
	}
	var n int
	if err := tx.QueryRow(`SELECT count(*) FROM task_authz_accounts WHERE task_id=$1`, taskID).Scan(&n); err != nil {
		return nil, err
	}
	if n >= MaxAuthzAccounts {
		return nil, ErrAuthzAccountLimit
	}
	id, err := insertAuthzAccount(tx, taskID, in)
	if err != nil {
		return nil, err
	}
	a, err := scanAuthzAccount(tx.QueryRow(`SELECT `+authzAccountCols+` FROM task_authz_accounts WHERE id=$1`, id))
	if err != nil {
		return nil, err
	}
	return a, tx.Commit()
}

// UpdateAuthzAccount changes the given fields of an account. The key never changes.
func (d *DB) UpdateAuthzAccount(taskID, id int64, in AuthzAccountInput) (*AuthzAccount, error) {
	tx, err := d.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := updateAuthzAccount(tx, taskID, id, in); err != nil {
		return nil, err
	}
	a, err := scanAuthzAccount(tx.QueryRow(`SELECT `+authzAccountCols+` FROM task_authz_accounts WHERE id=$1 AND task_id=$2`, id, taskID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrAuthzAccountNotFound
		}
		return nil, err
	}
	return a, tx.Commit()
}

func updateAuthzAccount(tx *sql.Tx, taskID, id int64, in AuthzAccountInput) error {
	var found bool
	if err := tx.QueryRow(`SELECT true FROM task_authz_accounts WHERE id=$1 AND task_id=$2 FOR UPDATE`, id, taskID).Scan(&found); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrAuthzAccountNotFound
		}
		return err
	}
	if in.Role != nil {
		role, err := cleanAuthzRole(*in.Role)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE task_authz_accounts SET role=$2, updated_at=now() WHERE id=$1`, id, role); err != nil {
			return err
		}
	}
	if in.Username != nil {
		username, err := cleanAuthzUsername(*in.Username)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE task_authz_accounts SET username=$2, updated_at=now() WHERE id=$1`, id, username); err != nil {
			return err
		}
	}
	if in.AgentAccess != nil {
		if _, err := tx.Exec(`UPDATE task_authz_accounts SET agent_access=$2, updated_at=now() WHERE id=$1`, id, *in.AgentAccess); err != nil {
			return err
		}
	}
	for column, value := range map[string]*string{"password": in.Password, "token": in.Token} {
		if value == nil {
			continue
		}
		if err := checkAuthzSecret(*value); err != nil {
			return err
		}
		var stored any // NULL removes the secret
		if *value != "" {
			s, err := sealSecret("task_authz_accounts", column, id, *value)
			if err != nil {
				return err
			}
			stored = s
		}
		// column is one of the two literals above, never caller input.
		if _, err := tx.Exec(`UPDATE task_authz_accounts SET `+column+`=$2, updated_at=now() WHERE id=$1`, id, stored); err != nil {
			return err
		}
	}
	return nil
}

// DeleteAuthzAccount removes an account and its secrets.
func (d *DB) DeleteAuthzAccount(taskID, id int64) error {
	res, err := d.Exec(`DELETE FROM task_authz_accounts WHERE id=$1 AND task_id=$2`, id, taskID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrAuthzAccountNotFound
	}
	return nil
}

// AuthzAccountSecrets returns the accounts of a task by key with their secrets
// opened. It exists for the runner of the authorization check and must not be
// used to answer an API request, build a prompt or write a report.
func (d *DB) AuthzAccountSecrets(taskID int64) (map[string]AuthzAccountSecret, error) {
	rows, err := d.Query(`SELECT id, key, role, username, COALESCE(password,''), COALESCE(token,''), agent_access FROM task_authz_accounts WHERE task_id=$1`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]AuthzAccountSecret{}
	for rows.Next() {
		var id int64
		var key string
		var a AuthzAccountSecret
		if err := rows.Scan(&id, &key, &a.Role, &a.Username, &a.Password, &a.Token, &a.AgentAccess); err != nil {
			return nil, err
		}
		if a.Password, err = openSecret("task_authz_accounts", "password", id, a.Password); err != nil {
			return nil, err
		}
		if a.Token, err = openSecret("task_authz_accounts", "token", id, a.Token); err != nil {
			return nil, err
		}
		out[key] = a
	}
	return out, rows.Err()
}

// AuthzAgentAccess reports whether a live task has a test account its agents may use.
func (d *DB) AuthzAgentAccess(taskID int64) (bool, error) {
	var ok bool
	err := d.QueryRow(`SELECT EXISTS (SELECT 1 FROM task_authz_accounts a JOIN tasks t ON t.id=a.task_id
WHERE a.task_id=$1 AND a.agent_access AND t.deleted_at IS NULL)`, taskID).Scan(&ok)
	return ok, err
}

// SaveAuthzPolicy stores the policy text of a task as written, valid or not.
func (d *DB) SaveAuthzPolicy(taskID int64, policy string) (time.Time, error) {
	if len(policy) > MaxAuthzPolicyBytes {
		return time.Time{}, ErrAuthzPolicySize
	}
	var at time.Time
	err := d.QueryRow(`INSERT INTO task_authz_policies(task_id, policy) SELECT id, $2 FROM tasks WHERE id=$1 AND deleted_at IS NULL
ON CONFLICT (task_id) DO UPDATE SET policy=EXCLUDED.policy, updated_at=now() RETURNING updated_at`, taskID, policy).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return at, ErrAuthzTaskNotFound
	}
	return at, err
}

// AuthzPolicy returns the stored policy text, or "" and a nil time when there is none.
func (d *DB) AuthzPolicy(taskID int64) (string, *time.Time, error) {
	var policy string
	var at time.Time
	err := d.QueryRow(`SELECT policy, updated_at FROM task_authz_policies WHERE task_id=$1`, taskID).Scan(&policy, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	return policy, &at, nil
}

// AddAuthzRun stores the report of a finished run and drops the oldest ones
// beyond the number a task keeps. A task that was archived or deleted while
// the run was in progress keeps nothing: the report is discarded.
func (d *DB) AddAuthzRun(taskID int64, result string, exitCode int, startedAt time.Time, report []byte) (int64, error) {
	var id int64
	if err := d.QueryRow(`INSERT INTO task_authz_runs(task_id, result, exit_code, started_at, report)
SELECT id, $2, $3, $4, $5::jsonb FROM tasks WHERE id=$1 AND deleted_at IS NULL RETURNING id`,
		taskID, result, exitCode, startedAt, string(report)).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrAuthzTaskNotFound
		}
		return 0, err
	}
	_, err := d.Exec(`DELETE FROM task_authz_runs WHERE task_id=$1 AND id NOT IN (SELECT id FROM task_authz_runs WHERE task_id=$1 ORDER BY id DESC LIMIT $2)`, taskID, authzRunsKept)
	return id, err
}

const authzRunCols = `id, result, exit_code, COALESCE(report->>'target',''), started_at, COALESCE((report->>'duration_ms')::bigint,0), COALESCE(report->'counts','{}'::jsonb)::text`

func scanAuthzRun(row interface{ Scan(...any) error }, extra ...any) (*AuthzRun, error) {
	var r AuthzRun
	var counts string
	if err := row.Scan(append([]any{&r.ID, &r.Result, &r.ExitCode, &r.Target, &r.StartedAt, &r.DurationMS, &counts}, extra...)...); err != nil {
		return nil, err
	}
	r.Counts = json.RawMessage(counts)
	return &r, nil
}

// ListAuthzRuns returns the stored runs of a task, newest first, without their reports.
func (d *DB) ListAuthzRuns(taskID int64) ([]*AuthzRun, error) {
	rows, err := d.Query(`SELECT `+authzRunCols+` FROM task_authz_runs WHERE task_id=$1 ORDER BY id DESC`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*AuthzRun{}
	for rows.Next() {
		r, err := scanAuthzRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AuthzRun returns one run of a task with its report.
func (d *DB) AuthzRun(taskID, id int64) (*AuthzRun, error) {
	return d.authzRunWhere(`task_id=$1 AND id=$2`, taskID, id)
}

// LatestAuthzRun returns the newest run of a task with its report, or nil when it has none.
func (d *DB) LatestAuthzRun(taskID int64) (*AuthzRun, error) {
	r, err := d.authzRunWhere(`task_id=$1 ORDER BY id DESC LIMIT 1`, taskID)
	if errors.Is(err, ErrAuthzRunNotFound) {
		return nil, nil
	}
	return r, err
}

func (d *DB) authzRunWhere(where string, args ...any) (*AuthzRun, error) {
	var report string
	r, err := scanAuthzRun(d.QueryRow(`SELECT `+authzRunCols+`, report::text FROM task_authz_runs WHERE `+where, args...), &report)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrAuthzRunNotFound
		}
		return nil, err
	}
	r.Report = json.RawMessage(report)
	return r, nil
}
