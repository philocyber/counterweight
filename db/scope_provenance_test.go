package db

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestScopeBreadthAppliesToStoredNetwork(t *testing.T) {
	for _, in := range []string{
		"::ffff:0.0.0.0/96", "::ffff:0.0.0.0/100", "::ffff:10.0.0.0/104", "::ffff:10.0.0.0/111",
		"0.0.0.0/0", "::/0", "2001:db8::/31",
	} {
		for name, parse := range map[string]func(string) (ParsedScope, error){
			"line": ParseScopeLine, "auto": ParseAutoScopeLine,
		} {
			if rule, err := parse(in); err == nil {
				t.Errorf("%s %q accepted as %+v", name, in, rule)
			}
		}
	}
	for in, want := range map[string]string{
		"::ffff:198.51.100.0/120": "198.51.100.0/24",
		"::ffff:10.1.0.0/112":     "10.1.0.0/16",
		"198.51.100.0/24":         "198.51.100.0/24",
		"2001:db8::/32":           "2001:db8::/32",
	} {
		rule, err := ParseScopeLine(in)
		if err != nil || rule.Kind != "cidr" || rule.Net != want {
			t.Errorf("%q: %+v %v, want %s", in, rule, err, want)
		}
	}
}

func TestAgentTaskScopeNetworkIsBounded(t *testing.T) {
	d, assets, _ := testSetup(t)
	t.Cleanup(func() { _ = d.Close() })
	task, err := d.CreateTask("agent network breadth", "test", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })
	for _, v := range []string{"0.0.0.0/0", "::/0", "10.0.0.0/8", "::ffff:0.0.0.0/96"} {
		for _, source := range []string{"agent", "auto"} {
			if ts, err := assets.AddAgentScope(task.ID, "cidr", v, "test", source); err == nil {
				t.Errorf("%s accepted %s: %+v", source, v, ts)
			}
		}
	}
	if _, err := assets.AddAgentScope(task.ID, "cidr", "198.51.100.0/24", "test", "agent"); err != nil {
		t.Fatalf("a /24 proposal must work: %v", err)
	}
	if _, err := assets.AddAgentScope(task.ID, "cidr", "10.0.0.0/8", "operator", "manual"); err != nil {
		t.Fatalf("an operator may scope a broad network: %v", err)
	}
}

func TestAgentCompanyScopeStaysOutOfReadGrants(t *testing.T) {
	d, assets, companies := testSetup(t)
	t.Cleanup(func() { _ = d.Close() })
	marker := fmt.Sprintf("provenance-%d", time.Now().UnixNano())
	root := marker + ".invalid"
	octet := time.Now().UnixNano() % 200
	network, host, netPrefix := fmt.Sprintf("100.100.%d.0/24", octet), fmt.Sprintf("100.100.%d.9", octet), fmt.Sprintf("100.100.%d.", octet)
	foreignRoot := marker + "-foreign.invalid"
	company, _, err := companies.UpsertCompany(marker, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM companies WHERE id=$1`, company) })
	if added, _, invalid, msgs := companies.AddScope(company, []string{root}, "operator"); added != 1 || invalid != 0 {
		t.Fatalf("operator rule: %d %d %v", added, invalid, msgs)
	}
	if added, _, invalid, msgs := companies.ProposeScope(company, []string{foreignRoot, network}, "agent claim"); added != 2 || invalid != 0 {
		t.Fatalf("agent rules: %d %d %v", added, invalid, msgs)
	}
	var ids []int64
	t.Cleanup(func() { _, _ = assets.DeleteByIDs(ids) })
	service := func(host, ip string) int64 {
		t.Helper()
		id, err := assets.UpsertHTTPService(UpsertHTTPServiceReq{URL: "https://" + host, IP: ip, Auth: []map[string]any{{"token": "CREDENTIAL_SENTINEL"}}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		return id
	}
	own := service("api."+root, "")
	foreign := service("api."+foreignRoot, "")
	viaNet := service("net."+marker+"-other.invalid", host)
	// A company link written with an app, which only the agent tool does, grants no reads either.
	linked, err := assets.UpsertApp(UpsertAppReq{Name: marker + " app", CompanyID: &company})
	if err != nil {
		t.Fatal(err)
	}
	ids = append(ids, linked)
	for _, id := range []int64{own, foreign, viaNet} {
		var attributed int64
		if err := d.QueryRow(`SELECT COALESCE(company_id,0) FROM assets WHERE id=$1`, id).Scan(&attributed); err != nil || attributed != company {
			t.Fatalf("attribution must still follow every rule: asset %d -> %d (company %d) %v", id, attributed, company, err)
		}
	}
	rules, err := companies.GetScope(company)
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string]string{}
	var pendingID int64
	for _, r := range rules {
		key := r.Domain + r.Net
		sources[key] = r.Source
		if r.Domain == foreignRoot {
			pendingID = r.ID
		}
	}
	if sources[root] != "manual" || sources[foreignRoot] != "agent" || sources[network] != "agent" {
		t.Fatalf("rule sources: %v", sources)
	}

	grantedTask := func(label string) (int64, []*Asset) {
		t.Helper()
		task, err := d.CreateTaskWithOptions(label, "test", TaskCreateOptions{CompanyIDs: []int64{company}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = d.DeleteTask(task.ID) })
		got, err := assets.GetByIDsInScope(task.ID, ids)
		if err != nil {
			t.Fatal(err)
		}
		return task.ID, got
	}
	taskID, got := grantedTask("before approval")
	if len(got) != 1 || got[0].ID != own {
		t.Fatalf("only the operator rule may grant reads: %+v", got)
	}
	// Linking the company makes only the granted assets the task's own.
	var stamped int
	if err := d.QueryRow(`SELECT count(*) FROM assets WHERE id=ANY($1::bigint[]) AND $2=ANY(task_ids)`,
		fmt.Sprintf("{%d,%d,%d}", foreign, viaNet, linked), taskID).Scan(&stamped); err != nil || stamped != 0 {
		t.Fatalf("task was given %d assets outside its grant as its own (%v)", stamped, err)
	}
	if err := d.QueryRow(`SELECT count(*) FROM assets WHERE id=$1 AND $2=ANY(task_ids)`, own, taskID).Scan(&stamped); err != nil || stamped != 1 {
		t.Fatalf("the granted asset did not become the task's own (%v)", err)
	}
	// An agent's link does not replace a link from before agents' links were told apart.
	var legacy int64
	if err := d.QueryRow(`INSERT INTO assets(type,app_name,company_id) VALUES ('app',$1,$2) RETURNING id`, marker+" legacy app", company).Scan(&legacy); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = assets.DeleteByIDs([]int64{legacy}) })
	other, _, err := companies.UpsertCompany(marker+"-other", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM companies WHERE id=$1`, other) })
	if _, err := assets.UpsertApp(UpsertAppReq{Name: marker + " legacy app", CompanyID: &other}); err != nil {
		t.Fatal(err)
	}
	var owner int64
	var source string
	if err := d.QueryRow(`SELECT company_id,company_source FROM assets WHERE id=$1`, legacy).Scan(&owner, &source); err != nil || owner != company || source != "explicit" {
		t.Fatalf("agent link replaced an earlier link: company=%d source=%q (%v)", owner, source, err)
	}
	var grant string
	if err := d.QueryRow(`SELECT read_grant::text FROM task_scope WHERE task_id=$1 AND kind='company'`, taskID).Scan(&grant); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(grant, foreignRoot) || strings.Contains(grant, netPrefix) {
		t.Fatalf("agent rule captured in the grant: %s", grant)
	}

	// Writing the same rule as an operator, or an agent repeating an operator rule, keeps the operator source.
	companies.ProposeScope(company, []string{root}, "agent repeat")
	if ok, err := companies.ApproveScopeRule(company, pendingID); err != nil || !ok {
		t.Fatalf("approve: %v %v", ok, err)
	}
	if ok, _ := companies.ApproveScopeRule(company, pendingID); ok {
		t.Fatal("approving twice must report nothing to approve")
	}
	rules, _ = companies.GetScope(company)
	for _, r := range rules {
		if r.Domain == root && r.Source != "manual" {
			t.Fatalf("agent repeat downgraded an operator rule: %+v", r)
		}
	}
	// Replacing the rule set from the console keeps the rule that is still pending.
	if _, _, _, err := companies.UpdateScopeInputsChecked(company, []ScopeInput{{Value: root}, {Value: foreignRoot}, {Value: network}}, "operator edit"); err != nil {
		t.Fatal(err)
	}
	rules, _ = companies.GetScope(company)
	sources = map[string]string{}
	for _, r := range rules {
		sources[r.Domain+r.Net] = r.Source
	}
	if sources[root] != "manual" || sources[network] != "agent" {
		t.Fatalf("rule set replacement approved a pending rule or lost an operator rule: %v", sources)
	}
	_, _ = companies.ApproveScopeRule(company, ruleID(t, companies, company, network))
	_, got = grantedTask("after approval")
	if len(got) != 3 {
		t.Fatalf("approved rules must grant reads: %d assets", len(got))
	}
}

func ruleID(t *testing.T, companies *CompanyStore, company int64, key string) int64 {
	t.Helper()
	rules, err := companies.GetScope(company)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rules {
		if r.Domain+r.Net == key {
			return r.ID
		}
	}
	t.Fatalf("rule %s not found", key)
	return 0
}

func TestUntestedAssetsListingFollowsOperatorGrants(t *testing.T) {
	d, assets, _ := testSetup(t)
	t.Cleanup(func() { _ = d.Close() })
	marker := fmt.Sprintf("untested-%d", time.Now().UnixNano())
	other, err := d.CreateTask("other engagement", "test", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	current, err := d.CreateTask("current engagement", "test", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(current.ID); _ = d.DeleteTask(other.ID) })
	foreignDomain, grantedDomain, ownDomain := marker+"-foreign.invalid", marker+"-granted.invalid", marker+"-own.invalid"
	var ids []int64
	t.Cleanup(func() { _, _ = assets.DeleteByIDs(ids) })
	upsert := func(domain string, task int64) int64 {
		t.Helper()
		id, err := assets.UpsertRootDomain(UpsertRootDomainReq{Domain: domain, TaskID: task})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		return id
	}
	foreign := upsert(foreignDomain, other.ID)
	granted := upsert(grantedDomain, other.ID)
	own := upsert(ownDomain, current.ID)
	for _, domain := range []string{foreignDomain, ownDomain} {
		if _, err := assets.AddAgentScope(current.ID, "root_domain", domain, "proposal", "agent"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := assets.AddAgentScope(current.ID, "root_domain", grantedDomain, "operator", "manual"); err != nil {
		t.Fatal(err)
	}
	listed, total, err := assets.ListUntestedAssetsWithSources(current.ID, "", 1000, 0)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int64]bool{}
	for _, a := range listed {
		seen[a.ID] = true
	}
	if seen[foreign] || !seen[granted] || !seen[own] || total != len(listed) {
		t.Fatalf("listing must hold granted and own assets only: foreign=%v granted=%v own=%v total=%d len=%d",
			seen[foreign], seen[granted], seen[own], total, len(listed))
	}
	cov, err := assets.TaskCoverageWithSources(current.ID)
	if err != nil || cov.Denominator != 3 {
		t.Fatalf("operator coverage keeps counting proposals: %+v %v", cov, err)
	}
	agentCov, err := assets.TaskCoverageForAgent(current.ID)
	if err != nil || agentCov.Denominator != 2 {
		t.Fatalf("agent counts must exclude assets outside the grants: %+v %v", agentCov, err)
	}
	hosts, err := assets.HostsByTaskForAgent(current.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hosts {
		if h == foreignDomain {
			t.Fatalf("agent host list exposes a foreign host: %v", hosts)
		}
	}
	if _, total, err := assets.ListUntestedAssetsWithSources(current.ID, "", 1, 0); err != nil || total != 2 {
		t.Fatalf("total must not depend on the page: %d %v", total, err)
	}
}
