package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	actool "github.com/Autumn-27/norma/tool"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/locale"
	"github.com/Philocyber/counterweight/recontools"
	"github.com/Philocyber/counterweight/report"
	"github.com/Philocyber/counterweight/scanner"
	"github.com/Philocyber/counterweight/selfupdate"
)

// Supplementary scanners are a supplement to the model's own review, never its
// basis. The review agents are not told scanners exist until the review has
// reached its natural end; only then are unverified leads handed over (see
// Engine.scanHandover). Nothing here writes to the exploration graph, the
// activity stream or any agent input before that point.

const (
	settingScannersEnabled = "supplementary_scanners_enabled"
	settingScannerPathFmt  = "scanner_path_%s"

	scanBatchSize      = 20 // leads handed to the review per round
	scanDeliveryMax    = 60 // secret hints handed to one task in total; the rest stay visible, undelivered
	scanPackageHintMax = 8  // packages whose advisories are handed to one task; the rest stay visible, undelivered
	maxLeadsPerScanner = 1000
	scanRunAbandoned   = 15 * time.Minute // a run still marked running after this is treated as dead
)

var (
	// scanWaitMax bounds how long a finished review waits for scanners still running.
	scanWaitMax  = 3 * time.Minute
	scanWaitPoll = 2 * time.Second
	// scannerLookPath is exec.LookPath, replaceable in tests.
	scannerLookPath = exec.LookPath
	// scanRun executes a scanner, replaceable in tests so they need no binaries.
	scanRun = scanner.Run
	// scanInDocker reports container execution, replaceable in tests.
	scanInDocker = selfupdate.InDocker
)

func (s *Server) scannersEnabled() bool { return s.m.pg.GetBool(settingScannersEnabled, true) }

// resolveScanner finds a scanner binary: configured path, managed tools directory, PATH.
func (s *Server) resolveScanner(key string) (scanner.Resolution, bool) {
	configured, _, _ := s.m.pg.GetSetting(fmt.Sprintf(settingScannerPathFmt, key))
	return scanner.Resolve(strings.TrimSpace(configured), recontools.BinPath(s.dataDir, key), key, scannerLookPath)
}

// scannerStatus is one scanner's availability for the console.
type scannerStatus struct {
	Key       string `json:"key"`
	Purpose   string `json:"purpose"`
	Available bool   `json:"available"`
	Source    string `json:"source,omitempty"` // configured | managed | path
	Reason    string `json:"reason,omitempty"`
	Step      string `json:"step,omitempty"` // the one step that makes it available
}

func (s *Server) scannerStatuses(lang locale.Lang) []scannerStatus {
	out := []scannerStatus{}
	for _, spec := range scanner.Specs() {
		st := scannerStatus{Key: spec.Key, Purpose: locale.Text(lang, spec.Purpose)}
		if res, ok := s.resolveScanner(spec.Key); ok {
			st.Available, st.Source = true, res.Source
		} else if scanInDocker() {
			st.Reason = locale.Text(lang, "%s is missing from this container image, which should include it", spec.Key)
			st.Step = locale.Text(lang, "The image is outdated: rebuild it so it includes %s", spec.Key)
		} else {
			st.Reason = locale.Text(lang, "%s was not found: no path is configured, it is not in the tools directory and it is not on PATH", spec.Key)
			st.Step = locale.Text(lang, "Download the %s release binary from %s and place it on PATH, or set its path in the scanner settings", spec.Key, spec.ReleasesURL)
		}
		out = append(out, st)
	}
	return out
}

// startScanners runs the scanners over a new review's clone in the background.
// They are independent of the model and of the review's progress. Each run is
// recorded before this returns, so a review that ends at once still sees them
// as in progress.
func (s *Server) startScanners(taskID int64, root string) {
	if taskID <= 0 || root == "" || !s.scannersEnabled() {
		return
	}
	var wg sync.WaitGroup
	for _, spec := range scanner.Specs() {
		if err := s.m.pg.BeginScanRun(taskID, spec.Key); err != nil {
			log.Printf("[scan] task %d: could not record %s run: %v", taskID, spec.Key, err)
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.runScanner(taskID, spec, root)
		}()
	}
	go wg.Wait()
}

// runScanner runs one scanner and records its outcome. It never returns an
// error to the task: a missing or failing scanner only changes its own run.
func (s *Server) runScanner(taskID int64, spec scanner.Spec, root string) {
	pg := s.m.pg
	finish := func(status, reason, version string, found, stored int) {
		if err := pg.FinishScanRun(taskID, spec.Key, status, reason, version, found, stored); err != nil {
			log.Printf("[scan] task %d: could not record %s outcome: %v", taskID, spec.Key, err)
		}
	}
	defer func() {
		if r := recover(); r != nil {
			finish(db.ScanFailed, "internal error", "", 0, 0)
		}
	}()
	res, ok := s.resolveScanner(spec.Key)
	if !ok {
		finish(db.ScanUnavailable, locale.Text(locale.ServerDefault(), "%s was not found: no path is configured, it is not in the tools directory and it is not on PATH", spec.Key), "", 0, 0)
		return
	}
	out, err := scanRun(s.ctx, spec, res.Path, root, scanner.BaseEnv())
	if err != nil {
		finish(db.ScanFailed, scanReason(err.Error()), out.Version, 0, 0)
		return
	}
	leads := out.Leads
	found := len(leads)
	if len(leads) > maxLeadsPerScanner {
		leads = leads[:maxLeadsPerScanner]
	}
	rows := make([]db.ScanLead, len(leads))
	for i, l := range leads {
		rows[i] = storableLead(l)
	}
	if _, err := pg.AddScanLeads(taskID, rows); err != nil {
		finish(db.ScanFailed, "could not store leads", out.Version, found, 0)
		return
	}
	finish(db.ScanOK, "", out.Version, found, len(rows))
}

// scanReason makes a scanner's failure text safe to store and show.
func scanReason(s string) string {
	return report.RedactLocalPaths(report.RedactSecrets(strings.TrimSpace(s)))
}

// storableLead converts a parsed lead for storage. Free text is passed through
// the report redactors as a last line of defence: the parsers already read no
// field that can hold a secret value.
func storableLead(l scanner.Lead) db.ScanLead {
	return db.ScanLead{
		Scanner: l.Scanner, Kind: l.Kind, RuleID: l.RuleID,
		Title: scanReason(l.Title), Detail: scanReason(l.Detail),
		File: l.File, Line: l.Line, Package: l.Package, Version: l.Version, FixedVersion: l.FixedVersion,
		Aliases: strings.Join(l.Aliases, " "), Fingerprint: l.Fingerprint, SeverityRank: l.Severity,
	}
}

// --- matching leads to findings ---

var (
	secretClassRe = regexp.MustCompile(`(?i)secret|credential|hard-?coded|api[ _-]?key|token|password|private key`)
	depClassRe    = regexp.MustCompile(`(?i)dependenc|vulnerable component|outdated|supply|known vuln|\bcve\b`)
)

// leadMatchesFinding reports whether a finding covers the same ground as a
// lead: for a secret, a finding of a credential class citing the same file; for
// an advisory, a finding naming the advisory (or an alias), or a dependency
// finding naming the package.
func leadMatchesFinding(l *db.ScanLead, f *db.DBFinding) bool {
	text := strings.ToLower(f.Name + "\n" + f.VulnClass + "\n" + f.Summary + "\n" + f.Evidence)
	class := f.VulnClass + " " + f.Name + " " + f.Summary
	switch l.Kind {
	case scanner.KindSecret:
		return l.File != "" && strings.Contains(text, strings.ToLower(l.File)) && secretClassRe.MatchString(class)
	case scanner.KindAdvisory:
		for _, id := range append([]string{l.RuleID}, strings.Fields(l.Aliases)...) {
			if id != "" && strings.Contains(text, strings.ToLower(id)) {
				return true
			}
		}
		if l.Package == "" || !depClassRe.MatchString(class) {
			return false
		}
		re, err := regexp.Compile(`(?i)(^|[^a-z0-9_.-])` + regexp.QuoteMeta(l.Package) + `($|[^a-z0-9_.-])`)
		return err == nil && re.MatchString(f.Name+"\n"+f.Summary+"\n"+f.Evidence)
	}
	return false
}

// reconcileScanLeads records, on delivered leads, that a finding recorded after
// the hand-over covers the same file or advisory.
func reconcileScanLeads(pg *db.DB, taskID int64, findings []*db.DBFinding) {
	delivered, err := pg.ListScanLeadsByState(taskID, db.LeadDelivered)
	if err != nil {
		return
	}
	for _, l := range delivered {
		if l.DeliveredAt == nil {
			continue
		}
		for _, f := range findings {
			if f.CreatedAt.After(*l.DeliveredAt) && leadMatchesFinding(l, f) {
				_, _ = pg.ResolveScanLead(taskID, l.ID, db.LeadConfirmed, f.ID, "", "finding_match")
				break
			}
		}
	}
}

// --- hand-over at the end of the review ---

// scanHandover is called at the two places where the engine concludes that a
// review has nothing left to plan and the task would complete. When there are
// undelivered leads it hands one batch to the task as hints plus one goal that
// keeps the review going until they are verified, and returns true: the caller
// must then NOT complete the task. With no leads it does nothing at all and
// returns false, so the task ends exactly as it would without this feature.
func (e *Engine) scanHandover(ctx context.Context, t *Task) bool {
	if taskAssessmentType(t) != db.AssessmentCodeReview {
		return false
	}
	pg := e.m.pg
	if !pg.GetBool(settingScannersEnabled, true) {
		return false
	}
	taskID, err := strconv.ParseInt(t.ID, 10, 64)
	if err != nil {
		return false
	}
	if !waitForScans(ctx, pg, taskID) {
		return false
	}
	pending, err := pg.ListScanLeadsByState(taskID, db.LeadNew)
	if err != nil || len(pending) == 0 {
		return false
	}
	findings := listTaskFindings(pg, taskID)
	reconcileScanLeads(pg, taskID, findings)

	// A finding the review already recorded for the same file and class covers
	// the lead: it is linked to that finding instead of being sent again.
	var candidates []*db.ScanLead
	for _, l := range pending {
		covered := false
		for _, f := range findings {
			if leadMatchesFinding(l, f) {
				_, _ = pg.ResolveScanLead(taskID, l.ID, db.LeadConfirmed, f.ID, "", "existing_finding")
				covered = true
				break
			}
		}
		if !covered {
			candidates = append(candidates, l)
		}
	}
	units := pickHintUnits(pg, taskID, candidates)
	if len(units) == 0 {
		return false
	}
	var ids []int64
	for _, u := range units {
		for _, l := range u.group {
			ids = append(ids, l.ID)
		}
	}
	claimed, err := pg.ClaimScanLeads(taskID, ids)
	if err != nil || len(claimed) == 0 {
		return false
	}
	won := map[int64]bool{}
	for _, id := range claimed {
		won[id] = true
	}

	lang := taskLanguage(pg, t.ID)
	var texts []string
	for _, u := range units {
		var mine, lost []int64
		for _, l := range u.group {
			if won[l.ID] {
				mine = append(mine, l.ID)
			}
		}
		if len(mine) == 0 {
			continue
		}
		text := leadHint(lang, u.group[0], u.group)
		if _, err := t.Store.AddNode(db.KindHint, map[string]any{"text": text, "scan_lead_id": u.group[0].ID}, 0, "active", "system", nil); err != nil {
			lost = mine
		}
		if len(lost) > 0 {
			_ = pg.UnclaimScanLeads(taskID, lost)
			continue
		}
		texts = append(texts, text)
	}
	if len(texts) == 0 {
		return false
	}
	goal := locale.Text(lang, "Verify the %d unverified automated leads delivered in the hints that mention a lead number: read the code for each, record a finding with report_finding only if the lead is a real issue, and resolve every lead with resolve_scan_lead.", len(texts))
	if gid, err := t.Store.AddGoal(map[string]any{"text": goal}, "system"); err == nil {
		if origin, _ := t.Store.OriginFactID(); origin > 0 {
			_ = t.Store.Link(origin, db.RelSpawns, gid)
		}
	}
	e.emitActivity(t, db.Activity{Worker: "system", Kind: "text",
		Summary: locale.Text(lang, "The review reached its end. %d unverified automated leads were handed over for verification.", len(texts))})
	t.NotifyHint(texts)
	return true
}

func listTaskFindings(pg *db.DB, taskID int64) []*db.DBFinding {
	fs, _, err := pg.ListFindingsPage(db.FindingFilter{TaskID: strconv.FormatInt(taskID, 10)}, 1, 5000)
	if err != nil {
		return nil
	}
	return fs
}

// waitForScans waits, up to scanWaitMax, for the task's scanners to finish. It
// returns false when the context ends first.
func waitForScans(ctx context.Context, pg *db.DB, taskID int64) bool {
	deadline := time.Now().Add(scanWaitMax)
	for {
		n, err := pg.ScanRunsInProgress(taskID, time.Now().Add(-scanRunAbandoned))
		if err != nil || n == 0 || !time.Now().Before(deadline) {
			return ctx.Err() == nil
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(scanWaitPoll):
		}
	}
}

// hintUnit is what one hint covers: a single secret lead, or every advisory of
// one package at one version. group[0] is the representative lead.
type hintUnit struct{ group []*db.ScanLead }

func packageKey(l *db.ScanLead) string { return l.Package + "@" + l.Version }

// pickHintUnits chooses what the next hand-over delivers. Secrets go first, one
// hint each. Advisories are grouped by package: at most scanPackageHintMax
// packages per task, ordered by number of advisories and then by the highest
// scanner score (used only to order, never shown). The advisories of packages
// beyond the cap stay new.
func pickHintUnits(pg *db.DB, taskID int64, candidates []*db.ScanLead) []hintUnit {
	all, _ := pg.ListScanLeads(taskID)
	deliveredSecrets, deliveredPkgs := 0, map[string]bool{}
	for _, l := range all {
		if l.DeliveredAt == nil {
			continue
		}
		if l.Kind == scanner.KindSecret {
			deliveredSecrets++
		} else {
			deliveredPkgs[packageKey(l)] = true
		}
	}
	var units []hintUnit
	secretRoom := min(scanBatchSize, scanDeliveryMax-deliveredSecrets)
	groups := map[string][]*db.ScanLead{}
	var keys []string
	for _, l := range candidates {
		if l.Kind == scanner.KindSecret {
			if len(units) < secretRoom {
				units = append(units, hintUnit{[]*db.ScanLead{l}})
			}
			continue
		}
		k := packageKey(l)
		if deliveredPkgs[k] {
			continue
		}
		if _, ok := groups[k]; !ok {
			keys = append(keys, k)
		}
		groups[k] = append(groups[k], l)
	}
	maxSev := func(g []*db.ScanLead) float64 {
		m := 0.0
		for _, l := range g {
			m = max(m, l.SeverityRank)
		}
		return m
	}
	sort.SliceStable(keys, func(i, j int) bool {
		a, b := groups[keys[i]], groups[keys[j]]
		if len(a) != len(b) {
			return len(a) > len(b)
		}
		return maxSev(a) > maxSev(b)
	})
	pkgRoom := min(scanPackageHintMax-len(deliveredPkgs), scanBatchSize-len(units))
	for _, k := range keys {
		if pkgRoom <= 0 {
			break
		}
		units = append(units, hintUnit{groups[k]})
		pkgRoom--
	}
	return units
}

// oneLine collapses text taken from the repository or a scanner into a short
// single line, so it cannot carry instructions across lines into a hint.
func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}

// leadHint words one lead as an unverified pointer. It states the scanner, the
// location and what to check, and never a verdict or a severity.
func leadHint(lang locale.Lang, l *db.ScanLead, group []*db.ScanLead) string {
	preface := locale.Text(lang, "Unverified automated lead #%d (not a finding; it does not change the scope of this review). ", l.ID)
	closing := locale.Text(lang, " Confirm or dismiss it by reading the code. If it is a real issue, record it with report_finding as usual, never writing a secret value into a finding. Then call resolve_scan_lead with lead_id=%d and status confirmed (with finding_id) or dismissed (with the reason).", l.ID)
	file := oneLine(l.File, 200)
	if l.Kind == scanner.KindSecret {
		loc := file
		if l.Line > 0 {
			loc += ":" + strconv.Itoa(l.Line)
		}
		return preface + locale.Text(lang, "Scanner %s matched rule \"%s\" at %s, a possible committed secret. Read that location and judge whether it is a real credential or a placeholder, example or test value.", l.Scanner, oneLine(l.RuleID, 80), loc) + closing
	}
	var names, fixes []string
	for _, a := range group {
		names = append(names, oneLine(a.RuleID, 60))
		fixes = append(fixes, a.FixedVersion)
	}
	listed := strings.Join(names[:min(3, len(names))], ", ")
	if len(names) > 3 {
		listed += locale.Text(lang, " and %d more", len(names)-3)
	}
	fix := ""
	if f := scanner.HighestFixed(fixes); f != "" {
		fix = locale.Text(lang, " A fixed version is %s.", oneLine(f, 80))
	}
	return preface + locale.Text(lang, "Scanner %s lists %d published advisories (%s) for package %s at version %s in %s. This only says the installed version is listed as affected, not that the code is exposed. Check by reading the code whether this code uses the affected functionality with untrusted input.%s", l.Scanner, len(group), listed, oneLine(l.Package, 120), oneLine(l.Version, 60), file, fix) + closing + locale.Text(lang, " Resolving this lead resolves all %d advisories of the package.", len(group))
}

// --- the optional resolve tool ---

// reviewExtraTools returns the tools added to review agents. Until a lead has
// been delivered to the task it returns nothing, so the agents have neither the
// tool nor any text about it.
func (s *Server) reviewExtraTools(_ context.Context, taskID int64) []actool.CoreTool {
	if s == nil || s.m == nil || taskID <= 0 {
		return nil
	}
	n, err := s.m.pg.CountScanLeadsDelivered(taskID)
	if err != nil || n == 0 {
		return nil
	}
	return []actool.CoreTool{s.toolResolveScanLead(taskID)}
}

func (s *Server) toolResolveScanLead(taskID int64) actool.CoreTool {
	lang := locale.ServerDefault()
	return wrTool("resolve_scan_lead",
		locale.Text(lang, "Record the outcome of an unverified automated lead delivered in a hint, after reading the code. status=confirmed when the lead is a real issue (pass the finding_id returned by report_finding when you recorded one); status=dismissed when it is not, with the reason."),
		objSchema(map[string]any{
			"lead_id":    map[string]any{"type": "integer", "description": locale.Text(lang, "The lead number from the hint")},
			"status":     map[string]any{"type": "string", "enum": []string{"confirmed", "dismissed"}, "description": locale.Text(lang, "confirmed or dismissed")},
			"finding_id": map[string]any{"type": "integer", "description": locale.Text(lang, "Optional: the finding_id returned by report_finding for a confirmed lead")},
			"reason":     strParam(locale.Text(lang, "Why the lead is a real issue or not; required when dismissing")),
		}, "lead_id", "status"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			lang := locale.FromContext(ctx)
			var a struct {
				LeadID    int64  `json:"lead_id"`
				Status    string `json:"status"`
				FindingID int64  `json:"finding_id"`
				Reason    string `json:"reason"`
			}
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf(locale.Text(lang, "Invalid arguments")), nil
			}
			lead, err := s.m.pg.GetScanLead(taskID, a.LeadID)
			if err != nil || lead == nil || lead.DeliveredAt == nil {
				return actool.Errorf(locale.Text(lang, "Unknown lead number for this task")), nil
			}
			reason := oneLine(scanReason(a.Reason), 500)
			switch a.Status {
			case db.LeadConfirmed:
			case db.LeadDismissed:
				if reason == "" {
					return actool.Errorf(locale.Text(lang, "A dismissal needs a reason")), nil
				}
			default:
				return actool.Errorf(locale.Text(lang, "status must be confirmed or dismissed")), nil
			}
			if a.FindingID > 0 {
				f, err := s.m.pg.GetFinding(a.FindingID)
				if err != nil || f == nil || f.TaskID == nil || *f.TaskID != taskID {
					return actool.Errorf(locale.Text(lang, "finding_id is not a finding of this task")), nil
				}
			}
			// A package hint stands for all of that package's delivered advisories. Ones a
			// finding already named are no longer delivered and keep their own outcome.
			targets := []*db.ScanLead{lead}
			if lead.Kind == scanner.KindAdvisory {
				if all, err := s.m.pg.ListScanLeadsByState(taskID, db.LeadDelivered); err == nil {
					for _, o := range all {
						if o.ID != lead.ID && o.Kind == scanner.KindAdvisory && packageKey(o) == packageKey(lead) {
							targets = append(targets, o)
						}
					}
				}
			}
			for _, o := range targets {
				if _, err := s.m.pg.ResolveScanLead(taskID, o.ID, a.Status, a.FindingID, reason, "agent"); err != nil {
					return actool.Errorf(locale.Text(lang, "Could not record the outcome")), nil
				}
			}
			return actool.Text(fmt.Sprintf("lead %d recorded as %s (%d lead(s) resolved)", lead.ID, a.Status, len(targets))), nil
		})
}

// --- report sections ---

// attachScanSections adds the scanner sections to a code-review report input.
// Advisories are listed, never filed as findings; a secret lead shows only its
// file, line and rule.
func (s *Server) attachScanSections(in *report.Input, t *Task) {
	if in.CodeReview == nil {
		return
	}
	id, err := strconv.ParseInt(t.ID, 10, 64)
	if err != nil {
		return
	}
	leads, err := s.m.pg.ListScanLeads(id)
	if err != nil {
		return
	}
	for _, l := range leads {
		review := scanReviewLabel(l)
		switch l.Kind {
		case scanner.KindSecret:
			in.SecretLeads = append(in.SecretLeads, report.SecretLead{File: l.File, Line: l.Line, Rule: l.RuleID, Review: review})
		case scanner.KindAdvisory:
			in.Advisories = append(in.Advisories, report.Advisory{Package: l.Package, Version: l.Version, ID: l.RuleID,
				Fixed: l.FixedVersion, File: l.File, Link: "https://osv.dev/vulnerability/" + l.RuleID, Review: review})
		}
	}
}

// scanReviewLabel says what became of a lead, in words that do not overstate it.
func scanReviewLabel(l *db.ScanLead) string {
	switch l.State {
	case db.LeadConfirmed:
		if l.FindingID != nil {
			return fmt.Sprintf("Covered by finding %d", *l.FindingID)
		}
		return "Confirmed by the reviewer"
	case db.LeadDismissed:
		return "Dismissed by the reviewer"
	case db.LeadDelivered:
		return "Sent for review, no outcome recorded"
	}
	return "Not reviewed"
}
