package report

import (
	"strings"
	"time"

	"github.com/Philocyber/counterweight/db"
)

// View is the task report as structured data for the web console. It carries
// the same facts as the Markdown and PDF renderers, already computed, so the UI
// only lays them out. Text is not redacted: the console shows stored records.
type View struct {
	ExecutionIsolation string      `json:"execution_isolation"`
	AssessmentType     string      `json:"assessment_type"`
	Kind               string      `json:"kind"` // "code_review" or "pentest"
	Title              string      `json:"title"`
	Goal               string      `json:"goal"`      // The task instructions as stored.
	Objective          string      `json:"objective"` // What the task set out to do, for readers.
	TaskID             string      `json:"task_id"`
	Status             string      `json:"status"`
	StatusLabel        string      `json:"status_label"`
	DocumentID         string      `json:"document_id"`
	Fingerprint        string      `json:"fingerprint"` // SHA-256 of the reported findings.
	Version            string      `json:"version"`
	Models             []string    `json:"models"`
	GeneratedAt        time.Time   `json:"generated_at"`
	CreatedAt          *time.Time  `json:"created_at,omitempty"`
	FirstRunAt         *time.Time  `json:"first_run_at,omitempty"`
	CompletedAt        *time.Time  `json:"completed_at,omitempty"`
	Execution          ExecView    `json:"execution"`
	Draft              bool        `json:"draft"`
	Severity           SeverityMix `json:"severity"`
	Review             []Count     `json:"review"`
	OpenReviews        int         `json:"open_reviews"`
	Posture            string      `json:"posture"`
	KeyActions         []KeyAction `json:"key_actions"`

	CodeReview *CodeReviewView `json:"code_review,omitempty"`
	CitedFiles []CitedFile     `json:"cited_files"`
	Scope      []ScopeView     `json:"scope"`
	Assets     []Count         `json:"assets"`
	Coverage   *CoverageView   `json:"coverage,omitempty"`

	// Lifecycle counts findings per stage (code reviews only; empty otherwise).
	Lifecycle []Count `json:"lifecycle"`

	// Supplementary scanner leads, separate from the findings; empty when none.
	Advisories  []Advisory   `json:"advisories"`
	SecretLeads []SecretLead `json:"secret_leads"`

	Findings    []FindingView `json:"findings"`
	Conclusions []RecordView  `json:"conclusions"`
	Attempts    []RecordView  `json:"attempts"`
	Limitations []string      `json:"limitations"`
	Truncated   bool          `json:"truncated"`
}

type ExecView struct {
	Kind     string `json:"kind"`
	Headline string `json:"headline"`
	Detail   string `json:"detail"`
}

type SeverityMix struct {
	Critical int `json:"critical"`
	High     int `json:"high"`
	Medium   int `json:"medium"`
	Low      int `json:"low"`
	Info     int `json:"info"`
}

type Count struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

type CodeReviewView struct {
	Repository   string `json:"repository"`
	URL          string `json:"url"`
	Ref          string `json:"ref"`
	Commit       string `json:"commit"`
	Mode         string `json:"mode"`
	Inferred     bool   `json:"inferred"`
	TargetSource string `json:"target_source"`
	ModeLabel    string `json:"mode_label"`
	// BlobBase prefixes "<file>#L<a>-L<b>" to link cited lines at the reviewed
	// commit; empty when the repository is not on GitHub or the commit is unknown.
	BlobBase string `json:"blob_base,omitempty"`
}

type CitedFile struct {
	File     string   `json:"file"`
	Lines    []string `json:"lines"`
	Findings int      `json:"findings"`
}

type ScopeView struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
	Source string `json:"source"`
}

type CoverageView struct {
	Tested int `json:"tested"`
	Total  int `json:"total"`
}

type FindingView struct {
	ID            int64          `json:"id"`
	Title         string         `json:"title"`
	Class         string         `json:"class"`
	ClassInfo     ClassInfo      `json:"class_info"`
	Severity      string         `json:"severity"`
	Status        string         `json:"status"`
	StatusLabel   string         `json:"status_label"`
	NextAction    string         `json:"next_action,omitempty"` // Empty once the finding is closed.
	CreatedAt     time.Time      `json:"created_at"`
	Summary       string         `json:"summary"`
	Evidence      string         `json:"evidence"`
	Trace         EvidenceTrace  `json:"trace"`
	Report        string         `json:"report"`
	ReportStale   bool           `json:"report_stale"`
	CodeLocations []CodeLocation `json:"code_locations"`
	Traffic       []TrafficView  `json:"traffic"`
	// Lifecycle and Stage are set only when the report tracks the finding lifecycle.
	Lifecycle *FindingLifecycle `json:"lifecycle,omitempty"`
	Stage     string            `json:"stage,omitempty"`
}

// CodeLocation is a file and the line ranges a finding cites.
type CodeLocation = codeLocation

type TrafficView struct {
	ID     int64  `json:"id"`
	Role   string `json:"role"`
	Method string `json:"method"`
	URL    string `json:"url"`
	Status int    `json:"status"`
	Note   string `json:"note"`
}

type RecordView struct {
	ID        int64     `json:"id"`
	State     string    `json:"state"`
	Origin    string    `json:"origin"`
	CreatedAt time.Time `json:"created_at"`
	Summary   string    `json:"summary"`
	Issue     string    `json:"issue,omitempty"`
	Removal   string    `json:"removal,omitempty"`
}

func optTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// BuildView assembles the console report view from the same input as Markdown and PDF.
func BuildView(in Input) View {
	fs := reportFindings(in)
	exec := DescribeExecution(in.Status, len(fs))
	w := &pdfWriter{findings: fs} // reuse the shared counting helpers
	v := View{
		ExecutionIsolation: in.ExecutionIsolation().Description(),
		AssessmentType:     nz(in.AssessmentType, "general"),
		Kind:               "pentest", Title: nz(in.Title, "Untitled task"), Goal: in.Goal, Objective: in.Objective(), TaskID: in.TaskID,
		Status: nz(in.Status, "unknown"), StatusLabel: taskStatusLabel(in.Status), DocumentID: in.DocumentID(), Fingerprint: in.ContentFingerprint(),
		Posture: posture(in), KeyActions: keyActions(in, 3),
		Version: nz(in.Version, "dev"), Models: append([]string{}, in.Models...), GeneratedAt: in.GeneratedAt,
		CreatedAt: optTime(in.CreatedAt), FirstRunAt: optTime(in.FirstRunAt), CompletedAt: optTime(in.CompletedAt),
		Execution:   ExecView{exec.Kind, exec.Headline, exec.Detail},
		OpenReviews: w.openReviews(), Truncated: in.Truncated,
		CitedFiles: []CitedFile{}, Scope: []ScopeView{}, Assets: []Count{}, Findings: []FindingView{}, Lifecycle: []Count{},
		Conclusions: []RecordView{}, Attempts: []RecordView{},
		Advisories: append([]Advisory{}, in.Advisories...), SecretLeads: append([]SecretLead{}, in.SecretLeads...),
	}
	v.Draft = !exec.Final() || v.OpenReviews > 0
	sev := w.severityCounts()
	v.Severity = SeverityMix{sev["critical"], sev["high"], sev["medium"], sev["low"], sev["info"]}

	status := map[string]int{}
	for _, f := range fs {
		status[nz(f.Status, db.FindingPending)]++
	}
	for _, s := range reviewStatusOrder {
		if status[s] > 0 {
			v.Review = append(v.Review, Count{s, status[s]})
			delete(status, s)
		}
	}
	for _, s := range sortedKeys(status) {
		v.Review = append(v.Review, Count{s, status[s]})
	}
	if v.Review == nil {
		v.Review = []Count{}
	}

	if cr := in.CodeReview; cr != nil {
		v.Kind = "code_review"
		v.CodeReview = &CodeReviewView{cr.Repository, cr.URL, cr.Ref, cr.Commit, cr.Mode, cr.Inferred, cr.targetSource(),
			reviewModeLabel(cr.Mode), cr.blobBase()}
		v.CitedFiles = citedFiles(fs)
		v.Limitations = append(v.Limitations, codeReviewNotes...)
		if in.Lifecycle != nil {
			v.Lifecycle = lifecycleCounts(in)
		}
	} else {
		for _, s := range in.Scope {
			v.Scope = append(v.Scope, ScopeView{s.Kind, nz(s.Domain, nz(s.Net, nz(s.CompanyName, nz(s.Value, s.Kind)))), s.Source})
		}
		for _, k := range sortedKeys(in.AssetCounts) {
			v.Assets = append(v.Assets, Count{k, in.AssetCounts[k]})
		}
		if c := in.Coverage; c != nil {
			v.Coverage = &CoverageView{c.Tested, c.Denominator}
		}
		v.Limitations = append(v.Limitations, limitationNotes...)
	}
	if in.Truncated {
		v.Limitations = append(v.Limitations, "Record limit reached: each section includes at most 5,000 newest records. Older records remain in the application; this export is incomplete.")
	}

	for _, f := range fs {
		fv := FindingView{
			ID: f.ID, Title: findingTitle(f), Class: f.VulnClass, ClassInfo: classInfo(f.VulnClass), Severity: normSeverity(f.Severity),
			Status: nz(f.Status, db.FindingPending), StatusLabel: findingStatusLabel(f.Status),
			CreatedAt: f.CreatedAt, Summary: strings.TrimSpace(f.Summary), Evidence: strings.TrimSpace(f.Evidence), Report: strings.TrimSpace(f.Report),
			Trace:         parseEvidence(f.Evidence),
			ReportStale:   f.Report != "" && f.EvidenceVersion != f.ReportEvidenceVersion,
			CodeLocations: codeLocations(f), Traffic: []TrafficView{},
		}
		if fv.CodeLocations == nil {
			fv.CodeLocations = []CodeLocation{}
		}
		var lp *FindingLifecycle
		if in.Lifecycle != nil {
			l := in.Lifecycle[f.ID]
			fv.Lifecycle, fv.Stage = &l, l.Stage()
			lp = &l
		}
		if !closed(f, lp) {
			fv.NextAction = nextAction(f, lp)
		}
		for _, b := range f.TrafficBindings {
			fv.Traffic = append(fv.Traffic, TrafficView{b.ID, b.Role, b.Snapshot.Method, b.Snapshot.URL, b.Snapshot.Status, b.Note})
		}
		v.Findings = append(v.Findings, fv)
	}
	for _, n := range in.Facts {
		if n.State != db.StateOrigin {
			v.Conclusions = append(v.Conclusions, recordView(n))
		}
	}
	for _, n := range in.Intents {
		v.Attempts = append(v.Attempts, recordView(n))
	}
	return v
}

func recordView(n *db.Node) RecordView {
	return RecordView{ID: n.ID, State: n.State, Origin: nz(n.Origin, "unspecified"), CreatedAt: n.CreatedAt,
		Summary: recordSummary(n), Issue: n.BlockedReason, Removal: n.DeleteReason}
}
