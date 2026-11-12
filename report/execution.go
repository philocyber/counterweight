package report

import "fmt"

// Execution states how far the task ran, so an export never presents an empty
// or partial findings list as a conclusion that the assets are safe.
type Execution struct {
	Kind     string // completed, partial, failed, in_progress, not_started, unknown
	Headline string
	Detail   string
}

// Final reports whether the run reached a terminal state that ended normally.
func (e Execution) Final() bool { return e.Kind == "completed" }

// DescribeExecution maps the resolved task status (done, failed, timeout,
// running, paused, queued, created) to report wording.
func DescribeExecution(status string, findings int) Execution {
	switch status {
	case "done":
		if findings == 0 {
			return Execution{"completed", "Completed with no registered findings",
				"The task reached its done state without registering a finding. That is a recorded result, not proof that the assets are safe; see Limitations."}
		}
		return Execution{"completed", "Completed with " + plural(findings, "registered finding", "registered findings"),
			"The task finished and registered " + plural(findings, "finding", "findings") + ". Review status is shown per finding."}
	case "timeout":
		return Execution{"partial", "Stopped at the task time limit",
			fmt.Sprintf("The task hit its wall-clock limit before finishing. %s preserved; unexplored work is not equivalent to a clean result.", preserved(findings))}
	case "failed":
		return Execution{"failed", "Task failed",
			fmt.Sprintf("The task stopped before completing. %s preserved. This is not a conclusion that no further issues exist.", preserved(findings))}
	case "running", "paused":
		return Execution{"in_progress", "Task in progress",
			"The task has not finished. Findings shown are provisional; an empty or short list is not a conclusion."}
	case "queued", "created", "":
		return Execution{"not_started", "Task not started",
			"The task has not started executing. Any records shown come from earlier or inherited context."}
	default:
		return Execution{"unknown", "Execution status unavailable",
			"Completion could not be verified. This report cannot establish that the task finished or that no issues were found."}
	}
}

// preserved reads "1 finding was" or "3 findings were".
func preserved(n int) string {
	if n == 1 {
		return "1 finding was"
	}
	return fmt.Sprintf("%d findings were", n)
}
