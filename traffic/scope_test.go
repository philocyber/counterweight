package traffic

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Philocyber/counterweight/locale"
)

type scopeCtxKey struct{}

func scopedCtx(task int64) context.Context {
	return context.WithValue(context.Background(), scopeCtxKey{}, task)
}

func scopeTestTraffic(t *testing.T) (tr *Traffic, ownID, foreignID, foreignHash string) {
	t.Helper()
	tr, _ = openTraffic(t)
	tr.record(newFlow("own.example.com", "GET", "/a", nil, []byte(`{"mine":true}`)))
	big := []byte(strings.Repeat("secret-row\n", maxInlineBody/10+2000))
	tr.record(newFlow("foreign.example.com", "GET", "/b", nil, big, withRespType("application/sql")))
	if err := tr.DB().QueryRow(`SELECT id FROM exchanges WHERE host='own.example.com'`).Scan(&ownID); err != nil {
		t.Fatal(err)
	}
	if err := tr.DB().QueryRow(`SELECT id FROM exchanges WHERE host='foreign.example.com'`).Scan(&foreignID); err != nil {
		t.Fatal(err)
	}
	if err := tr.DB().QueryRow(`SELECT resp_blob FROM exchange_bodies WHERE id=?`, foreignID).Scan(&foreignHash); err != nil || len(foreignHash) != 64 {
		t.Fatalf("foreign blob hash %q: %v", foreignHash, err)
	}
	return
}

func bindTestScope(tr *Traffic, resolveErr error) {
	tr.BindAgentScope(func(taskID int64) (func(string) bool, error) {
		if taskID == 99 {
			return nil, resolveErr
		}
		return func(host string) bool { return host == "own.example.com" }, nil
	}, func(ctx context.Context) ([]int64, bool, error) {
		v, ok := ctx.Value(scopeCtxKey{}).(int64)
		if !ok {
			return nil, false, nil
		}
		if v < 0 {
			return nil, true, nil
		}
		return []int64{v}, true, nil
	})
}

func callTool(t *testing.T, tr *Traffic, ctx context.Context, name string, in any) (string, bool) {
	t.Helper()
	raw, _ := json.Marshal(in)
	for _, tool := range tr.Tools(locale.En) {
		if tool.Name() == name {
			r, err := tool.Call(ctx, raw, nil)
			if err != nil {
				t.Fatal(err)
			}
			return r.Flatten(), r.IsError
		}
	}
	t.Fatalf("no tool %s", name)
	return "", false
}

func TestTrafficToolsConfineTaskBoundCallers(t *testing.T) {
	tr, ownID, foreignID, foreignHash := scopeTestTraffic(t)
	bindTestScope(tr, errors.New("scope unavailable"))
	ctx := scopedCtx(1)

	out, isErr := callTool(t, tr, ctx, "traffic_search", map[string]any{"host": "own.example.com"})
	if isErr || !strings.Contains(out, ownID) {
		t.Fatalf("own host search: %v %s", isErr, out)
	}
	out, _ = callTool(t, tr, ctx, "traffic_search", map[string]any{"host": "foreign.example.com"})
	if strings.Contains(out, foreignID) || !strings.Contains(out, "No matching traffic") {
		t.Fatalf("foreign host search leaked: %s", out)
	}
	out, _ = callTool(t, tr, ctx, "traffic_search", map[string]any{"host": "http://FOREIGN.example.com:80/x", "body_contains": "secret-row"})
	if strings.Contains(out, foreignID) {
		t.Fatalf("foreign host search with URL form leaked: %s", out)
	}

	if out, isErr = callTool(t, tr, ctx, "traffic_get", map[string]any{"id": ownID}); isErr || !strings.Contains(out, "mine") {
		t.Fatalf("own get: %v %s", isErr, out)
	}
	out, isErr = callTool(t, tr, ctx, "traffic_get", map[string]any{"id": foreignID})
	if !isErr || strings.Contains(out, "secret-row") {
		t.Fatalf("foreign get: %v %s", isErr, out)
	}
	out, isErr = callTool(t, tr, ctx, "traffic_blob", map[string]any{"hash": foreignHash})
	if !isErr || strings.Contains(out, "secret-row") {
		t.Fatalf("foreign blob: %v %s", isErr, out)
	}

	// An operator-level (unbound) caller keeps its reach.
	out, isErr = callTool(t, tr, context.Background(), "traffic_blob", map[string]any{"hash": foreignHash})
	if isErr || !strings.Contains(out, "secret-row") {
		t.Fatalf("unbound blob: %v %.80s", isErr, out)
	}
}

func TestTrafficToolsFailClosedWithoutIdentityOrScope(t *testing.T) {
	tr, _, foreignID, foreignHash := scopeTestTraffic(t)
	bindTestScope(tr, errors.New("scope unavailable"))
	for name, ctx := range map[string]context.Context{
		"bound without a task id": scopedCtx(-1),
		"scope lookup fails":      scopedCtx(99),
	} {
		for tool, in := range map[string]any{
			"traffic_search": map[string]any{"host": "own.example.com"},
			"traffic_get":    map[string]any{"id": foreignID},
			"traffic_blob":   map[string]any{"hash": foreignHash},
		} {
			out, isErr := callTool(t, tr, ctx, tool, in)
			if !isErr || strings.Contains(out, "secret-row") || strings.Contains(out, "own.example.com/") {
				t.Fatalf("%s / %s: error=%v %.80s", name, tool, isErr, out)
			}
		}
	}
}

func TestReadEvidenceRefusesWholeBatchOutsideScope(t *testing.T) {
	tr, ownID, foreignID, _ := scopeTestTraffic(t)
	bindTestScope(tr, nil)
	consumed := 0
	consume := func(EvidenceExchange) error { consumed++; return nil }

	if err := tr.ReadEvidence(scopedCtx(1), []string{ownID, foreignID}, consume); err == nil || consumed != 0 {
		t.Fatalf("mixed batch: err=%v consumed=%d", err, consumed)
	}
	if err := tr.ReadEvidence(scopedCtx(1), []string{ownID}, consume); err != nil || consumed != 1 {
		t.Fatalf("own exchange: err=%v consumed=%d", err, consumed)
	}
	// A context forced to a task is held to that task even without run identity.
	if err := tr.ReadEvidence(WithTaskScope(context.Background(), 1), []string{foreignID}, consume); err == nil {
		t.Fatal("forced task scope must refuse a foreign exchange")
	}
	if err := tr.ReadEvidence(WithTaskScope(context.Background(), 99), []string{ownID}, consume); err == nil {
		t.Fatal("forced task scope must fail closed when the scope is unavailable")
	}
	// Operator callers are unrestricted.
	if err := tr.ReadEvidence(context.Background(), []string{foreignID}, consume); err != nil {
		t.Fatal(err)
	}
}
