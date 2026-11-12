package db

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Philocyber/counterweight/sealed"
)

func sp(s string) *string { return &s }

func authzTask(t *testing.T, d *DB) int64 {
	t.Helper()
	task, err := d.CreateTask("authorization check fixture", "verify stored test accounts", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })
	return task.ID
}

func rawAuthzSecrets(t *testing.T, d *DB, id int64) (password, token string) {
	t.Helper()
	if err := d.QueryRow(`SELECT COALESCE(password,''), COALESCE(token,'') FROM task_authz_accounts WHERE id=$1`, id).Scan(&password, &token); err != nil {
		t.Fatal(err)
	}
	return password, token
}

func TestAuthzAccountKey(t *testing.T) {
	for role, want := range map[string]string{
		"Admin (tenant A)":      "admin_tenant_a",
		"  user_b  ":            "user_b",
		"Soporte Nivel 2":       "soporte_nivel_2",
		"--- ¿? ---":            "",
		"Ünïcode Röle":          "n_code_r_le",
		strings.Repeat("a", 90): strings.Repeat("a", 40),
	} {
		if got := AuthzAccountKey(role); got != want {
			t.Errorf("AuthzAccountKey(%q) = %q, want %q", role, got, want)
		}
	}
}

func TestAuthzAccountsAreSealedAndWriteOnly(t *testing.T) {
	d := openSecretsTestDB(t)
	taskID := authzTask(t, d)
	const password, token = "fixture-account-password", "fixture-account-bearer-value"

	a, err := d.CreateAuthzAccount(taskID, AuthzAccountInput{Role: sp("  Admin (tenant A) "), Username: sp(" root@tenant-a.test "), Password: sp(password), Token: sp(token)})
	if err != nil {
		t.Fatal(err)
	}
	if a.Key != "admin_tenant_a" || a.Role != "Admin (tenant A)" || a.Username != "root@tenant-a.test" || !a.HasPassword || !a.HasToken {
		t.Fatalf("created: %+v", a)
	}

	// At rest: sealed, and bound to the row and the column.
	rawPassword, rawToken := rawAuthzSecrets(t, d, a.ID)
	if !sealed.IsSealed(rawPassword) || !sealed.IsSealed(rawToken) || strings.Contains(rawPassword+rawToken, "fixture-account") {
		t.Fatalf("secrets stored in plaintext: %q %q", rawPassword, rawToken)
	}
	if _, err := d.Exec(`UPDATE task_authz_accounts SET password=$2 WHERE id=$1`, a.ID, rawToken); err != nil {
		t.Fatal(err)
	}
	if _, err := d.AuthzAccountSecrets(taskID); err == nil {
		t.Fatal("a ciphertext moved to another column must not open")
	}
	if _, err := d.Exec(`UPDATE task_authz_accounts SET password=$2 WHERE id=$1`, a.ID, rawPassword); err != nil {
		t.Fatal(err)
	}

	// What the console can read never holds a secret.
	list, err := d.ListAuthzAccounts(taskID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %v", list, err)
	}
	js, _ := json.Marshal(list)
	if strings.Contains(string(js), "fixture-account") || strings.Contains(string(js), sealed.Prefix) {
		t.Fatalf("the account list exposes a secret: %s", js)
	}

	// The runner gets them back, by key.
	secrets, err := d.AuthzAccountSecrets(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if got := secrets["admin_tenant_a"]; got != (AuthzAccountSecret{Role: "Admin (tenant A)", Username: "root@tenant-a.test", Password: password, Token: token}) {
		t.Fatalf("opened: %+v", got)
	}

	// Update: nil keeps, a value replaces, "" removes. The key never changes.
	a, err = d.UpdateAuthzAccount(taskID, a.ID, AuthzAccountInput{Role: sp("Platform owner"), Token: sp("")})
	if err != nil {
		t.Fatal(err)
	}
	if a.Key != "admin_tenant_a" || a.Role != "Platform owner" || !a.HasPassword || a.HasToken {
		t.Fatalf("updated: %+v", a)
	}
	secrets, _ = d.AuthzAccountSecrets(taskID)
	if got := secrets["admin_tenant_a"]; got.Password != password || got.Token != "" {
		t.Fatalf("after update: %+v", got)
	}
	if _, rawToken = rawAuthzSecrets(t, d, a.ID); rawToken != "" {
		t.Fatalf("a removed token left %q behind", rawToken)
	}

	// The startup migration sees these values, so a lost key is noticed.
	headers, err := storedSecretHeaders(context.Background(), d.DB)
	if err != nil || len(headers) == 0 {
		t.Fatalf("headers: %v %v", headers, err)
	}
}

func TestAuthzAccountValidationAndScoping(t *testing.T) {
	d := openSecretsTestDB(t)
	taskID, otherTask := authzTask(t, d), authzTask(t, d)

	a, err := d.CreateAuthzAccount(taskID, AuthzAccountInput{Role: sp("User B")})
	if err != nil || a.HasPassword || a.HasToken || a.Username != "" {
		t.Fatalf("minimal account: %+v %v", a, err)
	}
	for name, tc := range map[string]struct {
		in   AuthzAccountInput
		want error
	}{
		"same key":           {AuthzAccountInput{Role: sp("user-b")}, ErrAuthzAccountKey},
		"no role":            {AuthzAccountInput{}, ErrAuthzAccountRole},
		"role without a key": {AuthzAccountInput{Role: sp("???")}, ErrAuthzAccountRole},
		"role too long":      {AuthzAccountInput{Role: sp(strings.Repeat("r", 81))}, ErrAuthzAccountRole},
		"trailing newline":   {AuthzAccountInput{Role: sp("C"), Password: sp("secret-value\n")}, ErrAuthzAccountSecret},
		"leading space":      {AuthzAccountInput{Role: sp("D"), Token: sp(" secret-value")}, ErrAuthzAccountSecret},
		"secret too long":    {AuthzAccountInput{Role: sp("E"), Token: sp(strings.Repeat("t", 8193))}, ErrAuthzAccountField},
		"username too long":  {AuthzAccountInput{Role: sp("F"), Username: sp(strings.Repeat("u", 321))}, ErrAuthzAccountField},
	} {
		if _, err := d.CreateAuthzAccount(taskID, tc.in); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", name, err, tc.want)
		}
	}
	// A refused creation leaves nothing behind.
	if list, _ := d.ListAuthzAccounts(taskID); len(list) != 1 {
		t.Fatalf("accounts after refused creations: %d", len(list))
	}
	if _, err := d.CreateAuthzAccount(1<<60, AuthzAccountInput{Role: sp("Ghost")}); !errors.Is(err, ErrAuthzTaskNotFound) {
		t.Errorf("unknown task: %v", err)
	}

	// An account is reachable only through its own task.
	if _, err := d.UpdateAuthzAccount(otherTask, a.ID, AuthzAccountInput{Role: sp("Hijacked")}); !errors.Is(err, ErrAuthzAccountNotFound) {
		t.Errorf("update through another task: %v", err)
	}
	if err := d.DeleteAuthzAccount(otherTask, a.ID); !errors.Is(err, ErrAuthzAccountNotFound) {
		t.Errorf("delete through another task: %v", err)
	}
	if s, _ := d.AuthzAccountSecrets(otherTask); len(s) != 0 {
		t.Errorf("another task sees %d accounts", len(s))
	}

	// The limit.
	for i := 1; i < MaxAuthzAccounts; i++ {
		if _, err := d.CreateAuthzAccount(taskID, AuthzAccountInput{Role: sp("Role " + strings.Repeat("x", i))}); err != nil {
			t.Fatalf("account %d: %v", i, err)
		}
	}
	if _, err := d.CreateAuthzAccount(taskID, AuthzAccountInput{Role: sp("One too many")}); !errors.Is(err, ErrAuthzAccountLimit) {
		t.Errorf("limit: %v", err)
	}

	if err := d.DeleteAuthzAccount(taskID, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := d.DeleteAuthzAccount(taskID, a.ID); !errors.Is(err, ErrAuthzAccountNotFound) {
		t.Errorf("second delete: %v", err)
	}
}

func TestAuthzPolicyRunsAndTaskDeletion(t *testing.T) {
	d := openSecretsTestDB(t)
	task, err := d.CreateTask("authorization check fixture", "deleted with its accounts", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	taskID := task.ID
	t.Cleanup(func() { _ = d.DeleteTask(taskID) })

	if policy, at, err := d.AuthzPolicy(taskID); err != nil || policy != "" || at != nil {
		t.Fatalf("no policy yet: %q %v %v", policy, at, err)
	}
	if _, err := d.SaveAuthzPolicy(taskID, "version: 1\n"); err != nil {
		t.Fatal(err)
	}
	at, err := d.SaveAuthzPolicy(taskID, "version: 1\n# edited\n")
	if err != nil || at.IsZero() {
		t.Fatal(err)
	}
	if policy, got, err := d.AuthzPolicy(taskID); err != nil || policy != "version: 1\n# edited\n" || got == nil {
		t.Fatalf("policy: %q %v %v", policy, got, err)
	}
	if _, err := d.SaveAuthzPolicy(taskID, strings.Repeat("#", MaxAuthzPolicyBytes+1)); !errors.Is(err, ErrAuthzPolicySize) {
		t.Errorf("oversized policy: %v", err)
	}
	if _, err := d.SaveAuthzPolicy(1<<60, "version: 1\n"); !errors.Is(err, ErrAuthzTaskNotFound) {
		t.Errorf("unknown task: %v", err)
	}

	if r, err := d.LatestAuthzRun(taskID); err != nil || r != nil {
		t.Fatalf("no run yet: %+v %v", r, err)
	}
	started := time.Now().UTC().Truncate(time.Second)
	var last int64
	for i := 0; i < authzRunsKept+3; i++ {
		report := `{"target":"https://staging.example.com","duration_ms":840,"counts":{"cases":4,"checks":16,"passed":14,"failed":2,"inconclusive":0},"cases":[]}`
		if last, err = d.AddAuthzRun(taskID, "fail", 1, started, []byte(report)); err != nil {
			t.Fatal(err)
		}
	}
	runs, err := d.ListAuthzRuns(taskID)
	if err != nil || len(runs) != authzRunsKept || runs[0].ID != last {
		t.Fatalf("runs kept: %d, newest %d, want %d: %v", len(runs), runs[0].ID, last, err)
	}
	if r := runs[0]; r.Result != "fail" || r.ExitCode != 1 || r.Target != "https://staging.example.com" || r.DurationMS != 840 || !strings.Contains(string(r.Counts), `"failed": 2`) || r.Report != nil {
		t.Fatalf("summary: %+v", r)
	}
	full, err := d.AuthzRun(taskID, last)
	if err != nil || !strings.Contains(string(full.Report), `"cases"`) {
		t.Fatalf("run: %+v %v", full, err)
	}
	if latest, err := d.LatestAuthzRun(taskID); err != nil || latest == nil || latest.ID != last {
		t.Fatalf("latest: %+v %v", latest, err)
	}
	if _, err := d.AuthzRun(taskID+1, last); !errors.Is(err, ErrAuthzRunNotFound) {
		t.Errorf("run through another task: %v", err)
	}

	// A task that was archived meanwhile (its row stays as a stub) takes no new
	// account, policy or run: a run that ends after the archive stores nothing.
	stub := authzTask(t, d)
	if _, err := d.Exec(`UPDATE tasks SET archived_at=now(), deleted_at=now() WHERE id=$1`, stub); err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddAuthzRun(stub, "pass", 0, started, []byte(`{"counts":{}}`)); !errors.Is(err, ErrAuthzTaskNotFound) {
		t.Errorf("run stored for an archived task: %v", err)
	}
	if _, err := d.SaveAuthzPolicy(stub, "version: 1\n"); !errors.Is(err, ErrAuthzTaskNotFound) {
		t.Errorf("policy stored for an archived task: %v", err)
	}
	if _, err := d.CreateAuthzAccount(stub, AuthzAccountInput{Role: sp("Late")}); !errors.Is(err, ErrAuthzTaskNotFound) {
		t.Errorf("account stored for an archived task: %v", err)
	}

	// Deleting the task removes the accounts, the policy and the runs.
	if _, err := d.CreateAuthzAccount(taskID, AuthzAccountInput{Role: sp("Admin"), Password: sp("fixture-account-password")}); err != nil {
		t.Fatal(err)
	}
	if err := d.DeleteTask(taskID); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"task_authz_accounts", "task_authz_policies", "task_authz_runs"} {
		var n int
		if err := d.QueryRow(`SELECT count(*) FROM `+table+` WHERE task_id=$1`, taskID).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s keeps %d rows of a deleted task (%v)", table, n, err)
		}
	}
}

// A database whose key was lost discards the account secrets on reset, like
// every other stored credential, instead of keeping values nobody can open.
func TestAuthzAccountSecretsFollowKeyReset(t *testing.T) {
	d := openSecretsTestDB(t)
	taskID := authzTask(t, d)
	a, err := d.CreateAuthzAccount(taskID, AuthzAccountInput{Role: sp("Admin"), Password: sp("fixture-account-password")})
	if err != nil {
		t.Fatal(err)
	}
	other, err := sealed.New("another key", []byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback() // only observed, never committed to the shared test database
	// Without reset the value is left alone.
	if err := migrateAuthzAccountSecrets(ctx, tx, &resealer{k: other}); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := tx.QueryRow(`SELECT count(*) FROM task_authz_accounts WHERE id=$1 AND password LIKE 'cwenc:%'`, a.ID).Scan(&left); err != nil || left != 1 {
		t.Fatalf("value changed without reset: %d %v", left, err)
	}
	r := &resealer{k: other, reset: true}
	if err := migrateAuthzAccountSecrets(ctx, tx, r); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(`SELECT count(*) FROM task_authz_accounts WHERE id=$1 AND password IS NOT NULL`, a.ID).Scan(&left); err != nil || left != 0 || r.cleared == 0 {
		t.Fatalf("unreadable secret kept after reset: %d left, %d cleared, %v", left, r.cleared, err)
	}
}
