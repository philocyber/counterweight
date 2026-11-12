package report

import "time"

// FindingLifecycle is what happened to a code-review finding after it was
// reported: its independent verification, the fix proposed for it, the pull
// request and the re-check of the merged code. The server fills it from the
// finding's runs; the zero value means nothing happened yet.
type FindingLifecycle struct {
	// Verification is the newest completed verification verdict: confirmed,
	// rejected (false positive), needs_validation, or "" when never verified.
	Verification string `json:"verification"`
	// Fix is the state of the attempt shown (the published one, else the newest),
	// as the Mitigation tab names it, or "" when no fix was attempted.
	Fix     string `json:"fix"`
	PRURL   string `json:"pr_url,omitempty"`
	PRState string `json:"pr_state,omitempty"` // open, merged or closed
	// Recheck is the newest completed post-merge re-check verdict: rejected
	// (gone in the merged code), confirmed (still present), needs_validation.
	Recheck string `json:"recheck"`

	// When each step finished; nil when it did not happen or was not recorded.
	VerifiedAt  *time.Time `json:"verified_at,omitempty"`
	PROpenedAt  *time.Time `json:"pr_opened_at,omitempty"`
	RecheckedAt *time.Time `json:"rechecked_at,omitempty"`
}

// Lifecycle stages, from least to most advanced. One finding has exactly one.
const (
	StageUnverified      = "unverified"
	StageNeedsValidation = "needs_validation"
	StageFalsePositive   = "false_positive"
	StageConfirmed       = "confirmed"
	StageFixProposed     = "fix_proposed"
	StagePROpen          = "pr_open"
	StageMerged          = "merged"
	StageStillPresent    = "still_present"
	StageValidated       = "validated"
)

// lifecycleStages orders the summary rows.
var lifecycleStages = []string{StageValidated, StageStillPresent, StageMerged, StagePROpen, StageFixProposed,
	StageConfirmed, StageFalsePositive, StageNeedsValidation, StageUnverified}

var stageLabels = map[string]string{
	StageValidated:       "Fixed and validated after merge",
	StageStillPresent:    "Still present after merge",
	StageMerged:          "Fix merged, not re-checked",
	StagePROpen:          "Fix proposed in an open pull request",
	StageFixProposed:     "Fix written, not published",
	StageConfirmed:       "Confirmed by independent verification",
	StageFalsePositive:   "False positive (rejected by verification)",
	StageNeedsValidation: "Needs human validation",
	StageUnverified:      "Not independently verified",
}

// Stage is the single furthest point the finding reached.
func (l FindingLifecycle) Stage() string {
	switch {
	case l.PRState == "merged" && l.Recheck == "rejected":
		return StageValidated
	case l.PRState == "merged" && l.Recheck == "confirmed":
		return StageStillPresent
	case l.PRState == "merged":
		return StageMerged
	case l.PRState == "open":
		return StagePROpen
	case l.Fix == "ready":
		return StageFixProposed
	case l.Verification == "rejected":
		return StageFalsePositive
	case l.Verification == "confirmed":
		return StageConfirmed
	case l.Verification == "needs_validation":
		return StageNeedsValidation
	}
	return StageUnverified
}

func stageLabel(stage string) string { return nz(stageLabels[stage], stage) }

// verificationLabel, fixLabel and recheckLabel are the cells of the lifecycle table.
func verificationLabel(v string) string {
	switch v {
	case "confirmed":
		return "Confirmed"
	case "rejected":
		return "False positive"
	case "needs_validation":
		return "Needs validation"
	}
	return "Not verified"
}

func fixLabel(l FindingLifecycle) string {
	switch l.PRState {
	case "merged":
		return "PR merged"
	case "open":
		return "PR open"
	case "closed":
		return "PR closed unmerged"
	}
	switch l.Fix {
	case "":
		return "No fix"
	case "ready":
		return "Fix ready, not published"
	case "drafting", "in_review":
		return "Fix in progress"
	}
	return "Fix not usable (" + l.Fix + ")"
}

func recheckLabel(l FindingLifecycle) string {
	if l.PRState != "merged" {
		return "-"
	}
	switch l.Recheck {
	case "rejected":
		return "Fixed (re-check passed)"
	case "confirmed":
		return "Still present"
	case "needs_validation":
		return "Inconclusive"
	}
	return "Not re-checked"
}

const (
	lifecycleIntro = "What happened to each finding after it was reported: an independent verification that tried to refute it, the fix proposed as a pull request, and a re-check of the code after the merge."
	lifecycleNote  = "Verification and the post-merge re-check read the source; neither runs the application or its tests. \"Fixed (re-check passed)\" means an independent verifier re-read the merged code and found the reported path closed."
)

// lifecycleCounts tallies stages in display order, skipping empty ones.
func lifecycleCounts(in Input) []Count {
	n := map[string]int{}
	for _, f := range reportFindings(in) {
		n[in.Lifecycle[f.ID].Stage()]++
	}
	out := []Count{}
	for _, s := range lifecycleStages {
		if n[s] > 0 {
			out = append(out, Count{s, n[s]})
		}
	}
	return out
}
