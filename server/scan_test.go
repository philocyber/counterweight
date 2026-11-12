package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	actool "github.com/Autumn-27/norma/tool"
	"github.com/Philocyber/counterweight/agent"
	"github.com/Philocyber/counterweight/codesrc"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/locale"
	"github.com/Philocyber/counterweight/report"
	"github.com/Philocyber/counterweight/scanner"
)

// plantedSecret stands in for a credential. Fixtures place it in every field a
// scanner report could carry matched text in; it must appear nowhere afterwards.
const plantedSecret = "AKIAZZPLANTEDSECRET7890"

const scanGitleaksFixture = `[
 {"RuleID":"aws-access-token","Description":"AWS credentials api_key=` + plantedSecret + `","StartLine":14,
  "Match":"key = ` + plantedSecret + `","Secret":"` + plantedSecret + `","File":"/work/repo/config/prod.env","Author":"` + plantedSecret + `","Message":"` + plantedSecret + `"},
 {"RuleID":"generic-api-key","Description":"Generic API key","StartLine":3,"File":"/work/repo/deploy/ci.yml","Secret":"` + plantedSecret + `"}
]`

const scanOSVFixture = `{"results":[{"source":{"path":"/work/repo/web/package-lock.json"},"packages":[
 {"package":{"name":"lodash","version":"4.17.15","ecosystem":"npm"},
  "vulnerabilities":[{"id":"GHSA-p6mc-m468-83gw","aliases":["CVE-2020-8203"],"summary":"Prototype Pollution in lodash",
   "affected":[{"package":{"name":"lodash"},"ranges":[{"events":[{"introduced":"0"},{"fixed":"4.17.19"}]}]}]}],
  "groups":[{"ids":["GHSA-p6mc-m468-83gw","CVE-2020-8203"]}]},
 {"package":{"name":"minimist","version":"1.2.0","ecosystem":"npm"},
  "vulnerabilities":[{"id":"GHSA-xvch-5gv4-984h","summary":"Prototype Pollution in minimist","affected":[{"package":{"name":"minimist"},"ranges":[{"events":[{"fixed":"1.2.6"}]}]}]}]}]}]}`

type scanFixture struct {
	s  *Server
	m  *Manager
	t  *Task
	id int64
}

// newScanFixture builds a code-review task whose scanners are replaced by fakes
// that return the fixtures. run overrides the fake scanner (nil: the fixtures).
func newScanFixture(t *testing.T, run func(spec scanner.Spec, root string) (scanner.Result, error)) *scanFixture {
	t.Helper()
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("isolated PostgreSQL unavailable: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	s := newAdmissionTestServer(m, func(*Task) bool { return false })
	s.ctx = context.Background()
	s.dataDir = t.TempDir()
	s.registerScanRoutes(http.NewServeMux())

	oldRun, oldLook, oldWait, oldPoll := scanRun, scannerLookPath, scanWaitMax, scanWaitPoll
	t.Cleanup(func() { scanRun, scannerLookPath, scanWaitMax, scanWaitPoll = oldRun, oldLook, oldWait, oldPoll })
	scanWaitMax, scanWaitPoll = 400*time.Millisecond, 20*time.Millisecond
	// The fixture's clone path does not exist; file confinement would refuse the run before the tool list is built.
	oldRoots := agent.FileRoots
	agent.FileRoots = nil
	t.Cleanup(func() { agent.FileRoots = oldRoots })
	scannerLookPath = func(string) (string, error) { return "", os.ErrNotExist }
	if run == nil {
		run = func(spec scanner.Spec, root string) (scanner.Result, error) {
			var leads []scanner.Lead
			var err error
			if spec.Key == scanner.Gitleaks {
				leads, err = scanner.ParseGitleaks(root, []byte(scanGitleaksFixture))
			} else {
				leads, err = scanner.ParseOSV(root, []byte(scanOSVFixture))
			}
			return scanner.Result{Leads: leads, Version: "fake"}, err
		}
	}
	scanRun = func(_ context.Context, spec scanner.Spec, _, root string, _ []string) (scanner.Result, error) {
		return run(spec, root)
	}
	// Explicit configured paths make both scanners resolve.
	for _, spec := range scanner.Specs() {
		key := fmt.Sprintf(settingScannerPathFmt, spec.Key)
		_ = m.pg.SetSetting(key, os.Args[0])
		t.Cleanup(func() { _, _ = m.pg.Exec(`DELETE FROM settings WHERE key=$1`, key) })
	}
	t.Cleanup(func() { _, _ = m.pg.Exec(`DELETE FROM settings WHERE key=$1`, settingScannersEnabled) })

	repo, _ := codesrc.ParseRepo("acme/fixture")
	task, err := s.createCodeReviewFromClone(repo, codesrc.ModeLight, "/work/repo", "abc123", "", db.TaskCreateOptions{Language: locale.En})
	if err != nil {
		t.Fatal(err)
	}
	id, _ := strconv.ParseInt(task.ID, 10, 64)
	t.Cleanup(func() { _ = m.pg.DeleteTask(id) })
	f := &scanFixture{s: s, m: m, t: task, id: id}
	f.waitScans()
	return f
}

func (f *scanFixture) waitScans() {
	for i := 0; i < 200; i++ {
		runs, _ := f.m.pg.ListScanRuns(f.id)
		done := len(runs) > 0
		for _, r := range runs {
			done = done && r.Status != db.ScanRunning
		}
		if done {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// graphText gathers every text an agent could read from the task's graph and activity.
func (f *scanFixture) graphText(t *testing.T) (all string, hints, goals int) {
	t.Helper()
	var b strings.Builder
	for _, kind := range []string{db.KindHint, db.KindGoal, db.KindFact, db.KindIntent, db.KindFinding} {
		nodes, err := f.t.Store.ListByKind(kind, 500)
		if err != nil {
			t.Fatal(err)
		}
		switch kind {
		case db.KindHint:
			hints = len(nodes)
		case db.KindGoal:
			goals = len(nodes)
		}
		for _, n := range nodes {
			b.Write(n.Payload)
		}
	}
	acts, _, err := f.t.Store.ActivityList(nil, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range acts {
		b.WriteString(a.Summary + a.Detail)
	}
	b.WriteString(f.t.Goal + f.t.Description)
	return b.String(), hints, goals
}

func (f *scanFixture) toolNames() []string {
	ctx := agent.WithRunInfo(context.Background(), agent.RunInfo{AssessmentType: db.AssessmentCodeReview, TaskID: f.id})
	var names []string
	for _, key := range []string{db.CodeReviewPlannerAgentKey, db.CodeReviewWorkerAgentKey} {
		tools, _, cleanup := agent.AugmentTools(ctx, key, []actool.CoreTool{})
		cleanup()
		for _, tool := range tools {
			names = append(names, tool.Name())
		}
	}
	return names
}

func has(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func mentionsScanners(s string) bool {
	s = strings.ToLower(s)
	for _, w := range []string{"scanner", "gitleaks", "osv", "lead", "advisory", "resolve_scan_lead", "supplementary"} {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

func (f *scanFixture) addLeads(t *testing.T, kind string, n int, tag string) {
	t.Helper()
	var rows []db.ScanLead
	for i := 0; i < n; i++ {
		rows = append(rows, db.ScanLead{Scanner: scanner.Gitleaks, Kind: kind, RuleID: "rule", Title: "t", File: fmt.Sprintf("f%s%d.txt", kind, i),
			Line: i + 1, Fingerprint: fmt.Sprintf("%s-%s-%d", kind, tag, i)})
	}
	if _, err := f.m.pg.AddScanLeads(f.id, rows); err != nil {
		t.Fatal(err)
	}
}

// addAdvisories stores n advisories of one package version.
func (f *scanFixture) addAdvisories(t *testing.T, pkg string, n int, fixed string, severity float64) {
	t.Helper()
	var rows []db.ScanLead
	for i := 0; i < n; i++ {
		rows = append(rows, db.ScanLead{Scanner: scanner.OSV, Kind: scanner.KindAdvisory, RuleID: fmt.Sprintf("GHSA-%s-%d", pkg, i), Title: "t",
			File: "package-lock.json", Package: pkg, Version: "1.0.0", FixedVersion: fixed, SeverityRank: severity, Fingerprint: fmt.Sprintf("adv-%s-%d", pkg, i)})
	}
	if _, err := f.m.pg.AddScanLeads(f.id, rows); err != nil {
		t.Fatal(err)
	}
}

// Nothing about scanners reaches the agents before the review's end point, even
// though the scanners have already finished and stored leads.
func TestScannersInvisibleToAgentsBeforeReviewEnd(t *testing.T) {
	f := newScanFixture(t, nil)
	leads, _ := f.m.pg.ListScanLeads(f.id)
	if len(leads) != 4 {
		t.Fatalf("scanners should have stored 4 leads by now, got %d", len(leads))
	}
	text, hints, _ := f.graphText(t)
	if hints != 0 || mentionsScanners(text) {
		t.Fatalf("scanner text reached the graph, goal or activity before the review ended (hints=%d): %.300s", hints, text)
	}
	if names := f.toolNames(); has(names, "resolve_scan_lead") {
		t.Fatalf("review agents have the resolve tool before any lead was delivered: %v", names)
	}
	if f.s.reviewExtraTools(context.Background(), f.id) != nil {
		t.Fatal("no extra tools before delivery")
	}
	// Both ends of the review exist, and only the end-of-review hook delivers.
	if !f.s.engine.scanHandover(context.Background(), f.t) {
		t.Fatal("the end-of-review hook must deliver the stored leads")
	}
	after, hints, goals := f.graphText(t)
	if hints != 4 || goals < 1 || !strings.Contains(after, "Unverified automated lead") {
		t.Fatalf("after the end: hints=%d goals=%d", hints, goals)
	}
	if !has(f.toolNames(), "resolve_scan_lead") {
		t.Fatalf("the resolve tool must exist once leads were delivered: %v / %v", f.toolNames(), f.s.reviewExtraTools(context.Background(), f.id))
	}
}

// With nothing to deliver, nothing at all happens: no hint, no goal, no
// activity, no tool, and the task is free to complete.
func TestNoLeadsDeliversNothing(t *testing.T) {
	f := newScanFixture(t, func(scanner.Spec, string) (scanner.Result, error) { return scanner.Result{}, nil })
	beforeText, beforeHints, beforeGoals := f.graphText(t)
	if f.s.engine.scanHandover(context.Background(), f.t) {
		t.Fatal("zero leads must not hold the task open")
	}
	afterText, hints, goals := f.graphText(t)
	if afterText != beforeText || hints != beforeHints || goals != beforeGoals {
		t.Fatal("zero leads must change nothing the agents can see")
	}
	if has(f.toolNames(), "resolve_scan_lead") {
		t.Fatal("no tool without delivered leads")
	}
	f.t.trigMu.Lock()
	pending := len(f.t.pendingTriggers)
	f.t.trigMu.Unlock()
	if pending != 0 {
		t.Fatalf("no planner trigger expected, got %d", pending)
	}
}

// Unavailable scanners are recorded and reported, never an error, and the
// absence of results is not communicated to the agents either.
func TestUnavailableScannersAreSkippedQuietly(t *testing.T) {
	f := newScanFixture(t, nil)
	for _, spec := range scanner.Specs() {
		_, _ = f.m.pg.Exec(`DELETE FROM settings WHERE key=$1`, fmt.Sprintf(settingScannerPathFmt, spec.Key))
	}
	repo, _ := codesrc.ParseRepo("acme/other")
	task, err := f.s.createCodeReviewFromClone(repo, codesrc.ModeLight, "/work/other", "def", "", db.TaskCreateOptions{Language: locale.En})
	if err != nil {
		t.Fatal(err)
	}
	other := &scanFixture{s: f.s, m: f.m, t: task}
	other.id, _ = strconv.ParseInt(task.ID, 10, 64)
	t.Cleanup(func() { _ = f.m.pg.DeleteTask(other.id) })
	other.waitScans()
	runs, _ := f.m.pg.ListScanRuns(other.id)
	if len(runs) != 2 {
		t.Fatalf("runs=%d", len(runs))
	}
	for _, r := range runs {
		if r.Status != db.ScanUnavailable || !strings.Contains(r.Reason, "was not found") {
			t.Fatalf("run: %+v", r)
		}
	}
	if f.s.engine.scanHandover(context.Background(), task) {
		t.Fatal("nothing to deliver")
	}
	text, hints, _ := other.graphText(t)
	if hints != 0 || mentionsScanners(text) {
		t.Fatalf("unavailability leaked to the agents: %.200s", text)
	}
	st := f.s.scannerStatuses(locale.En)
	if len(st) != 2 || st[0].Available || st[0].Reason == "" || st[0].Step == "" {
		t.Fatalf("statuses: %+v", st)
	}
}

// Secrets are delivered one hint each (20 per round, up to a cap); advisories are
// delivered grouped by package, at most 8 packages per task, each exactly once.
func TestBatchingAndSingleDelivery(t *testing.T) {
	f := newScanFixture(t, func(scanner.Spec, string) (scanner.Result, error) { return scanner.Result{}, nil })
	for i := 0; i < 10; i++ { // package i has i+1 advisories
		f.addAdvisories(t, fmt.Sprintf("p%d", i), i+1, "9.9.9", 7.4)
	}
	f.addLeads(t, scanner.KindSecret, 25, "s")
	ctx := context.Background()

	if !f.s.engine.scanHandover(ctx, f.t) {
		t.Fatal("first round")
	}
	if _, hints, _ := f.graphText(t); hints != 20 {
		t.Fatalf("the first round is 20 secret hints, got %d", hints)
	}
	if got, _ := f.m.pg.ListScanLeadsByState(f.id, db.LeadDelivered); len(got) != 20 {
		t.Fatalf("delivered %d", len(got))
	}
	if !f.s.engine.scanHandover(ctx, f.t) {
		t.Fatal("second round")
	}
	if f.s.engine.scanHandover(ctx, f.t) {
		t.Fatal("nothing left that may be delivered")
	}
	_, hints, _ := f.graphText(t)
	if hints != 25+scanPackageHintMax {
		t.Fatalf("25 secrets plus %d package hints expected, got %d", scanPackageHintMax, hints)
	}
	delivered, _ := f.m.pg.ListScanLeadsByState(f.id, db.LeadDelivered)
	left, _ := f.m.pg.ListScanLeadsByState(f.id, db.LeadNew)
	adv := 0
	for _, l := range delivered {
		if l.Kind == scanner.KindAdvisory {
			adv++
			if l.Package == "p0" || l.Package == "p1" {
				t.Fatalf("packages beyond the cap must not be delivered: %s", l.Package)
			}
		}
	}
	// Packages p9..p2 (10+9+...+3 advisories) are delivered together; p1 and p0 stay new.
	if adv != 52 || len(left) != 3 {
		t.Fatalf("delivered advisories=%d new=%d", adv, len(left))
	}
	seen := map[int64]bool{}
	nodes, _ := f.t.Store.ListByKind(db.KindHint, 500)
	var big string
	for _, n := range nodes {
		var p struct {
			ID   int64  `json:"scan_lead_id"`
			Text string `json:"text"`
		}
		_ = json.Unmarshal(n.Payload, &p)
		if seen[p.ID] {
			t.Fatalf("lead %d delivered twice", p.ID)
		}
		seen[p.ID] = true
		if !strings.Contains(p.Text, "Unverified automated lead") || !strings.Contains(p.Text, "does not change the scope") ||
			!strings.Contains(p.Text, "Confirm or dismiss it by reading the code") {
			t.Fatalf("hint wording: %s", p.Text)
		}
		if strings.Contains(strings.ToLower(p.Text), "severity") || strings.Contains(p.Text, "7.4") {
			t.Fatalf("scanner severity must not appear in a hint: %s", p.Text)
		}
		if strings.Contains(p.Text, "package p9 ") {
			big = p.Text
		}
	}
	for _, want := range []string{"10 published advisories", "and 7 more", "package p9 at version 1.0.0", "A fixed version is 9.9.9", "resolves all 10 advisories"} {
		if !strings.Contains(big, want) {
			t.Fatalf("package hint lacks %q: %s", want, big)
		}
	}

	// The secret cap: more secrets than the cap leave the rest undelivered.
	f.addLeads(t, scanner.KindSecret, 80, "x")
	for f.s.engine.scanHandover(ctx, f.t) {
	}
	delivered, _ = f.m.pg.ListScanLeadsByState(f.id, db.LeadDelivered)
	secrets := 0
	for _, l := range delivered {
		if l.Kind == scanner.KindSecret {
			secrets++
		}
	}
	if secrets != scanDeliveryMax {
		t.Fatalf("delivered %d secrets, cap %d", secrets, scanDeliveryMax)
	}
}

// Packages are ordered by number of advisories, then by the scanner score,
// which orders only and is never stored on a finding or shown.
func TestPackageOrdering(t *testing.T) {
	f := newScanFixture(t, func(scanner.Spec, string) (scanner.Result, error) { return scanner.Result{}, nil })
	f.addAdvisories(t, "few", 2, "", 9.9)
	f.addAdvisories(t, "tielow", 4, "", 2.0)
	f.addAdvisories(t, "tiehigh", 4, "", 8.0)
	f.addAdvisories(t, "many", 6, "", 1.0)
	leads, _ := f.m.pg.ListScanLeadsByState(f.id, db.LeadNew)
	var order []string
	for _, u := range pickHintUnits(f.m.pg, f.id, leads) {
		order = append(order, u.group[0].Package)
	}
	if strings.Join(order, ",") != "many,tiehigh,tielow,few" {
		t.Fatalf("order: %v", order)
	}
}

// Resolving a package hint resolves every delivered advisory of the package with
// the same reason, except those a finding already named.
func TestPackageResolutionCoversAllAdvisories(t *testing.T) {
	f := newScanFixture(t, func(scanner.Spec, string) (scanner.Result, error) { return scanner.Result{}, nil })
	f.addAdvisories(t, "lodash", 3, "4.17.21", 0)
	f.addAdvisories(t, "other", 1, "", 0)
	if !f.s.engine.scanHandover(context.Background(), f.t) {
		t.Fatal("handover")
	}
	if _, err := f.m.pg.AddFinding(f.id, 0, "prototype-pollution", "Prototype pollution via lodash", "high", "see GHSA-lodash-1", "a.js:1", "w", nil); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/tasks/"+f.t.ID+"/scan", nil)
	req.SetPathValue("id", f.t.ID)
	f.s.getTaskScan(rec, req) // reconcile: the finding names advisory 1
	all, _ := f.m.pg.ListScanLeads(f.id)
	var rep int64
	for _, l := range all {
		if l.Package == "lodash" && l.State == db.LeadDelivered && (rep == 0 || l.ID < rep) {
			rep = l.ID
		}
	}
	raw, _ := json.Marshal(map[string]any{"lead_id": rep, "status": "dismissed", "reason": "not reachable"})
	if res, _ := f.s.toolResolveScanLead(f.id).Call(context.Background(), raw, nil); res.IsError {
		t.Fatal(res.Flatten())
	}
	all, _ = f.m.pg.ListScanLeads(f.id)
	for _, l := range all {
		switch {
		case l.Package == "lodash" && l.RuleID == "GHSA-lodash-1":
			if l.State != db.LeadConfirmed || l.ResolvedBy != "finding_match" {
				t.Fatalf("the advisory a finding named keeps its outcome: %+v", l)
			}
		case l.Package == "lodash":
			if l.State != db.LeadDismissed || l.Resolution != "not reachable" {
				t.Fatalf("package advisory not resolved together: %+v", l)
			}
		case l.Package == "other":
			if l.State != db.LeadDelivered {
				t.Fatalf("another package must be untouched: %+v", l)
			}
		}
	}
}

// Availability text distinguishes the container image from a native install.
func TestAvailabilityStepsDifferByEnvironment(t *testing.T) {
	f := newScanFixture(t, nil)
	for _, spec := range scanner.Specs() {
		_, _ = f.m.pg.Exec(`DELETE FROM settings WHERE key=$1`, fmt.Sprintf(settingScannerPathFmt, spec.Key))
	}
	old := scanInDocker
	t.Cleanup(func() { scanInDocker = old })
	scanInDocker = func() bool { return true }
	for _, st := range f.s.scannerStatuses(locale.En) {
		if st.Available || !strings.Contains(st.Reason, "container image") || !strings.Contains(st.Step, "rebuild") || strings.Contains(st.Step, "Download") {
			t.Fatalf("docker: %+v", st)
		}
	}
	scanInDocker = func() bool { return false }
	want := map[string]string{scanner.OSV: "https://github.com/google/osv-scanner/releases", scanner.Gitleaks: "https://github.com/gitleaks/gitleaks/releases"}
	for _, st := range f.s.scannerStatuses(locale.En) {
		if !strings.Contains(st.Step, want[st.Key]) || !strings.Contains(st.Step, "PATH") || !strings.Contains(st.Step, "settings") {
			t.Fatalf("native: %+v", st)
		}
	}
}

// A finding the review already recorded for the same file and class covers the
// lead: it is linked to that finding and not sent again.
func TestLeadsDeduplicatedAgainstExistingFindings(t *testing.T) {
	f := newScanFixture(t, nil)
	fid, err := f.m.pg.AddFinding(f.id, 0, "hardcoded-secret", "Hardcoded AWS key", "high", "A key is committed", "config/prod.env:14 contains a hardcoded key", "worker", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !f.s.engine.scanHandover(context.Background(), f.t) {
		t.Fatal("other leads remain")
	}
	leads, _ := f.m.pg.ListScanLeads(f.id)
	for _, l := range leads {
		if l.File == "config/prod.env" {
			if l.State != db.LeadConfirmed || l.FindingID == nil || *l.FindingID != fid || l.ResolvedBy != "existing_finding" {
				t.Fatalf("covered lead: %+v", l)
			}
		} else if l.State != db.LeadDelivered {
			t.Fatalf("uncovered lead should be delivered: %+v", l)
		}
	}
	text, _, _ := f.graphText(t)
	if strings.Contains(text, "config/prod.env") {
		t.Fatal("a covered lead must not be sent again")
	}
}

// Scanners still running when the review ends are waited for a bounded time;
// late leads stay visible and undelivered, and the task is not restarted.
func TestLateLeadsStayUndelivered(t *testing.T) {
	f := newScanFixture(t, func(scanner.Spec, string) (scanner.Result, error) { return scanner.Result{}, nil })
	if err := f.m.pg.BeginScanRun(f.id, scanner.OSV); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if f.s.engine.scanHandover(context.Background(), f.t) {
		t.Fatal("nothing stored yet")
	}
	if d := time.Since(start); d < scanWaitMax || d > scanWaitMax+3*time.Second {
		t.Fatalf("expected a bounded wait of about %s, waited %s", scanWaitMax, d)
	}
	f.addLeads(t, scanner.KindSecret, 1, "")
	_ = f.m.pg.FinishScanRun(f.id, scanner.OSV, db.ScanOK, "", "fake", 1, 1)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/tasks/"+f.t.ID+"/scan", nil)
	req.SetPathValue("id", f.t.ID)
	f.s.getTaskScan(rec, req)
	var out struct {
		Counts map[string]int `json:"counts"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Counts[db.LeadNew] != 1 {
		t.Fatalf("late lead must be visible as new: %s", rec.Body.String())
	}
}

// A secret value planted in fixture output appears in no stored column, hint,
// API response, report section or tool result.
func TestPlantedSecretNeverAppearsAnywhere(t *testing.T) {
	f := newScanFixture(t, nil)
	if !f.s.engine.scanHandover(context.Background(), f.t) {
		t.Fatal("handover")
	}
	var dump strings.Builder
	rows, err := f.m.pg.Query(`SELECT to_jsonb(l)::text FROM scan_leads l WHERE task_id=$1 UNION ALL SELECT to_jsonb(r)::text FROM scan_runs r WHERE task_id=$1`, f.id)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		dump.WriteString(s)
	}
	rows.Close()
	graph, _, _ := f.graphText(t)
	dump.WriteString(graph)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/tasks/"+f.t.ID+"/scan", nil)
	req.SetPathValue("id", f.t.ID)
	f.s.getTaskScan(rec, req)
	dump.WriteString(rec.Body.String())

	in := report.Input{CodeReview: &report.CodeReview{Repository: "acme/fixture"}, FindingRecords: []*db.DBFinding{}}
	f.s.attachScanSections(&in, f.t)
	if len(in.SecretLeads) != 2 || len(in.Advisories) != 2 {
		t.Fatalf("report input: %d secrets %d advisories", len(in.SecretLeads), len(in.Advisories))
	}
	dump.WriteString(report.Markdown(in))
	view, _ := json.Marshal(report.BuildView(in))
	dump.Write(view)

	tool := f.s.toolResolveScanLead(f.id)
	leads, _ := f.m.pg.ListScanLeads(f.id)
	raw, _ := json.Marshal(map[string]any{"lead_id": leads[0].ID, "status": "dismissed", "reason": "placeholder api_key=" + plantedSecret})
	res, _ := tool.Call(context.Background(), raw, nil)
	dump.WriteString(res.Flatten())
	l, _ := f.m.pg.GetScanLead(f.id, leads[0].ID)
	dump.WriteString(l.Resolution)

	if strings.Contains(dump.String(), plantedSecret) {
		i := strings.Index(dump.String(), plantedSecret)
		t.Fatalf("the planted secret value was stored or shown: ...%s...", dump.String()[max(0, i-60):min(len(dump.String()), i+60)])
	}
	if !strings.Contains(dump.String(), "[REDACTED]") {
		t.Fatal("expected the last-line redaction to have fired on the description")
	}
}

// Leads become confirmed or dismissed through the tool, or when a later finding
// cites the same file; the tool refuses what it should.
func TestLeadResolution(t *testing.T) {
	f := newScanFixture(t, nil)
	if !f.s.engine.scanHandover(context.Background(), f.t) {
		t.Fatal("handover")
	}
	all, _ := f.m.pg.ListScanLeads(f.id)
	var leads []*db.ScanLead // the two secret leads: the advisories are used below
	for _, l := range all {
		if l.Kind == scanner.KindSecret {
			leads = append(leads, l)
		}
	}
	tool := f.s.toolResolveScanLead(f.id)
	call := func(args map[string]any) actool.Result {
		raw, _ := json.Marshal(args)
		res, _ := tool.Call(context.Background(), raw, nil)
		return res
	}
	if res := call(map[string]any{"lead_id": leads[0].ID, "status": "dismissed"}); !res.IsError {
		t.Fatal("a dismissal needs a reason")
	}
	if res := call(map[string]any{"lead_id": 999999, "status": "confirmed"}); !res.IsError {
		t.Fatal("unknown lead")
	}
	if res := call(map[string]any{"lead_id": leads[0].ID, "status": "maybe"}); !res.IsError {
		t.Fatal("bad status")
	}
	if res := call(map[string]any{"lead_id": leads[0].ID, "status": "dismissed", "reason": "test fixture value"}); res.IsError {
		t.Fatal(res.Flatten())
	}
	if l, _ := f.m.pg.GetScanLead(f.id, leads[0].ID); l.State != db.LeadDismissed || l.Resolution != "test fixture value" || l.ResolvedBy != "agent" {
		t.Fatalf("dismissed lead: %+v", l)
	}
	other, _ := f.m.pg.AddFinding(0, 0, "x", "x", "low", "x", "x", "w", nil)
	t.Cleanup(func() { _, _ = f.m.pg.Exec(`DELETE FROM findings WHERE id=$1`, other) })
	if res := call(map[string]any{"lead_id": leads[1].ID, "status": "confirmed", "finding_id": other}); !res.IsError {
		t.Fatal("a finding of another task is refused")
	}
	own, _ := f.m.pg.AddFinding(f.id, 0, "x", "x", "low", "x", "x", "w", nil)
	if res := call(map[string]any{"lead_id": leads[1].ID, "status": "confirmed", "finding_id": own}); res.IsError {
		t.Fatal(res.Flatten())
	}
	if l, _ := f.m.pg.GetScanLead(f.id, leads[1].ID); l.State != db.LeadConfirmed || l.FindingID == nil || *l.FindingID != own {
		t.Fatalf("confirmed lead: %+v", l)
	}

	// A finding recorded after delivery that names the advisory confirms its lead.
	var adv *db.ScanLead
	for _, l := range all {
		if l.Kind == scanner.KindAdvisory && l.State == db.LeadDelivered && l.RuleID == "GHSA-p6mc-m468-83gw" {
			adv = l
		}
	}
	if adv == nil {
		t.Fatal("advisory lead not found")
	}
	matched, _ := f.m.pg.AddFinding(f.id, 0, "prototype-pollution", "Prototype pollution reachable through lodash", "high", "Request data reaches _.merge; see GHSA-p6mc-m468-83gw", "server/a.js:10", "w", nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/tasks/"+f.t.ID+"/scan", nil)
	req.SetPathValue("id", f.t.ID)
	f.s.getTaskScan(rec, req)
	if l, _ := f.m.pg.GetScanLead(f.id, adv.ID); l.State != db.LeadConfirmed || l.FindingID == nil || *l.FindingID != matched || l.ResolvedBy != "finding_match" {
		t.Fatalf("lead matched by a later finding: %+v", l)
	}
}

// The API reports runs, leads with states and scanner availability, and the
// setting turns the feature off completely.
func TestScanAPIAndSetting(t *testing.T) {
	f := newScanFixture(t, nil)
	get := func() map[string]any {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/tasks/"+f.t.ID+"/scan", nil)
		req.SetPathValue("id", f.t.ID)
		f.s.getTaskScan(rec, req)
		if rec.Code != 200 {
			t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
		}
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}
	out := get()
	if out["applicable"] != true || out["enabled"] != true || len(out["runs"].([]any)) != 2 || len(out["leads"].([]any)) != 4 ||
		len(out["scanners"].([]any)) != 2 || !strings.Contains(out["note"].(string), "git history is not scanned") {
		t.Fatalf("api: %v", out)
	}
	lead := out["leads"].([]any)[0].(map[string]any)
	for _, k := range []string{"id", "scanner", "kind", "rule_id", "state", "fingerprint"} {
		if _, ok := lead[k]; !ok {
			t.Fatalf("lead lacks %q: %v", k, lead)
		}
	}

	put := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		f.s.putScanSettings(rec, httptest.NewRequest("PUT", "/api/scan/settings", strings.NewReader(body)))
		return rec
	}
	if rec := put(`{"paths":{"osv-scanner":"relative/osv"}}`); rec.Code != 400 {
		t.Fatalf("a relative path is refused: %d", rec.Code)
	}
	if rec := put(`{"paths":{"nope":"/x"}}`); rec.Code != 400 {
		t.Fatalf("unknown scanner: %d", rec.Code)
	}
	if rec := put(`{"enabled":false}`); rec.Code != 200 {
		t.Fatalf("disable: %d %s", rec.Code, rec.Body.String())
	}
	if f.s.scannersEnabled() {
		t.Fatal("setting not applied")
	}
	if f.s.engine.scanHandover(context.Background(), f.t) {
		t.Fatal("with scanners off nothing is delivered even if leads exist")
	}
	repo, _ := codesrc.ParseRepo("acme/off")
	task, err := f.s.createCodeReviewFromClone(repo, codesrc.ModeLight, "/work/off", "c", "", db.TaskCreateOptions{Language: locale.En})
	if err != nil {
		t.Fatal(err)
	}
	offID, _ := strconv.ParseInt(task.ID, 10, 64)
	t.Cleanup(func() { _ = f.m.pg.DeleteTask(offID) })
	time.Sleep(150 * time.Millisecond)
	if runs, _ := f.m.pg.ListScanRuns(offID); len(runs) != 0 {
		t.Fatalf("scanners off must not run: %v", runs)
	}
	if rec := put(`{"enabled":true}`); rec.Code != 200 || !f.s.scannersEnabled() {
		t.Fatal("re-enable")
	}

	// A task that is not a code review reports itself as not applicable.
	gen, err := f.m.CreateTaskWithOptions("general", "goal", db.TaskCreateOptions{Language: locale.En})
	if err != nil {
		t.Fatal(err)
	}
	genID, _ := strconv.ParseInt(gen.ID, 10, 64)
	t.Cleanup(func() { _ = f.m.pg.DeleteTask(genID) })
	if f.s.engine.scanHandover(context.Background(), gen) {
		t.Fatal("only code reviews hand over")
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/tasks/"+gen.ID+"/scan", nil)
	req.SetPathValue("id", gen.ID)
	f.s.getTaskScan(rec, req)
	if !strings.Contains(rec.Body.String(), `"applicable":false`) {
		t.Fatalf("general task: %s", rec.Body.String())
	}
}

func TestSettingDefaultsOn(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("isolated PostgreSQL unavailable: %v", err)
	}
	defer m.Close()
	_, _ = m.pg.Exec(`DELETE FROM settings WHERE key=$1`, settingScannersEnabled)
	if !(&Server{m: m}).scannersEnabled() {
		t.Fatal("supplementary scanners default to on")
	}
}

// Scanner failures are recorded on the run and never reach the task.
func TestFailedScannerIsRecordedNotRaised(t *testing.T) {
	f := newScanFixture(t, func(spec scanner.Spec, _ string) (scanner.Result, error) {
		return scanner.Result{}, fmt.Errorf("exited with status 2: panic at /home/user/.cache password=hunter2hunter2")
	})
	runs, _ := f.m.pg.ListScanRuns(f.id)
	if len(runs) != 2 {
		t.Fatalf("runs: %v", runs)
	}
	for _, r := range runs {
		if r.Status != db.ScanFailed || !strings.Contains(r.Reason, "status 2") || strings.Contains(r.Reason, "hunter2hunter2") {
			t.Fatalf("run: %+v", r)
		}
	}
	if f.s.engine.scanHandover(context.Background(), f.t) {
		t.Fatal("failed scanners deliver nothing")
	}
}

// The planner loop calls the hook where a goalless review would complete: with
// leads the task stays open and gets hints and a goal; without, it completes as
// it always did.
func TestPlannerLoopHandsOverBeforeCompleting(t *testing.T) {
	run := func(t *testing.T, leads bool) (status string, hints, goals int) {
		var fn func(scanner.Spec, string) (scanner.Result, error)
		if !leads {
			fn = func(scanner.Spec, string) (scanner.Result, error) { return scanner.Result{}, nil }
		}
		f := newScanFixture(t, fn)
		f.s.engine.debounce = 20 * time.Millisecond
		f.s.engine.SetAuthoritativeAgentResolver(func(*Task) (*agent.Planner, *agent.Worker) { return new(agent.Planner), new(agent.Worker) })
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { f.s.engine.plannerLoop(ctx, f.t); close(done) }()
		f.t.Notify()
		for i := 0; i < 200; i++ {
			_, hints, goals = f.graphText(t)
			if hints > 0 || isTerminalStatus(f.t.lifecycleSnapshot().Status) {
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
		cancel() // stop before the follow-up planning round would need a model
		<-done
		// The loop above can stop while the handover is still writing: count
		// again once the planner loop has returned.
		_, hints, goals = f.graphText(t)
		return f.t.lifecycleSnapshot().Status, hints, goals
	}
	if status, hints, goals := run(t, true); isTerminalStatus(status) || hints != 4 || goals != 1 {
		t.Fatalf("with leads the review must stay open: status=%s hints=%d goals=%d", status, hints, goals)
	}
	if status, hints, _ := run(t, false); status != "done" || hints != 0 {
		t.Fatalf("without leads the review completes unchanged: status=%s hints=%d", status, hints)
	}
}
