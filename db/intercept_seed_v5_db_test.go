package db

import (
	"database/sql"
	"errors"
	"testing"
)

// The v5 refinement against a real database: it replaces the pattern of a
// built-in rule only where the rule still has the pattern it was shipped
// with, and leaves every decision of the operator alone.
func TestInterceptSeedV5OnDatabase(t *testing.T) {
	d := assessmentDB(t)
	refinements := InterceptRuleRefinementsV5()
	if len(refinements) < 3 {
		t.Fatalf("refinements: %d", len(refinements))
	}
	const flag = "intercept_default_rules_v5"
	type row struct {
		pattern string
		enabled bool
		found   bool
	}
	read := func(name string) (r row) {
		t.Helper()
		err := d.QueryRow(`SELECT pattern, enabled FROM intercept_rules WHERE name=$1`, name).Scan(&r.pattern, &r.enabled)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			t.Fatal(err)
		}
		r.found = err == nil
		return r
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := d.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	rerun := func() {
		t.Helper()
		exec(`DELETE FROM settings WHERE key=$1`, flag)
		if err := d.seedDefaultInterceptRulesV5(); err != nil {
			t.Fatal(err)
		}
	}

	// Opening the database ran every seed. On a database that was seeded
	// before this refinement existed, this also proves that the old patterns
	// kept in the code are the ones that were shipped.
	for _, r := range refinements {
		if got := read(r.Name); !got.found || got.pattern != r.NewPattern {
			t.Fatalf("%s: not refined after start-up (found=%v)", r.Name, got.found)
		}
	}

	edited, disabled, deleted := refinements[0], refinements[1], refinements[2]
	var saved struct {
		priority                      int
		target, kind, action, message string
		timeoutEnabled                bool
		timeoutSeconds                int
		timeoutAction                 string
	}
	if err := d.QueryRow(`SELECT priority, match_target, match_type, action, message, timeout_enabled, timeout_seconds, timeout_action FROM intercept_rules WHERE name=$1`, deleted.Name).
		Scan(&saved.priority, &saved.target, &saved.kind, &saved.action, &saved.message, &saved.timeoutEnabled, &saved.timeoutSeconds, &saved.timeoutAction); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Leave the shared test database as a fresh install would be.
		for _, r := range refinements {
			_, _ = d.Exec(`UPDATE intercept_rules SET pattern=$2, enabled=true WHERE name=$1`, r.Name, r.NewPattern)
		}
		_, _ = d.Exec(`
INSERT INTO intercept_rules(name, enabled, priority, match_target, match_type, pattern, action, message, timeout_enabled, timeout_seconds, timeout_action)
SELECT $1, true, $2, $3, $4, $5, $6, $7, $8, $9, $10 WHERE NOT EXISTS (SELECT 1 FROM intercept_rules WHERE name=$1)`,
			deleted.Name, saved.priority, saved.target, saved.kind, deleted.NewPattern, saved.action, saved.message, saved.timeoutEnabled, saved.timeoutSeconds, saved.timeoutAction)
		_ = d.SetSetting(flag, "done")
	})

	// An install from before the refinement: every rule has its old pattern.
	for _, r := range refinements {
		exec(`UPDATE intercept_rules SET pattern=$2 WHERE name=$1`, r.Name, r.OldPattern)
	}
	// The operator edited one rule, switched another off and deleted a third.
	const custom = `operator-pattern`
	exec(`UPDATE intercept_rules SET pattern=$2 WHERE name=$1`, edited.Name, custom)
	exec(`UPDATE intercept_rules SET enabled=false WHERE name=$1`, disabled.Name)
	exec(`DELETE FROM intercept_rules WHERE name=$1`, deleted.Name)
	rerun()

	if got := read(edited.Name); got.pattern != custom {
		t.Errorf("an edited rule was overwritten")
	}
	if got := read(disabled.Name); got.enabled || got.pattern != disabled.NewPattern {
		t.Errorf("a rule that was switched off: enabled=%v, refined=%v; want it still off, with the refined pattern", got.enabled, got.pattern == disabled.NewPattern)
	}
	if read(deleted.Name).found {
		t.Errorf("a deleted rule came back")
	}
	for _, r := range refinements[3:] {
		if got := read(r.Name); got.pattern != r.NewPattern || !got.enabled {
			t.Errorf("%s: untouched rule not refined", r.Name)
		}
	}

	// With the flag set, a later start changes nothing, even for a rule put
	// back to its old pattern by hand.
	last := refinements[len(refinements)-1]
	exec(`UPDATE intercept_rules SET pattern=$2 WHERE name=$1`, last.Name, last.OldPattern)
	if err := d.seedDefaultInterceptRulesV5(); err != nil {
		t.Fatal(err)
	}
	if got := read(last.Name); got.pattern != last.OldPattern {
		t.Errorf("the refinement ran again although its flag was set")
	}
}
