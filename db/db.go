// Package db is Counterweight's PostgreSQL data source, replacing the legacy single-file graph SQLite store.
// It opens connections, applies the schema, and seeds built-in agents and variable catalogs.
package db

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"github.com/Philocyber/counterweight/locale"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Philocyber/counterweight/config"
	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib" // pgx database/sql driver ("pgx")
)

//go:embed schema.sql
var schemaSQL string

// Pool bounds keep bursts from the planner, workers and streams below the
// server's default max_connections (100) and avoid reconnect churn.
const (
	maxOpenConns    = 40
	maxIdleConns    = 20
	connMaxIdleTime = 5 * time.Minute
)

const schemaMigrationLockKey int64 = 7337741001

var schemaDeadlockRetryDelays = [...]time.Duration{
	100 * time.Millisecond,
	250 * time.Millisecond,
	500 * time.Millisecond,
	time.Second,
}

type schemaExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func isPostgresDeadlock(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "40P01"
}

func applySchemaWithRetry(ctx context.Context, execer schemaExecer, sleep func(time.Duration)) error {
	for attempt := 0; ; attempt++ {
		if _, err := execer.ExecContext(ctx, schemaSQL); err != nil {
			if !isPostgresDeadlock(err) || attempt >= len(schemaDeadlockRetryDelays) {
				return err
			}
			sleep(schemaDeadlockRetryDelays[attempt])
			continue
		}
		return nil
	}
}

// withSchemaMigrationLock pins the session-level lock to one checked-out
// connection. Running pg_advisory_lock through *sql.DB is incorrect because a
// later schema or unlock call may use a different pooled PostgreSQL session.
func withSchemaMigrationLock(ctx context.Context, sqlDB *sql.DB, action func(*sql.Conn) error) (err error) {
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, schemaMigrationLockKey); err != nil {
		return locale.Errorf("advisory lock: %w", err)
	}
	defer func() {
		if _, unlockErr := conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, schemaMigrationLockKey); unlockErr != nil && err == nil {
			err = locale.Errorf("advisory unlock: %w", unlockErr)
		}
	}()
	return action(conn)
}

// coordinateWithSchemaMigration makes long, multi-table archive transactions
// mutually exclusive with startup DDL while allowing ordinary runtime queries
// to continue normally.
func coordinateWithSchemaMigration(tx *sql.Tx) error {
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock($1)`, schemaMigrationLockKey); err != nil {
		return locale.Errorf("coordinate with schema migration: %w", err)
	}
	return nil
}

// DSN resolves the PostgreSQL connection string and reports where it came from.
// Precedence: env COUNTERWEIGHT_PG_DSN > config file (config.json). There is no
// built-in default — it errors if neither source is configured. Test binaries
// get the "_test" database instead (see testDatabaseDSN).
func DSN() (dsn, source string, err error) {
	dsn, source, err = config.PostgresDSN()
	if err != nil || !testing.Testing() {
		return dsn, source, err
	}
	dsn, err = testDatabaseDSN(dsn)
	return dsn, "test database", err
}

// DB wraps the shared *sql.DB. PG handles its own connection pool + concurrency
// (MVCC), so unlike the old SQLite store there is no process-wide write mutex.
type DB struct{ *sql.DB }

// ensureDatabase connects to the postgres system database and creates the target
// database if it does not exist. dsn must be a postgres:// URL.
func ensureDatabase(dsn string) error {
	u, err := url.Parse(dsn)
	if err != nil {
		return nil // unparseable DSN — let the normal Open fail with a clear error
	}
	dbName := strings.TrimPrefix(u.Path, "/")
	if dbName == "" || dbName == "postgres" {
		return nil
	}
	// connect to the postgres maintenance database instead
	adminDSN := *u
	adminDSN.Path = "/postgres"
	admin, err := sql.Open("pgx", adminDSN.String())
	if err != nil {
		return nil // best-effort; let Open surface the real error
	}
	defer admin.Close()
	if err := admin.Ping(); err != nil {
		return nil
	}
	var exists bool
	_ = admin.QueryRow(`SELECT true FROM pg_database WHERE datname=$1`, dbName).Scan(&exists)
	if !exists {
		if _, err := admin.Exec(`CREATE DATABASE "` + dbName + `"`); err != nil {
			return locale.Errorf("create database: %w", connectionDiagnostic(err))
		}
	}
	return nil
}

// Open connects, applies the schema (idempotent), loads the credential
// encryption key and seals any plaintext credentials, then seeds builtin rows.
func Open(dsn string) (*DB, error) {
	if err := refuseNonTestDatabase(dsn); err != nil {
		return nil, err
	}
	if err := ensureDatabase(dsn); err != nil {
		return nil, err
	}
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, locale.Errorf("open postgres (%s): %w", config.Redact(dsn), connectionDiagnostic(err))
	}
	sqlDB.SetMaxOpenConns(maxOpenConns)
	idleConns, idleTime := maxIdleConns, connMaxIdleTime
	if testing.Testing() {
		// A test binary opens hundreds of short-lived pools. Keeping the production
		// number of idle connections in each one exhausts the server's connection limit.
		idleConns, idleTime = 2, 5*time.Second
	}
	sqlDB.SetMaxIdleConns(idleConns)
	sqlDB.SetConnMaxIdleTime(idleTime)
	if err := sqlDB.Ping(); err != nil {
		sqlDB.Close()
		return nil, locale.Errorf("ping postgres (%s): %w", config.Redact(dsn), connectionDiagnostic(err))
	}
	d := &DB{sqlDB}
	// pgx runs multi-statement Exec via the simple protocol when there are no args.
	// Keep the dedicated lock connection checked out until both DDL and seeding
	// finish so concurrent application instances cannot initialize out of order.
	err = withSchemaMigrationLock(context.Background(), sqlDB, func(conn *sql.Conn) error {
		if err := applySchemaWithRetry(context.Background(), conn, time.Sleep); err != nil {
			return locale.Errorf("apply schema: %w", err)
		}
		keys, err := initSecrets(context.Background(), conn, loadSecretKeys, resetCredentialsRequested())
		if err != nil {
			return err
		}
		secretKeys.Store(keys)
		if err := d.migrateLegacyCodeReviewTargets(); err != nil {
			return err
		}
		if err := d.seedBuiltins(); err != nil {
			return locale.Errorf("seed builtins: %w", err)
		}
		return nil
	})
	if err != nil {
		sqlDB.Close()
		return nil, err
	}
	return d, nil
}

// builtinAgent describes one of the fixed agents and its prompt-variable catalog.
type builtinAgent struct {
	key, name, role, desc string
	vars                  []promptVar
	interactiveShell      bool // Initial interactive-shell default; ON CONFLICT preserves subsequent user toggles.
	runSeconds            *int // Initial per-run wall-clock limit in seconds; nil uses the seed default (1200), 0 is unlimited.
}

type promptVar struct{ name, desc, example, source string }

// intp returns a pointer to v for explicit optional builtinAgent fields such as runSeconds.
func intp(v int) *int { return &v }

// builtinAgents mirrors design section 5(a). Built-in tools are not persisted; seed only agents and variables.
// The interactive_shell_default_v1 block below enables planner/worker/mainagent/auto by default
// while preserving later toggles. interactiveShell here is for new agents that should start enabled.
var builtinAgents = []builtinAgent{
	{"goals", "Goal decomposition", "goals", "Break a penetration-testing objective into independent, verifiable subgoals.", []promptVar{
		{"EngagementDescription", "Task description (target/background)", "Test the example.com website", "exploration"},
		// Now is a global runtime variable (see server.globalPromptVars), no longer repeated in
		// each agent catalog; otherwise withGlobalVars would append a duplicate name.
	}, false, nil},
	{"planner", "Planner", "planner", "Review the situation and goals, adding exploration intents only for genuinely uncovered directions (one planning loop per task).", []promptVar{
		{"Goal", "Overall task objective", "Obtain administrator access to example.com", "exploration"},
		{"AssetSummary", "Asset count/type distribution summary (optional)", "domain:3 ip:5 site:2", "distilled"},
	}, false, nil},
	{"mainagent", "Main agent", "main", "Human interface: observe progress and turn user intent into hints or high-priority intents.", []promptVar{
		{"Goal", "Current task objective", "Obtain administrator access to example.com", "exploration"},
		{"AssetSummary", "Initial situation summary (optional)", "domain:3 ip:5", "distilled"},
		{"FindingsSummary", "Confirmed vulnerability summary (optional)", "high:1 medium:2", "distilled"},
	}, false, nil},
	{"worker", "Worker", "worker", "Claim and execute one intent, write facts and vulnerabilities back to the knowledge graph, then stop.", []promptVar{
		{"ProxyAddr", "Recording proxy address (controls conditional prompt text)", "127.0.0.1:8080", "runtime"},
		{"WorkerName", "Worker identity (optional)", "worker-1", "runtime"},
	}, false, nil},
	// Auto is the built-in platform-operations agent, driven by chat rather than the penetration-testing orchestration loop.
	{"auto", "Auto", "assistant", "Platform assistant: manage tasks (create, inspect, pause, hint) and assets, and create or edit skills, custom tools, and MCP servers.", nil, false, nil},
	// The standalone penetration-testing agent handles reconnaissance through wrap-up via chat, planning, executing, and validating independently. Interactive shell is enabled by default.
	{"pentest", "Penetration testing", "assistant", "Independent penetration-testing agent: handles reconnaissance, attack-surface discovery, exploitation, verification, and wrap-up with its own planning, execution, and adversarial validation.", nil, true, intp(0)},
	// The standalone white-box security code review agent reviews cloned source (no live
	// target) and records findings whose evidence is code. Interactive shell enabled for
	// Read/Grep/Glob over the tree. No template vars: its prompt interpolates none.
	{"codereview", "Security code review", "assistant", "White-box security code review agent: reviews cloned source code, traces source-to-sink, validates disprove-first, and records findings with code evidence. No live-target attacks.", nil, true, intp(0)},
	// Planner and worker of code_review tasks: same roles as planner/worker, with
	// source-review prompts and a read-only tool set enforced by the agent package.
	{CodeReviewPlannerAgentKey, "Code review planner", "planner", "Plans a code review task: maps the repository and adds review directions (files, entry points, data flows) only for uncovered areas.", []promptVar{
		{"Goal", "Overall task objective", "Review acme/app for security vulnerabilities", "exploration"},
	}, false, nil},
	{CodeReviewWorkerAgentKey, "Code review worker", "worker", "Claims one review direction, reads the cloned source, records facts and findings with file and line evidence, then stops.", nil, false, nil},
}

// Agent keys of the planner and worker that run code_review tasks.
const (
	CodeReviewPlannerAgentKey = "codereview_planner"
	CodeReviewWorkerAgentKey  = "codereview_worker"
)

// seedBuiltins inserts built-ins and updates only exact bundled metadata defaults.
// Localization must never overwrite customized names, descriptions, or examples.
func (d *DB) seedBuiltins() error {
	for _, a := range builtinAgents {
		var agentID int64
		err := d.QueryRow(`
INSERT INTO agents(key, name, description, role, builtin, enabled, interactive_shell, run_seconds)
VALUES ($1, $2, NULLIF($3,''), $4, true, true, $5, COALESCE($6, 1200))
ON CONFLICT (key) DO UPDATE SET
 name = CASE WHEN agents.name IN (EXCLUDED.name, $7) THEN EXCLUDED.name ELSE agents.name END,
 description = CASE WHEN agents.description IN (EXCLUDED.description, $8) THEN EXCLUDED.description ELSE agents.description END
RETURNING id`, a.key, a.name, a.desc, a.role, a.interactiveShell, a.runSeconds, legacyDefaultMetadata(a.name), legacyDefaultMetadata(a.desc)).Scan(&agentID)
		if err != nil {
			return locale.Errorf("agent %s: %w", a.key, err)
		}
		for _, v := range a.vars {
			if _, err := d.Exec(`
INSERT INTO agent_prompt_vars(agent_id, var_name, description, example, source)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (agent_id, var_name) DO UPDATE SET
 description = CASE WHEN agent_prompt_vars.description IN (EXCLUDED.description, $6) THEN EXCLUDED.description ELSE agent_prompt_vars.description END,
 example = CASE WHEN agent_prompt_vars.example IN (EXCLUDED.example, $7) THEN EXCLUDED.example ELSE agent_prompt_vars.example END,
 source = EXCLUDED.source`,
				agentID, v.name, v.desc, v.example, v.source, legacyDefaultMetadata(v.desc), legacyDefaultMetadata(v.example)); err != nil {
				return locale.Errorf("agent %s var %s: %w", a.key, v.name, err)
			}
		}
	}
	// Drop catalog entries for variables that were renamed, so the white-list no
	// longer advertises a name templates can't resolve (EngagementTitle→Description).
	// After promoting Now from agent catalogs to a global runtime variable, remove the old goals
	// catalog entry to avoid duplicate frontend variable keys.
	if _, err := d.Exec(`DELETE FROM agent_prompt_vars WHERE var_name IN ('EngagementTitle', 'CoverageGaps', 'Now')`); err != nil {
		return locale.Errorf("cleanup renamed vars: %w", err)
	}
	// Default-on interactive_shell for the runtime agents (planner/worker/mainagent/auto)
	// ONCE — respects a later user toggle-off (guarded by a settings flag). goals(one-shot
	// decomposer) stays off. Runs after the column exists (schema applied before seed).
	if v, _, _ := d.GetSetting("interactive_shell_default_v1"); v != "true" {
		if _, err := d.Exec(`UPDATE agents SET interactive_shell=true WHERE key IN ('planner','worker','mainagent','auto')`); err != nil {
			return locale.Errorf("seed interactive_shell defaults: %w", err)
		}
		_ = d.SetSetting("interactive_shell_default_v1", "true")
	}
	// Seed the built-in browser (Playwright) MCP once — DISABLED by default (users
	// enable it when needed), no proxy by default. The traffic-capture toggle injects/strips
	// the recording proxy + CA at runtime (server.Manager.syncBrowserMCPProxy).
	// Insert only if absent so we never clobber user edits (args/env/enabled/
	// visibility) on restart.
	if _, err := d.Exec(`
INSERT INTO mcp_servers(name, transport, command, args, env, enabled)
VALUES ('browser', 'stdio', 'npx', $1, '{}', false)
ON CONFLICT (name) DO NOTHING`,
		`["@playwright/mcp","--headless"]`); err != nil {
		return locale.Errorf("seed browser mcp: %w", err)
	}
	// NOTE: the placeholder ScopeSentry data-source MCP (empty URL + empty X-API-Key,
	// disabled) is seeded directly in schema.sql §F so a raw `psql < schema.sql` init
	// also gets it. schema.sql is Exec'd on every startup, so it stays idempotent.
	if err := d.seedBuiltinSkillVisibility(); err != nil {
		return locale.Errorf("seed skill visibility: %w", err)
	}
	if err := d.seedDefaultInterceptRules(); err != nil {
		return locale.Errorf("seed intercept rules: %w", err)
	}
	if err := d.seedDefaultInterceptRulesV2(); err != nil {
		return locale.Errorf("seed intercept rules v2: %w", err)
	}
	if err := d.seedDefaultInterceptRulesV3(); err != nil {
		return locale.Errorf("seed intercept rules v3: %w", err)
	}
	if err := d.seedDefaultInterceptRulesV4(); err != nil {
		return locale.Errorf("seed intercept rules v4: %w", err)
	}
	if err := d.seedDefaultInterceptRulesV5(); err != nil {
		return locale.Errorf("seed intercept rules v5: %w", err)
	}
	if err := d.seedDefaultAssetInterceptRulesV2(); err != nil {
		return locale.Errorf("seed asset intercept rules: %w", err)
	}
	if err := d.seedDefaultAssetInterceptRules(); err != nil {
		return locale.Errorf("seed asset intercept rules: %w", err)
	}
	return nil
}

// seedDefaultAssetInterceptRules inserts the built-in asset blocklist (fuzzy
// domain matches for government / education sites) once on first startup. Gated
// by a settings flag so a user's later disable/delete is never resurrected on
// restart — same policy as the intercept-rule seed.
func (d *DB) seedDefaultAssetInterceptRules() error {
	if v, _, _ := d.GetSetting("asset_intercept_default_rules_v1"); v == "done" {
		return nil
	}
	rules := []struct {
		kind    string
		pattern string
		note    string
	}{
		{"fuzzy_domain", ".gov", "[Built-in] Government websites (.gov)"},
		{"fuzzy_domain", ".edu", "[Built-in] Education websites (.edu)"},
	}
	for _, r := range rules {
		if _, err := d.Exec(`
INSERT INTO asset_intercept_rules(enabled, kind, pattern, note, builtin)
VALUES (true, $1, $2, $3, true)
ON CONFLICT DO NOTHING`, r.kind, r.pattern, r.note); err != nil {
			return locale.Errorf("asset rule %q: %w", r.pattern, err)
		}
	}
	return d.SetSetting("asset_intercept_default_rules_v1", "done")
}

// seedDefaultAssetInterceptRulesV2 adds the government and military endings
// the first seed did not cover: .mil, and the government second-level names
// used outside English (gob.ar, gob.mx, gob.es, gouv.fr...). The dots on both
// sides keep ".gob." and ".gouv." from matching ordinary names. Its own flag
// makes it land once on databases that already ran the first seed, and never
// brings back a rule the user later disabled or deleted.
func (d *DB) seedDefaultAssetInterceptRulesV2() error {
	if v, _, _ := d.GetSetting("asset_intercept_default_rules_v2"); v == "done" {
		return nil
	}
	rules := []struct{ pattern, note string }{
		{".mil", "[Built-in] Military websites (.mil)"},
		{".gob.", "[Built-in] Government websites (.gob.)"},
		{".gouv.", "[Built-in] Government websites (.gouv.)"},
	}
	for _, r := range rules {
		if _, err := d.Exec(`
INSERT INTO asset_intercept_rules(enabled, kind, pattern, note, builtin)
SELECT true, 'fuzzy_domain', $1, $2, true
WHERE NOT EXISTS (SELECT 1 FROM asset_intercept_rules WHERE kind='fuzzy_domain' AND pattern=$1)`, r.pattern, r.note); err != nil {
			return locale.Errorf("asset rule %q: %w", r.pattern, err)
		}
	}
	return d.SetSetting("asset_intercept_default_rules_v2", "done")
}

// builtinSkillVisibility maps a shipped skill's directory name → the built-in
// agent keys that should see it by default. The skill FILES themselves live on the
// filesystem (SkillDir, loaded by norma at runtime); DB only carries this visibility
// binding. Skills omitted here (e.g. playwright-cli, scopesentry) ship invisible by
// default — the user turns them on per-agent when needed. scopesentry additionally
// declares `mcps: ScopeSentry`, which only takes effect once it's made visible and
// that MCP is enabled/configured.
var builtinSkillVisibility = map[string][]string{
	"api-recon": {"auto", "pentest", "worker"},
	// AI-enabled application assessments: discoverable by the task planner/worker
	// and the standalone pentest agent. Code-review agents remain source-only.
	"ai-red-team-method":          {"planner", "worker", "pentest"},
	"ai-target-recon":             {"planner", "worker", "pentest"},
	"ai-prompt-injection-testing": {"planner", "worker", "pentest"},
	"ai-agent-workflows":          {"planner", "worker", "pentest"},
	"ai-rag-retrieval":            {"planner", "worker", "pentest"},
	"ai-mcp-tool-trust":           {"planner", "worker", "pentest"},
	"ai-model-supply-chain":       {"planner", "worker", "pentest"},
	"ai-llm-output-sinks":         {"planner", "worker", "pentest"},
	"ai-serving-infrastructure":   {"planner", "worker", "pentest"},
	"ai-control-validation":       {"planner", "worker", "pentest"},
	// White-box code review: the methodology + core static-analysis skills are visible
	// to the task agents (worker/planner) that execute a /code-review task, and to the
	// standalone codereview chat agent for interactive source questions. Users can turn
	// on more per-agent as needed.
	"whitebox-code-review-methodology":  {"codereview", "worker", "planner", CodeReviewPlannerAgentKey, CodeReviewWorkerAgentKey},
	"vuln-assessment-methodology":       {"codereview", "worker", "planner", CodeReviewPlannerAgentKey, CodeReviewWorkerAgentKey},
	"dom-vulnerability-static-analysis": {"codereview", "worker", "planner", CodeReviewPlannerAgentKey, CodeReviewWorkerAgentKey},
	"custom-sanitizer-audit":            {"codereview", "worker", "planner", CodeReviewPlannerAgentKey, CodeReviewWorkerAgentKey},
}

// seedBuiltinSkillVisibility binds the shipped built-in skills to their default
// agents. Insert-if-absent preserves a user's explicit disabled row on restart.
func (d *DB) seedBuiltinSkillVisibility() error {
	for skillName, agentKeys := range builtinSkillVisibility {
		for _, key := range agentKeys {
			if _, err := d.Exec(`
INSERT INTO agent_skill_visibility(agent_id, skill_name, enabled)
SELECT id, $2, true FROM agents WHERE key=$1
ON CONFLICT (agent_id, skill_name) DO NOTHING`, key, skillName); err != nil {
				return locale.Errorf("skill %s → agent %s: %w", skillName, key, err)
			}
		}
	}
	return nil
}

// seedDefaultInterceptRules inserts built-in safety intercept rules once on
// first startup. The seed is gated by a settings flag so user edits (disable,
// delete, re-order) are never overwritten on subsequent restarts.
func (d *DB) seedDefaultInterceptRules() error {
	if v, _, _ := d.GetSetting("intercept_default_rules_v1"); v == "done" {
		return nil
	}
	type rule struct {
		name     string
		target   string // tool_name | tool_input
		typ      string // string | regex
		pattern  string
		action   string
		message  string
		priority int
	}
	rules := []rule{
		// System-destructive commands (priority 100).
		{
			name:     "[Built-in] Recursive forced deletion: rm -rf",
			target:   "tool_input",
			typ:      "regex",
			pattern:  interceptOldRecursiveDeletion,
			action:   "deny",
			message:  "Recursive forced deletion (rm -rf / rm --recursive) is prohibited because it may permanently damage the system or target environment",
			priority: 100,
		},
		{
			name:     "[Built-in] Delete critical system directories",
			target:   "tool_input",
			typ:      "regex",
			pattern:  interceptOldCriticalDirectories,
			action:   "deny",
			message:  "Deleting critical system paths is prohibited",
			priority: 100,
		},
		{
			name:     "[Built-in] Disk formatting: mkfs",
			target:   "tool_input",
			typ:      "regex",
			pattern:  interceptOldMkfs,
			action:   "deny",
			message:  "Formatting disks (mkfs) is prohibited",
			priority: 100,
		},
		{
			name:     "[Built-in] Overwrite disk devices: dd",
			target:   "tool_input",
			typ:      "regex",
			pattern:  interceptOldDD,
			action:   "deny",
			message:  "Using dd to overwrite disk devices is prohibited",
			priority: 100,
		},
		{
			name:     "[Built-in] Fork bomb",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `:\(\)\s*\{[^}]*:\|:`,
			action:   "deny",
			message:  "Executing fork bombs is prohibited",
			priority: 100,
		},
		{
			name:     "[Built-in] Shutdown / reboot",
			target:   "tool_input",
			typ:      "regex",
			pattern:  interceptOldShutdown,
			action:   "deny",
			message:  "Shutdown and reboot commands are prohibited",
			priority: 100,
		},
		{
			name:     "[Built-in] Kill all processes",
			target:   "tool_input",
			typ:      "regex",
			pattern:  interceptOldKill,
			action:   "deny",
			message:  "kill -9 -1 and killall -9 (kill all processes) are prohibited",
			priority: 100,
		},
		{
			name:     "[Built-in] Disk wiping: shred / wipe",
			target:   "tool_input",
			typ:      "regex",
			pattern:  interceptOldWipe,
			action:   "deny",
			message:  "Wiping disk devices with shred/wipe is prohibited",
			priority: 100,
		},
		{
			name:     "[Built-in] Flush firewall rules",
			target:   "tool_input",
			typ:      "regex",
			pattern:  interceptOldFirewall,
			action:   "deny",
			message:  "Flushing firewall rules (iptables -F / nft flush) is prohibited",
			priority: 100,
		},
		// Destructive database operations (priority 90).
		{
			name:     "[Built-in] SQL DROP DATABASE / TABLE / SCHEMA",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)\bDROP\s+(?:DATABASE|TABLE|SCHEMA|INDEX|VIEW|TABLESPACE|USER|ROLE)\b`,
			action:   "deny",
			message:  "DROP operations are prohibited because they may irreversibly destroy database objects",
			priority: 90,
		},
		{
			name:     "[Built-in] SQL TRUNCATE",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)\bTRUNCATE\s+(?:TABLE\s+)?\w`,
			action:   "deny",
			message:  "TRUNCATE is prohibited because it may remove all data from a table",
			priority: 90,
		},
		{
			name:     "[Built-in] MongoDB drop / dropDatabase",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)\.(?:dropDatabase|dropCollection|drop)\s*\(`,
			action:   "deny",
			message:  "MongoDB drop operations are prohibited",
			priority: 90,
		},
		{
			name:     "[Built-in] Redis FLUSHALL / FLUSHDB",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)\b(?:FLUSHALL|FLUSHDB)\b`,
			action:   "deny",
			message:  "Redis FLUSHALL / FLUSHDB is prohibited because it may clear all cached data",
			priority: 90,
		},
		// Destructive HTTP requests (priority 80).
		// Three common ways agents send DELETE requests:
		//   1. curl -X DELETE / --request DELETE (direct Bash execution or a script).
		//   2. Python HTTP clients' .delete() method.
		//   3. method: 'DELETE' / method="DELETE" in JS or other scripts.
		{
			name:     "[Built-in] HTTP DELETE via curl / wget",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)\bcurl\b[^|\n&;"]{0,300}(?:-X\s*DELETE|--request\s+DELETE|-XDELETE)|\bwget\b[^|\n&;"]{0,300}--method[=\s]+DELETE`,
			action:   "deny",
			message:  "Sending HTTP DELETE via curl/wget is prohibited because it may delete target-system data",
			priority: 80,
		},
		{
			name:     "[Built-in] Python HTTP client DELETE (requests/httpx/aiohttp)",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)\b(?:requests|httpx|aiohttp|urllib\.request)\.delete\s*\(|session\.delete\s*\(|client\.delete\s*\(`,
			action:   "deny",
			message:  "Sending DELETE requests with Python HTTP clients is prohibited",
			priority: 80,
		},
		{
			name:     "[Built-in] HTTP DELETE method in scripts (JS/general)",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)axios\.delete\s*\(|method\s*[:=]\s*['"]DELETE['"]`,
			action:   "deny",
			message:  "Declaring and sending HTTP DELETE requests in scripts is prohibited",
			priority: 80,
		},
		{
			name:     "[Built-in] Bulk clearing / purging endpoints",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)/(?:clear|wipe|flush|purge|truncate|drop|destroy|factory[-_]reset|reset[-_]all)(?:[/?#"'\s]|$)`,
			action:   "deny",
			message:  "Calling bulk clearing or destructive endpoints (/clear /wipe /flush /purge, etc.) is prohibited",
			priority: 80,
		},
	}
	for _, r := range rules {
		if _, err := d.Exec(`
INSERT INTO intercept_rules(name, enabled, priority, match_target, match_type, pattern, action, message, timeout_enabled, timeout_seconds, timeout_action)
VALUES ($1, true, $2, $3, $4, $5, $6, $7, false, 60, 'deny')
ON CONFLICT DO NOTHING`,
			r.name, r.priority, r.target, r.typ, r.pattern, r.action, r.message,
		); err != nil {
			return locale.Errorf("rule %q: %w", r.name, err)
		}
	}
	return d.SetSetting("intercept_default_rules_v1", "done")
}

// Keep historical seed bytes stable: V5 only updates defaults still equal to
// these patterns, leaving operator edits and deletions alone.
const (
	interceptOldRecursiveDeletion   = `(?i)\brm\b.{0,80}(?:-[a-z]*r[a-z]*f[a-z]*|-[a-z]*f[a-z]*r[a-z]*|--recursive|--no-preserve-root)`
	interceptOldCriticalDirectories = `\brm\b[^"'\n]{0,60}["'\s](/|/etc|/bin|/usr|/boot|/var|/lib|/sys|/proc|/dev|/sbin|/root)`
	interceptOldMkfs                = `\bmkfs\b`
	interceptOldDD                  = `\bdd\b[^|\n]{0,100}\bof=\s*/dev/[a-zA-Z]`
	interceptOldShutdown            = `\b(?:shutdown|reboot|halt|poweroff|init\s+[06])\b`
	interceptOldKill                = `\bkill\s+-9\s+-1\b|\bkillall\s+-9\b`
	interceptOldWipe                = `\b(?:shred|wipe)\b[^|\n]{0,80}/dev/[a-zA-Z]`
	interceptOldFirewall            = `\biptables\s+(?:-F|--flush)\b|\bnft\s+flush\s+ruleset\b`
	interceptOldDestructive         = `(?i)\b(rm\s+-rf\s+/|mkfs|dd\s+if=|:\(\)\s*\{|shutdown|reboot|>\s*/dev/sd)`
)

// Start of a canonical subject, or a conservative raw-input command boundary.
// URL/option/identifier characters cannot introduce a bare program. JSON string
// escapes need their own alternative for commands following a line break/tab.
// Whitespace and quotes deliberately retain embedded interpreter/remote commands,
// at the cost of matching some command-like data arguments too.
// Keep literal executable paths and && in embedded programs too. A file argument
// whose full basename is a dangerous program (with dangerous arguments), or a
// URL containing literal && followed by a command, remains ambiguous raw data.
const interceptV5CommandStart = `(?:^|(?:[^\w./\\:@%?&=+#~-]|\\[nrt]|&&)(?:/?(?:[\w.-]+/)*)?)`

const interceptV5CommandEnd = `(?:$|[\s"';&|<>()]|\\[nrt]|\\+["'])`

// InterceptRuleRefinementsV5 returns a fresh copy of the exact migration table
// so matcher tests can exercise the seeded patterns without a database.
func InterceptRuleRefinementsV5() []struct{ Name, OldPattern, NewPattern string } {
	return []struct{ Name, OldPattern, NewPattern string }{
		{"[Built-in] Recursive forced deletion: rm -rf", interceptOldRecursiveDeletion,
			`(?i)` + interceptV5CommandStart + `rm(?:[\s"'].{0,79}|\\[nrt].{0,78})(?:-[a-z]*r[a-z]*f[a-z]*|-[a-z]*f[a-z]*r[a-z]*|--recursive|--no-preserve-root)`},
		{"[Built-in] Delete critical system directories", interceptOldCriticalDirectories,
			interceptV5CommandStart + `rm[\s"'](?:.{0,59}[\s"'])?(?:\\*["'])*\\*/(?:etc|bin|usr|boot|var|lib|sys|proc|dev|sbin|root)?(?:/\*?|\*)?(?:$|[\s"';&|<>)]|\\[nrt]|\\+["'])`},
		{"[Built-in] Disk formatting: mkfs", interceptOldMkfs,
			interceptV5CommandStart + `mkfs(?:\.[\w-]+)?` + interceptV5CommandEnd},
		{"[Built-in] Overwrite disk devices: dd", interceptOldDD,
			interceptV5CommandStart + `dd(?:[\s"'][^|\n]{0,99}|\\[nrt][^|\n]{0,98})\bof=\s*/dev/[a-zA-Z]`},
		{"[Built-in] Shutdown / reboot", interceptOldShutdown,
			interceptV5CommandStart + `(?:shutdown|reboot|halt|poweroff|init\s+[06])` + interceptV5CommandEnd},
		{"[Built-in] Kill all processes", interceptOldKill,
			interceptV5CommandStart + `(?:kill\s+-9\s+-1\b|killall\s+-9\b)`},
		{"[Built-in] Disk wiping: shred / wipe", interceptOldWipe,
			interceptV5CommandStart + `(?:shred|wipe)(?:[\s"'][^|\n]{0,79}|\\[nrt][^|\n]{0,78})/dev/[a-zA-Z]`},
		{"[Built-in] Flush firewall rules", interceptOldFirewall,
			interceptV5CommandStart + `(?:iptables\s+(?:-F|--flush)\b|nft\s+flush\s+ruleset\b)`},
		// V1 handles actual fork bombs, but V2 also guarded incomplete function
		// signatures. Keep that residual and disk redirection. Without the dd
		// read/copy trigger, redirection must also catch dd if=image > /dev/sdb
		// (the old word boundary before '>' missed whitespace before redirects).
		{"[Built-in] Destructive system commands", interceptOldDestructive,
			`(?i)(?:\b:\(\)\s*\{|>\s*/dev/(?:sd|hd|vd|xvd|nvme|mmcblk|disk))`},
	}
}

// seedDefaultInterceptRulesV5 refines untouched defaults only. Each conditional
// update is idempotent, including retries after a partial migration failure.
func (d *DB) seedDefaultInterceptRulesV5() error {
	if v, _, err := d.GetSetting("intercept_default_rules_v5"); err != nil {
		return err
	} else if v == "done" {
		return nil
	}
	for _, r := range InterceptRuleRefinementsV5() {
		if _, err := d.Exec(`
UPDATE intercept_rules SET pattern = $1
WHERE name = $2 AND pattern = $3 AND match_target = 'tool_input' AND match_type = 'regex'`,
			r.NewPattern, r.Name, r.OldPattern,
		); err != nil {
			return locale.Errorf("rule %q: %w", r.Name, err)
		}
	}
	return d.SetSetting("intercept_default_rules_v5", "done")
}

// seedDefaultInterceptRulesV2 migrates the two safety patterns that used to be
// hard-coded in guard.go (destructive shell + data-exfil pipe) into ordinary
// intercept rules. Gated by its own flag so it also lands on DBs that already ran
// v1. Unlike the old guard.go floor, these are ordinary [Built-in] rules — the user can
// disable or delete them. The exfil rule ships DISABLED by default (its
// curl/wget/nc pipe pattern mis-fires on legitimate CTF/pentest reverse-shell and
// data-transfer pipes); enable it manually when exfil gating is actually wanted.
func (d *DB) seedDefaultInterceptRulesV2() error {
	if v, _, _ := d.GetSetting("intercept_default_rules_v2"); v == "done" {
		return nil
	}
	rules := []struct {
		name     string
		pattern  string
		action   string
		message  string
		enabled  bool
		priority int
	}{
		{
			name:     "[Built-in] Destructive system commands",
			pattern:  interceptOldDestructive,
			action:   "deny",
			message:  "Destructive command denied (rm -rf /, mkfs, dd, fork bomb, shutdown/reboot, or disk-device overwrite)",
			enabled:  true,
			priority: 100,
		},
		{
			name:     "[Built-in] Data-exfiltration pipeline",
			pattern:  `(?i)(curl|wget|nc|ncat)\b[^|]*\b(\|\s*(curl|wget|nc))`,
			action:   "deny",
			message:  "Suspected data-exfiltration pipeline denied (command output sent through curl/wget/nc)",
			enabled:  false,
			priority: 80,
		},
	}
	for _, r := range rules {
		if _, err := d.Exec(`
INSERT INTO intercept_rules(name, enabled, priority, match_target, match_type, pattern, action, message, timeout_enabled, timeout_seconds, timeout_action)
VALUES ($1, $2, $3, 'tool_input', 'regex', $4, $5, $6, false, 60, 'deny')
ON CONFLICT DO NOTHING`,
			r.name, r.enabled, r.priority, r.pattern, r.action, r.message,
		); err != nil {
			return locale.Errorf("rule %q: %w", r.name, err)
		}
	}
	return d.SetSetting("intercept_default_rules_v2", "done")
}

// seedDefaultInterceptRulesV3 adds the delete-endpoint path rule. The v1 HTTP rules
// only catch the DELETE *method* (curl -X DELETE, requests.delete(, method:'DELETE'),
// and v1's path rule covers only /clear /wipe /flush /purge /truncate /drop /destroy
// /factory-reset /reset-all — so a plain `curl 'http://t/api/user/delete?id=1'` (a
// delete endpoint reached with GET/POST, which is how most web apps expose deletion)
// slipped through every built-in rule. Own flag so it also lands on DBs that already
// ran v1/v2, where editing the v1 seed would have no effect.
//
// The pattern deliberately requires a separator after the verb so /delivery,
// /details, /delta and /delegate do not match, while /deleteAll, /delete_user and
// /delete-user do. destroy is re-covered here because v1's rule does not allow a
// suffix (/destroyAll was missed).
//
// Exported as a package const only so the seeded regex is unit-testable without a DB.
const deleteEndpointPathPattern = `(?i)/(?:(?:delete|remove|unlink|erase|destroy)[-\w]*|del)(?:[/?#"'\s]|$)`

func (d *DB) seedDefaultInterceptRulesV3() error {
	if v, _, _ := d.GetSetting("intercept_default_rules_v3"); v == "done" {
		return nil
	}
	const name = "[Built-in] Deletion endpoints"
	if _, err := d.Exec(`
INSERT INTO intercept_rules(name, enabled, priority, match_target, match_type, pattern, action, message, timeout_enabled, timeout_seconds, timeout_action)
SELECT $1, true, 80, 'tool_input', 'regex', $2, 'deny', $3, false, 60, 'deny'
WHERE NOT EXISTS (SELECT 1 FROM intercept_rules WHERE name = $1)`,
		name,
		deleteEndpointPathPattern,
		"Calling deletion endpoints (/delete /remove /unlink /erase, etc.) is prohibited regardless of HTTP method; many applications allow GET/POST to trigger real data deletion",
	); err != nil {
		return locale.Errorf("rule %q: %w", name, err)
	}
	return d.SetSetting("intercept_default_rules_v3", "done")
}

// V4 patterns inspect serialized tool_input as well as the interceptor's decoded
// subjects. Require a command position, not just a keyword in a URL or payload.
// This is accident prevention, not shell parsing: aliases, scripts, variable
// expansion and encoded programs still need the independent approval gate.
const interceptV4CommandStart = `(?i)(?:^|"(?:command|cmd|input)"\s*:\s*"|&&|\|\||[;|\r\n]|\\n)[ \t]*(?:sudo[ \t]+)?(?:/[\w./-]+/|[a-z]:\\{1,2}(?:[\w.-]+\\{1,2})*)?`

// Stay inside one JSON string and shell segment, accepting JSON escape pairs.
// The bound keeps unrelated, distant arguments from being joined into a match.
const interceptV4Args = `(?:[^"\\;&|\r\n]|\\[^\r\n]){0,240}?`

const interceptV4OptionalArgs = `(?:` + interceptV4Args + `[ \t])?`

var defaultInterceptRulesV4 = []struct {
	name     string
	pattern  string
	action   string
	message  string
	priority int
}{
	// Disk erasure is not needed to demonstrate a security finding. Include
	// Windows and macOS utilities alongside Linux discard/signature removal.
	{
		name:     "[Built-in] Disk erasure across platforms",
		pattern:  interceptV4CommandStart + `(?:format(?:\.com)?[ \t]+[a-z]:|(?:Clear-Disk|Initialize-Disk|Format-Volume)[ \t]+|diskutil[ \t]+(?:eraseDisk|zeroDisk|secureErase)[ \t]+|wipefs[ \t]+(?:-a|--all)\b|blkdiscard[ \t]+)`,
		action:   "deny",
		message:  "Disk erasure, initialization and formatting commands are prohibited",
		priority: 100,
	},
	// Recursive cleanup can be legitimate, but Windows equivalents of rm -r
	// deserve a human decision. Require the recursive switch, in either order.
	{
		name:     "[Built-in] Windows recursive deletion",
		pattern:  interceptV4CommandStart + `(?:(?:rmdir|rd|del|erase)(?:\.exe)?[ \t]+` + interceptV4OptionalArgs + `/s(?:[ \t]|\\?["']|$)|(?:Remove-Item|ri|rm|del|erase)[ \t]+` + interceptV4OptionalArgs + `-Recurse(?:[ \t]|\\?["']|$))`,
		action:   "ask",
		message:  "Recursive Windows deletion requires human approval because it may remove an entire directory tree",
		priority: 95,
	},
	// Only explicit flood modes are denied; ordinary packet probes and bounded
	// host scans remain available. Do not block a tool merely by its name.
	{
		name:     "[Built-in] Network flood modes",
		pattern:  interceptV4CommandStart + `hping3?(?:\.exe)?[ \t]+` + interceptV4OptionalArgs + `--flood(?:[ \t]|\\?["']|$)`,
		action:   "deny",
		message:  "Network flood modes are prohibited because they can deny service",
		priority: 100,
	},
	// Large ranges and opaque/random target lists warrant scope review, whereas
	// a single host or an IPv4 /24 does not. Ports and URL text are not targets.
	{
		name:     "[Built-in] Broad network scan targets",
		pattern:  interceptV4CommandStart + `(?:nmap|masscan|rustscan)(?:\.exe)?[ \t]+` + interceptV4OptionalArgs + `(?:\\?["'])?(?:-i[LR](?:[ \t]|[^\s"']+)|(?:[0-9]{1,3}\.){3}[0-9]{1,3}/(?:[0-9]|1[0-9]|2[0-3])(?:[ \t]|\\?["']|$)|(?:[0-9a-f]*:){2,}[0-9a-f]*/(?:[0-9]|[1-5][0-9]|6[0-4])(?:[ \t]|\\?["']|$))`,
		action:   "ask",
		message:  "Large network ranges and file-based or random scan targets require human scope approval",
		priority: 85,
	},
	// Credential extraction can prove impact during an authorized assessment,
	// but dumping OS secrets risks exposing an organization's reusable access.
	{
		name:     "[Built-in] Credential store dumping",
		pattern:  interceptV4CommandStart + `(?:mimikatz(?:\.exe)?[ \t]+` + interceptV4Args + `(?:\\?["'])?(?:sekurlsa|lsadump)::|(?:impacket-)?secretsdump(?:\.py|\.exe)?[ \t]+` + interceptV4Args + `(?:@|\bLOCAL\b)|reg(?:\.exe)?[ \t]+save[ \t]+(?:hklm\\{1,2}|hkey_local_machine\\{1,2})(?:sam|security|system)\b|security[ \t]+dump-keychain\b|procdump(?:64)?(?:\.exe)?[ \t]+` + interceptV4Args + `\blsass(?:\.exe)?\b)`,
		action:   "ask",
		message:  "Dumping operating-system credential stores requires human approval",
		priority: 95,
	},
	// Gate explicit curl file uploads from well-known secret locations. Reads,
	// ordinary evidence uploads and paths mentioned in a URL are not uploads.
	// This deliberately does not try to infer secrets from arbitrary filenames.
	{
		name:     "[Built-in] Sensitive credential file uploads",
		pattern:  interceptV4CommandStart + `curl(?:\.exe)?[ \t]+` + interceptV4OptionalArgs + `(?:(?:--data(?:-binary)?[= \t]+|--data-ascii[= \t]+|-d[ \t]*)(?:\\?["'])?@(?:\\?["'])?|(?:--form[= \t]+|-F[ \t]*)(?:\\?["'])?[^\s@"']+=@|(?:--upload-file[= \t]+|-T[ \t]*)(?:\\?["'])?)(?:[^\s"']*[/\\](?:\.ssh[/\\]+id_[\w-]+|\.aws[/\\]+credentials|\.kube[/\\]+config)|/etc/(?:shadow|gshadow))(?:[ \t;]|\\?["']|$)`,
		action:   "ask",
		message:  "Uploading files from sensitive credential locations requires human approval",
		priority: 95,
	},
}

// seedDefaultInterceptRulesV4 has its own flag so existing installations receive
// the new defaults once. Name checks preserve existing rules after partial seeds;
// the completion flag preserves subsequent operator edits and deletions.
func (d *DB) seedDefaultInterceptRulesV4() error {
	if v, _, err := d.GetSetting("intercept_default_rules_v4"); err != nil {
		return err
	} else if v == "done" {
		return nil
	}
	for _, r := range defaultInterceptRulesV4 {
		if _, err := d.Exec(`
INSERT INTO intercept_rules(name, enabled, priority, match_target, match_type, pattern, action, message, timeout_enabled, timeout_seconds, timeout_action)
SELECT $1, true, $2, 'tool_input', 'regex', $3, $4, $5, false, 60, 'deny'
WHERE NOT EXISTS (SELECT 1 FROM intercept_rules WHERE name = $1)`,
			r.name, r.priority, r.pattern, r.action, r.message,
		); err != nil {
			return locale.Errorf("rule %q: %w", r.name, err)
		}
	}
	return d.SetSetting("intercept_default_rules_v4", "done")
}
