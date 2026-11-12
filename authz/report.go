package authz

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// ReportSchema identifies the JSON report format.
const ReportSchema = "counterweight-authz-report/1"

// Check and case results. A report can also end in ResultError.
const (
	Pass         = "pass"
	Fail         = "fail"
	Inconclusive = "inconclusive"
	ResultError  = "error"
)

// Report is the outcome of a run. It holds no credential: paths are the
// policy's own text and response excerpts are redacted and bounded.
type Report struct {
	Schema       string          `json:"schema"`
	PolicySHA256 string          `json:"policy_sha256"`
	Target       string          `json:"target,omitempty"`
	StartedAt    time.Time       `json:"started_at"`
	DurationMS   int64           `json:"duration_ms"`
	Result       string          `json:"result"` // pass | fail | inconclusive | error
	ExitCode     int             `json:"exit_code"`
	Counts       Counts          `json:"counts"`
	Errors       []string        `json:"errors,omitempty"`
	Personas     []PersonaResult `json:"personas,omitempty"`
	Cases        []CaseResult    `json:"cases,omitempty"`
}

// Counts are the checks that were actually sent, by result.
type Counts struct {
	Cases        int `json:"cases"`
	Checks       int `json:"checks"`
	Passed       int `json:"passed"`
	Failed       int `json:"failed"`
	Inconclusive int `json:"inconclusive"`
}

type PersonaResult struct {
	Name             string `json:"name"`
	Role             string `json:"role,omitempty"` // name shown for the persona, when it has one
	Authenticated    bool   `json:"authenticated"`
	IdentityVerified bool   `json:"identity_verified"`
}

type CaseResult struct {
	ID     string        `json:"id"`
	Method string        `json:"method"`
	Path   string        `json:"path"`
	Write  bool          `json:"write,omitempty"`
	Result string        `json:"result"`
	Checks []CheckResult `json:"checks"`
}

type CheckResult struct {
	Persona        string `json:"persona"`
	Expect         string `json:"expect"` // allow | deny
	ExpectedStatus []int  `json:"expected_status"`
	Status         int    `json:"status,omitempty"`
	Result         string `json:"result"`
	Kind           string `json:"kind,omitempty"`
	Detail         string `json:"detail,omitempty"`
	DurationMS     int64  `json:"duration_ms"`
	ResponseBytes  int    `json:"response_bytes"`
	Excerpt        string `json:"excerpt,omitempty"`
}

// sessionLost downgrades the results of a persona whose session stopped
// working during the run. Access that was wrongly granted stays a failure: a
// dead session cannot explain it.
func (r *Report) sessionLost(persona string) {
	for i := range r.Cases {
		for j := range r.Cases[i].Checks {
			ch := &r.Cases[i].Checks[j]
			if ch.Persona != persona || ch.Kind == "access_granted" || ch.Kind == "data_exposed" {
				continue
			}
			ch.Result, ch.Kind = Inconclusive, "session_lost"
			ch.Detail = "the persona's session stopped working during the run"
		}
	}
}

// finish computes case results, counts, the overall result and the exit code:
// 0 every check passed, 1 the policy is violated, 2 the run proves nothing
// (setup error, inconclusive check, or no check at all).
func (r *Report) finish(elapsed time.Duration) {
	r.DurationMS = elapsed.Milliseconds()
	r.Counts = Counts{Cases: len(r.Cases)}
	for i := range r.Cases {
		c := &r.Cases[i]
		c.Result = Pass
		for _, ch := range c.Checks {
			r.Counts.Checks++
			switch ch.Result {
			case Pass:
				r.Counts.Passed++
			case Fail:
				r.Counts.Failed++
				c.Result = Fail
			default:
				r.Counts.Inconclusive++
				if c.Result == Pass {
					c.Result = Inconclusive
				}
			}
		}
	}
	switch {
	case r.Counts.Failed > 0:
		r.Result, r.ExitCode = Fail, 1
	case len(r.Errors) > 0 || r.Counts.Checks == 0:
		r.Result, r.ExitCode = ResultError, 2
	case r.Counts.Inconclusive > 0:
		r.Result, r.ExitCode = Inconclusive, 2
	default:
		r.Result, r.ExitCode = Pass, 0
	}
}

// WriteText prints the report for a terminal or a CI log.
func (r *Report) WriteText(w io.Writer) {
	if r.Target != "" {
		fmt.Fprintf(w, "Authorization check against %s\n", r.Target)
	}
	// A persona is shown by its role name when it has one: "Admin (admin_a)".
	shown := map[string]string{}
	for _, p := range r.Personas {
		shown[p.Name] = p.Name
		if p.Role != "" && p.Role != p.Name {
			shown[p.Name] = p.Role + " (" + p.Name + ")"
		}
	}
	if len(r.Personas) > 0 {
		names := make([]string, len(r.Personas))
		for i, p := range r.Personas {
			names[i] = shown[p.Name]
			switch {
			case p.IdentityVerified:
				names[i] += " (identity verified)"
			case p.Authenticated:
				names[i] += " (NOT verified)"
			}
		}
		fmt.Fprintf(w, "Personas: %s\n", strings.Join(names, ", "))
	}
	if len(r.Cases) > 0 {
		fmt.Fprintln(w)
	}
	for _, c := range r.Cases {
		fmt.Fprintf(w, "%-12s  %s  (%s %s)\n", strings.ToUpper(c.Result), c.ID, c.Method, c.Path)
		for _, ch := range c.Checks {
			if ch.Result != Pass {
				fmt.Fprintf(w, "    %s as %s: %s [%s]\n", strings.ToUpper(ch.Result), shown[ch.Persona], ch.Detail, ch.Kind)
			}
		}
	}
	if len(r.Errors) > 0 {
		fmt.Fprintln(w)
	}
	for _, e := range r.Errors {
		fmt.Fprintf(w, "ERROR: %s\n", e)
	}
	fmt.Fprintf(w, "\nResult: %s. %d cases, %d checks sent: %d passed, %d failed, %d inconclusive.\n",
		strings.ToUpper(r.Result), r.Counts.Cases, r.Counts.Checks, r.Counts.Passed, r.Counts.Failed, r.Counts.Inconclusive)
	switch r.Result {
	case ResultError:
		fmt.Fprintln(w, "The run could not be completed, so it proves nothing about the target.")
	case Inconclusive:
		fmt.Fprintln(w, "Some checks prove nothing either way. This is not a pass.")
	}
}
