package intercept

import (
	"testing"

	"github.com/Philocyber/counterweight/db"
)

func TestPolicyLoadFailureDeniesAndRecovers(t *testing.T) {
	dsn, _, err := db.DSN()
	if err != nil {
		t.Skip("requires disposable PostgreSQL")
	}
	d, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	i := New(d)
	prior, err := i.GetEnabledTools()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = i.SetEnabledTools(prior) })
	const probe = "PolicyLoadProbe"
	if err := i.SetEnabledTools([]string{probe}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`ALTER TABLE settings RENAME TO policy_failure_settings`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`ALTER TABLE policy_failure_settings RENAME TO settings`) })
	if !i.IsToolEnabled(probe) {
		t.Fatal("failed policy load silently disabled interception")
	}
	decision, matched := i.Match(probe, []byte(`{}`))
	if !matched || decision.Action != "deny" {
		t.Fatalf("failed settings read allowed execution: %+v matched=%v", decision, matched)
	}
	if _, err := d.Exec(`ALTER TABLE policy_failure_settings RENAME TO settings`); err != nil {
		t.Fatal(err)
	}
	if !i.IsToolEnabled(probe) || i.IsToolEnabled("ExplicitlyDisabledProbe") {
		t.Fatal("policy did not recover with the configured tool selection")
	}
	if decision, matched := i.Match(probe, []byte(`{}`)); matched && decision.Action == "deny" {
		t.Fatal("load failure poisoned the cache after recovery")
	}
}

func TestMalformedToolPolicyIsNotAnEmptyDisabledPolicy(t *testing.T) {
	dsn, _, err := db.DSN()
	if err != nil {
		t.Skip("requires disposable PostgreSQL")
	}
	d, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	i := New(d)
	prior, err := i.GetEnabledTools()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = i.SetEnabledTools(prior) })
	if err := d.SetSetting("intercept_enabled_tools", "invalid JSON"); err != nil {
		t.Fatal(err)
	}
	if !i.IsToolEnabled("Bash") {
		t.Fatal("malformed policy silently disabled all controls")
	}
	decision, matched := i.Match("Bash", []byte(`{"command":"fixture"}`))
	if !matched || decision.Action != "deny" {
		t.Fatal("malformed policy did not deny")
	}
	if err := i.SetEnabledTools([]string{"Bash"}); err != nil {
		t.Fatal(err)
	}
	rule, err := d.CreateInterceptRule("invalid fixture rule", "tool_name", "regex", "[", "deny", "", 1000, true, false, 0, "deny")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteInterceptRule(rule.ID) })
	i.Invalidate()
	if decision, matched := i.Match("Bash", []byte(`{}`)); !matched || decision.Action != "deny" {
		t.Fatal("invalid enabled regex was silently discarded")
	}
	if err := d.DeleteInterceptRule(rule.ID); err != nil {
		t.Fatal(err)
	}
	if err := i.SetEnabledTools([]string{}); err != nil {
		t.Fatal(err)
	}
	if i.IsToolEnabled("Bash") {
		t.Fatal("a valid deliberate empty policy cannot disable interception")
	}
}
