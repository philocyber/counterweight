package server

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/report"
)

// writeSARIF sends a SARIF log as a download.
func writeSARIF(w http.ResponseWriter, filename string, raw []byte) {
	w.Header().Set("Content-Type", report.SARIFMediaType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	_, _ = w.Write(raw)
}

// findingAffected labels the assets each finding is anchored to, for results
// that have no source location.
func (s *Server) findingAffected(fs []*db.DBFinding) map[int64][]string {
	assets := s.resolveFindingAssets(fs)
	out := map[int64][]string{}
	for _, f := range fs {
		for _, id := range f.AssetIDs {
			if a := assets[id]; a != nil {
				out[f.ID] = append(out[f.ID], assetLabel(a))
			}
		}
	}
	return out
}

// findingsSARIF renders findings from any mix of tasks: each is judged by the
// code-review metadata of its own task.
func (s *Server) findingsSARIF(ctx context.Context, fs []*db.DBFinding, now time.Time) ([]byte, error) {
	reviews := map[int64]*report.CodeReview{}
	for _, f := range fs {
		if f.TaskID == nil {
			continue
		}
		if _, done := reviews[*f.TaskID]; !done {
			var cr *report.CodeReview
			if t := s.m.ResolveTask(i64s(*f.TaskID)); t != nil {
				cr = s.taskCodeReview(t)
			}
			reviews[*f.TaskID] = cr
		}
	}
	reviewOf := func(f *db.DBFinding) *report.CodeReview {
		if f.TaskID == nil {
			return nil
		}
		return reviews[*f.TaskID]
	}
	// Launched reviews track verification and fixes; inferred ones do not.
	var tracked []*db.DBFinding
	for _, f := range fs {
		if cr := reviewOf(f); cr != nil && !cr.Inferred {
			tracked = append(tracked, f)
		}
	}
	lifecycle, err := s.findingLifecycles(ctx, tracked)
	if err != nil {
		return nil, err
	}
	return report.SARIF(report.SARIFInput{
		Findings: fs, GeneratedAt: now, Version: BuildVersion,
		CodeReview: reviewOf, Lifecycle: lifecycle, Affected: s.findingAffected(fs),
	})
}
