package server

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSARIFExportRoutes(t *testing.T) {
	s, f, request := trafficEvidenceServer(t)
	findings := fmt.Sprintf("/api/exploration/findings/export?format=sarif&scope=selected&ids=%d", f.FindingID)
	rec, err := s.m.pg.GetFinding(f.FindingID)
	if err != nil || rec == nil || rec.TaskID == nil {
		t.Fatalf("finding lookup: %v", err)
	}
	task := fmt.Sprintf("/api/report?task=%d&format=sarif", *rec.TaskID)

	for _, path := range []string{findings, task} {
		// An unauthenticated request must be refused.
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 401 {
			t.Errorf("%s without a session: status %d, want 401", path, w.Code)
		}

		w = request("GET", path, "")
		if w.Code != 200 {
			t.Fatalf("%s: status %d: %s", path, w.Code, w.Body.String())
		}
		if ct := w.Header().Get("Content-Type"); ct != "application/sarif+json" {
			t.Errorf("%s: content type %q", path, ct)
		}
		if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") || !strings.HasSuffix(cd, `.sarif"`) {
			t.Errorf("%s: content disposition %q", path, cd)
		}
		var log struct {
			Version string `json:"version"`
			Runs    []struct {
				Results []json.RawMessage `json:"results"`
			} `json:"runs"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &log); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if log.Version != "2.1.0" || len(log.Runs) != 1 || len(log.Runs[0].Results) != 1 {
			t.Errorf("%s: unexpected log: %s", path, w.Body.String())
		}
	}
}
