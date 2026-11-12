package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/Philocyber/counterweight/db"
)

func TestOperatorScopeApprovalAPI(t *testing.T) {
	if !strings.Contains(os.Getenv("COUNTERWEIGHT_PG_DSN"), "/counterweight_read_grant_server?") {
		t.Skip("requires explicitly selected disposable counterweight_read_grant_server database")
	}
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	s := New(ctx, m, dir, dir, dir)
	token, err := signTestSession(t, s)
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := m.CreateTask("operator scope API fixture", "test", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	taskID, _ := strconv.ParseInt(fixture.ID, 10, 64)
	t.Cleanup(func() { _ = m.pg.DeleteTask(taskID) })
	host := fmt.Sprintf("scope-api-%s.invalid", fixture.ID)
	id, err := m.assets.UpsertHTTPService(db.UpsertHTTPServiceReq{URL: "https://" + host, Auth: []map[string]any{{"token": "fixture stored auth"}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = m.assets.DeleteByIDs([]int64{id}) })
	proposal, err := m.assets.AddAgentScope(taskID, "subdomain", host, "agent proposal", "agent")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"kind": "subdomain", "value": host, "reason": "operator approves these reads"})
	h := s.Handler()
	call := func(method, path, credential string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
		if credential != "" {
			r.Header.Set("Authorization", "Bearer "+credential)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	path := "/api/tasks/" + fixture.ID + "/scope"
	if w := call("POST", path, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated scope approval: %d %s", w.Code, w.Body.String())
	}
	if got, err := m.assets.GetByIDsInScope(taskID, []int64{id}); err != nil || len(got) != 0 {
		t.Fatalf("proposal already granted access: %+v %v", got, err)
	}
	w := call("POST", path, token)
	var granted db.TaskScope
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &granted) != nil || granted.ID != proposal.ID || granted.Source != "manual" {
		t.Fatalf("operator did not approve the existing row: %d %s", w.Code, w.Body.String())
	}
	if got, err := m.assets.GetByIDsInScope(taskID, []int64{id}); err != nil || len(got) != 1 || len(got[0].Auth) != 1 {
		t.Fatalf("approved credential workflow failed: %+v %v", got, err)
	}
	if w := call("DELETE", path+"/"+fmt.Sprint(granted.ID), token); w.Code != http.StatusOK {
		t.Fatalf("revoke scope: %d %s", w.Code, w.Body.String())
	}
	if got, err := m.assets.GetByIDsInScope(taskID, []int64{id}); err != nil || len(got) != 0 {
		t.Fatalf("revoked grant still permits reads: %+v %v", got, err)
	}
}
