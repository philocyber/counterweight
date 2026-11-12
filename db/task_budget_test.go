package db

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"testing"
	"time"
)

func f64(v float64) *float64 { return &v }
func i64(v int64) *int64     { return &v }

// openBudgetTestDB opens the test database with the usage ledger and creates a
// task that is deleted, with its ledger rows, when the test ends.
func openBudgetTestDB(t *testing.T, opts TaskCreateOptions) (*DB, *Task) {
	t.Helper()
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) - skipping", err)
	}
	t.Cleanup(func() { d.Close() })
	if err := d.EnsureLLMUsageTable(); err != nil {
		t.Fatal(err)
	}
	task, err := d.CreateTaskWithOptions("budget test", "budget goal", opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = d.Exec(`DELETE FROM llm_usage WHERE task_id=$1`, fmt.Sprint(task.ID))
		_ = d.DeleteTask(task.ID)
	})
	return d, task
}

func insertUsage(t *testing.T, d *DB, task *Task, in, out, cacheRead int, cost *float64, currency string) {
	t.Helper()
	if err := d.InsertLLMUsage(&LLMUsage{
		TaskID: fmt.Sprint(task.ID), ExplorationID: task.ExplorationID, Worker: "worker", Model: "m", ProfileName: "p",
		InputTokens: in, OutputTokens: out, CacheRead: cacheRead, Status: "ok", Cost: cost, Currency: currency,
	}); err != nil {
		t.Fatal(err)
	}
}

func budgetView(t *testing.T, d *DB, task *Task) TaskBudgetView {
	t.Helper()
	v, err := d.TaskBudgetView(task.ID)
	if err != nil || v == nil {
		t.Fatalf("budget view = %v, %v", v, err)
	}
	return *v
}

// Cached tokens are a subset of input_tokens, so usage is input + output only.
func TestBudgetUsageDoesNotDoubleCountCachedTokens(t *testing.T) {
	d, task := openBudgetTestDB(t, TaskCreateOptions{})
	insertUsage(t, d, task, 1000, 200, 900, nil, "")
	insertUsage(t, d, task, 500, 50, 0, nil, "")
	v := budgetView(t, d, task)
	if v.Tokens != 1750 {
		t.Fatalf("tokens = %d, want 1750 (input 1500 + output 250, cache not added)", v.Tokens)
	}
	if v.Calls != 2 || v.MaxCallTokens != 1200 {
		t.Fatalf("calls=%d max=%d", v.Calls, v.MaxCallTokens)
	}
	if v.Limited() || v.State() != "ok" || v.Percent() != 0 {
		t.Fatalf("task without budget reported %+v", v)
	}
}

func TestModelPricingCallCost(t *testing.T) {
	full := ModelPricing{Input: f64(3), CachedInput: f64(0.3), Output: f64(15)}
	// 1000 input of which 800 cached, 100 output: 200*3 + 800*0.3 + 100*15 per million.
	cost, ok := full.CallCost(1000, 100, 800)
	if want := (200*3 + 800*0.3 + 100*15) / 1e6; !ok || math.Abs(cost-want) > 1e-12 {
		t.Fatalf("cost = %v ok=%v, want %v", cost, ok, want)
	}
	noCached := ModelPricing{Input: f64(3), Output: f64(15)}
	if cost, ok = noCached.CallCost(1000, 100, 800); !ok || math.Abs(cost-(1000*3+100*15)/1e6) > 1e-12 {
		t.Fatalf("cached tokens without a cached price must bill as input, got %v", cost)
	}
	free := ModelPricing{Input: f64(0), Output: f64(0)}
	if cost, ok = free.CallCost(5000, 5000, 0); !ok || cost != 0 {
		t.Fatalf("explicit zero prices are a known zero cost, got %v ok=%v", cost, ok)
	}
	for _, p := range []ModelPricing{{}, {Input: f64(1)}, {Output: f64(1)}} {
		if _, ok := p.CallCost(10, 10, 0); ok {
			t.Fatalf("%+v must not produce a cost", p)
		}
	}
}

// Cost comes from the price stored with each call; unpriced calls are unknown,
// and a later price edit does not rewrite what was already recorded.
func TestBudgetCostEstimationAndPriceChange(t *testing.T) {
	d, task := openBudgetTestDB(t, TaskCreateOptions{CostBudget: f64(1), CostCurrency: "usd"})
	var profileID int64
	if err := d.QueryRow(`INSERT INTO llm_profiles(name,format,model) VALUES($1,'openai','price-model') RETURNING id`, fmt.Sprintf("budget-price-%d", task.ID)).Scan(&profileID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM llm_profiles WHERE id=$1`, profileID) })

	// The unpriced profile makes the first call's cost unknown.
	p, err := d.ProfilePricing(profileID)
	if err != nil || p.Priced() {
		t.Fatalf("new profile pricing = %+v, %v", p, err)
	}
	insertUsage(t, d, task, 1000, 1000, 0, nil, "")
	v := budgetView(t, d, task)
	if v.Cost != 0 || v.UnknownCostCalls != 1 {
		t.Fatalf("unpriced call: cost=%v unknown=%d, want 0 and 1", v.Cost, v.UnknownCostCalls)
	}
	if v.CostBudget == nil || *v.CostBudget != 1 || v.CostCurrency != "USD" {
		t.Fatalf("cost budget = %v %q", v.CostBudget, v.CostCurrency)
	}

	price := func(in, out float64) {
		t.Helper()
		if _, err := d.SaveProfile(&LLMProfile{ID: profileID, Name: fmt.Sprintf("budget-price-%d", task.ID), Format: "openai", Model: "price-model", Streaming: true,
			PricingSet: true, PriceInput: f64(in), PriceOutput: f64(out)}); err != nil {
			t.Fatal(err)
		}
		pr, err := d.ProfilePricing(profileID)
		if err != nil || !pr.Priced() || *pr.Input != in || pr.Currency != "USD" {
			t.Fatalf("stored pricing = %+v, %v", pr, err)
		}
	}
	record := func(in, out int) {
		t.Helper()
		pr, _ := d.ProfilePricing(profileID)
		cost, ok := pr.CallCost(in, out, 0)
		if !ok {
			t.Fatal("profile should be priced")
		}
		insertUsage(t, d, task, in, out, 0, &cost, pr.Currency)
	}
	price(1000, 2000)
	record(1000, 1000)
	first := budgetView(t, d, task).Cost
	if want := (1000*1000.0 + 1000*2000.0) / 1e6; math.Abs(first-want) > 1e-9 {
		t.Fatalf("cost after first priced call = %v, want %v", first, want)
	}
	price(5000, 10000) // later price edit
	if after := budgetView(t, d, task).Cost; math.Abs(after-first) > 1e-9 {
		t.Fatalf("a price edit rewrote history: %v -> %v", first, after)
	}
	record(1000, 1000)
	v = budgetView(t, d, task)
	want := first + (1000*5000.0+1000*10000.0)/1e6
	if math.Abs(v.Cost-want) > 1e-9 || v.UnknownCostCalls != 1 {
		t.Fatalf("cost=%v unknown=%d, want %v and 1", v.Cost, v.UnknownCostCalls, want)
	}
	// A call priced in another currency cannot be added to a USD budget: unknown.
	other := 100.0
	insertUsage(t, d, task, 10, 10, 0, &other, "EUR")
	if v = budgetView(t, d, task); math.Abs(v.Cost-want) > 1e-9 || v.UnknownCostCalls != 2 {
		t.Fatalf("foreign currency: cost=%v unknown=%d", v.Cost, v.UnknownCostCalls)
	}
	// A failed call with no tokens never counts as unknown.
	insertUsage(t, d, task, 0, 0, 0, nil, "")
	if v = budgetView(t, d, task); v.UnknownCostCalls != 2 {
		t.Fatalf("zero-token call counted as unknown: %d", v.UnknownCostCalls)
	}
}

func TestProfilePricingSavedOnlyWhenSet(t *testing.T) {
	d, task := openBudgetTestDB(t, TaskCreateOptions{})
	name := fmt.Sprintf("budget-profile-%d", task.ID)
	id, err := d.SaveProfile(&LLMProfile{Name: name, Format: "openai", Model: "m", Streaming: true,
		PricingSet: true, PriceInput: f64(2), PriceCachedInput: f64(0.5), PriceOutput: f64(8), PriceCurrency: "eur"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM llm_profiles WHERE id=$1`, id) })
	p, err := d.ProfileByID(id)
	if err != nil || p == nil || p.PriceInput == nil || *p.PriceInput != 2 || *p.PriceCachedInput != 0.5 || *p.PriceOutput != 8 || p.PriceCurrency != "EUR" {
		t.Fatalf("saved profile = %+v, %v", p, err)
	}
	// An update that does not mention prices leaves them alone.
	if _, err = d.SaveProfile(&LLMProfile{ID: id, Name: name, Format: "openai", Model: "m2", Streaming: true}); err != nil {
		t.Fatal(err)
	}
	if p, _ = d.ProfileByID(id); p.Model != "m2" || p.PriceInput == nil || *p.PriceInput != 2 {
		t.Fatalf("prices lost on an update without them: %+v", p)
	}
	// Setting them with nil values clears them; invalid values are rejected.
	if _, err = d.SaveProfile(&LLMProfile{ID: id, Name: name, Format: "openai", Model: "m2", Streaming: true, PricingSet: true}); err != nil {
		t.Fatal(err)
	}
	if p, _ = d.ProfileByID(id); p.PriceInput != nil || p.PriceOutput != nil || p.PriceCurrency != "USD" || p.Pricing().Priced() {
		t.Fatalf("prices not cleared: %+v", p)
	}
	for _, bad := range []*LLMProfile{
		{ID: id, Name: name, Format: "openai", Model: "m", PricingSet: true, PriceInput: f64(-1)},
		{ID: id, Name: name, Format: "openai", Model: "m", PricingSet: true, PriceOutput: f64(math.NaN())},
		{ID: id, Name: name, Format: "openai", Model: "m", PricingSet: true, PriceCurrency: "dollars"},
	} {
		if _, err = d.SaveProfile(bad); err == nil {
			t.Fatalf("invalid pricing accepted: %+v", bad)
		}
	}
}

func TestBudgetViewStateAndReservation(t *testing.T) {
	v := TaskBudgetView{TokenBudget: 1000, Tokens: 500}
	if v.State() != "ok" || v.Percent() != 50 || v.ReachedKind() != "" {
		t.Fatalf("50%%: %s %v", v.State(), v.Percent())
	}
	v.Tokens = 800
	if v.State() != "warning" || v.Percent() != 80 {
		t.Fatalf("80%%: %s %v", v.State(), v.Percent())
	}
	v.Tokens = 1000
	if v.State() != "reached" || v.ReachedKind() != "tokens" {
		t.Fatalf("100%%: %s", v.State())
	}
	c := TaskBudgetView{CostBudget: f64(2), Cost: 2.5, CostCurrency: "USD"}
	if c.State() != "reached" || c.ReachedKind() != "cost" || c.Percent() != 125 {
		t.Fatalf("cost: %s %s %v", c.State(), c.ReachedKind(), c.Percent())
	}
	// With nothing in flight a call always fits; beside others each must fit its estimate.
	small := TaskBudgetView{TokenBudget: 100, Tokens: 0}
	if !small.Fits(0) || small.Fits(1) {
		t.Fatalf("budget for less than one call: Fits(0)=%v Fits(1)=%v", small.Fits(0), small.Fits(1))
	}
	roomy := TaskBudgetView{TokenBudget: 100000, Tokens: 1000, MaxCallTokens: 10000}
	if !roomy.Fits(1) || !roomy.Fits(8) || roomy.Fits(9) {
		t.Fatalf("reservation: Fits(1)=%v Fits(8)=%v Fits(9)=%v", roomy.Fits(1), roomy.Fits(8), roomy.Fits(9))
	}
}

func TestTaskBudgetCreateDefaultUpdateAndWarning(t *testing.T) {
	d, plain := openBudgetTestDB(t, TaskCreateOptions{})
	if v := budgetView(t, d, plain); v.TokenBudget != 0 || v.CostBudget != nil || v.CostCurrency != "USD" {
		t.Fatalf("default task has a budget: %+v", v)
	}
	prev := d.DefaultTaskTokenBudget()
	t.Cleanup(func() { _ = d.SetDefaultTaskTokenBudget(prev) })
	if err := d.SetDefaultTaskTokenBudget(5000); err != nil {
		t.Fatal(err)
	}
	if got := d.DefaultTaskTokenBudget(); got != 5000 {
		t.Fatalf("default = %d", got)
	}
	inherited, err := d.CreateTaskWithOptions("inherits", "g", TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := d.CreateTaskWithOptions("explicit", "g", TaskCreateOptions{TokenBudget: i64(0), CostBudget: f64(3), CostCurrency: "eur"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(inherited.ID); _ = d.DeleteTask(explicit.ID) })
	if v := budgetView(t, d, inherited); v.TokenBudget != 5000 {
		t.Fatalf("new task did not take the default: %+v", v)
	}
	if v := budgetView(t, d, explicit); v.TokenBudget != 0 || v.CostBudget == nil || *v.CostBudget != 3 || v.CostCurrency != "EUR" {
		t.Fatalf("explicit budget wrong: %+v", v)
	}
	if _, err = d.CreateTaskWithOptions("bad", "g", TaskCreateOptions{TokenBudget: i64(-1)}); err == nil {
		t.Fatal("negative budget accepted")
	}
	if err = d.SetDefaultTaskTokenBudget(-1); err == nil {
		t.Fatal("negative default accepted")
	}
	if err = d.SetDefaultTaskTokenBudget(0); err != nil || d.DefaultTaskTokenBudget() != 0 {
		t.Fatalf("clearing the default: %v", err)
	}

	// Update: omitted fields stay, zero removes a limit, an edit re-arms the warning.
	if err = d.UpdateTaskBudget(plain.ID, i64(2000), f64(1.5), nil); err != nil {
		t.Fatal(err)
	}
	if v := budgetView(t, d, plain); v.TokenBudget != 2000 || *v.CostBudget != 1.5 {
		t.Fatalf("update: %+v", v)
	}
	first, err := d.MarkBudgetWarned(plain.ID)
	if err != nil || !first {
		t.Fatalf("first warning mark = %v, %v", first, err)
	}
	if again, _ := d.MarkBudgetWarned(plain.ID); again {
		t.Fatal("warning marked twice")
	}
	eur := "eur"
	if err = d.UpdateTaskBudget(plain.ID, nil, f64(0), &eur); err != nil {
		t.Fatal(err)
	}
	v := budgetView(t, d, plain)
	if v.TokenBudget != 2000 || v.CostBudget != nil || v.CostCurrency != "EUR" || v.WarnedAt != nil {
		t.Fatalf("partial update: %+v", v)
	}
	for _, bad := range []struct {
		token *int64
		cost  *float64
	}{{i64(-5), nil}, {nil, f64(-1)}, {nil, f64(math.Inf(1))}} {
		if err = d.UpdateTaskBudget(plain.ID, bad.token, bad.cost, nil); err == nil {
			t.Fatalf("invalid update accepted: %+v", bad)
		}
	}
	if err = d.UpdateTaskBudget(plain.ID, nil, nil, ptr("US")); err == nil {
		t.Fatal("invalid currency accepted")
	}
}

func ptr[T any](v T) *T { return &v }

func TestBeginBudgetHold(t *testing.T) {
	d, task := openBudgetTestDB(t, TaskCreateOptions{TokenBudget: i64(10)})
	inserted, err := d.BeginBudgetHold(task.ID, "Budget reached: 12 of 10 tokens used")
	if err != nil || !inserted {
		t.Fatalf("first hold = %v, %v", inserted, err)
	}
	if again, err := d.BeginBudgetHold(task.ID, "second"); err != nil || again {
		t.Fatalf("second hold = %v, %v", again, err)
	}
	state, err := d.TaskApprovalState(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	active := state.Active()
	if active == nil || active.Outcome != BudgetHoldOutcome || active.Reason != "Budget reached: 12 of 10 tokens used" {
		t.Fatalf("active hold = %+v", active)
	}
	if got, _ := d.GetTask(task.ID); got == nil || !got.Paused {
		t.Fatalf("task not paused: %+v", got)
	}
	if v := budgetView(t, d, task); !v.PausedForBudget {
		t.Fatal("view does not report the budget hold")
	}
	// A finished task is never paused.
	if _, err = d.Exec(`UPDATE task_approval_holds SET ended_at=now() WHERE task_id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = d.Exec(`UPDATE tasks SET status='done' WHERE id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	if done, err := d.BeginBudgetHold(task.ID, "x"); err != nil || done {
		t.Fatalf("hold on a finished task = %v, %v", done, err)
	}
}

// archiveRoundTrip archives and restores a task; mutate may edit the snapshot to
// imitate an older archive.
func archiveRoundTrip(t *testing.T, d *DB, task *Task, mutate func(*TaskArchiveSnapshot)) {
	t.Helper()
	if err := d.EnsureLLMRecordsTable(); err != nil {
		t.Fatal(err)
	}
	if err := d.SetPaused(task.ID, true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM task_archives WHERE task_id=$1`, task.ID) })
	archive, err := d.QueueTaskArchive(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := d.ClaimTaskArchiveJob(t.Context()); err != nil || claimed == nil || claimed.ID != archive.ID {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	var records bytes.Buffer
	snapshot, err := d.SnapshotTaskArchiveWithLLMRecords(task.ID, &records)
	if err != nil {
		t.Fatal(err)
	}
	if mutate != nil {
		mutate(snapshot)
	}
	if err = d.CompleteTaskArchive(archive.ID, snapshot, "/tmp/budget-task.tar.zst", "abc", 100, 50); err != nil {
		t.Fatal(err)
	}
	if _, err = d.QueueTaskArchiveRestore(archive.ID); err != nil {
		t.Fatal(err)
	}
	if claimed, err := d.ClaimTaskArchiveJob(t.Context()); err != nil || claimed == nil {
		t.Fatalf("restore claim = %+v, %v", claimed, err)
	}
	if _, err = d.RestoreTaskArchiveWithLLMRecords(archive.ID, snapshot, int64((10 * time.Minute).Seconds()), bytes.NewReader(records.Bytes())); err != nil {
		t.Fatal(err)
	}
	if err = d.CompleteTaskArchiveRestore(archive.ID); err != nil {
		t.Fatal(err)
	}
}

func TestTaskBudgetArchiveRoundTrip(t *testing.T) {
	d, task := openBudgetTestDB(t, TaskCreateOptions{TokenBudget: i64(7000), CostBudget: f64(4.5), CostCurrency: "EUR"})
	cost := 0.25
	insertUsage(t, d, task, 400, 100, 300, &cost, "EUR")
	if first, err := d.MarkBudgetWarned(task.ID); err != nil || !first {
		t.Fatalf("warned = %v, %v", first, err)
	}
	archiveRoundTrip(t, d, task, func(s *TaskArchiveSnapshot) {
		var rows []map[string]any
		if err := json.Unmarshal(s.Tables["tasks"], &rows); err != nil || len(rows) != 1 {
			t.Fatalf("archived task row: %v %d", err, len(rows))
		}
		if rows[0]["token_budget"] == nil || rows[0]["cost_currency"] != "EUR" {
			t.Fatalf("archive does not carry the budget: %v", rows[0])
		}
	})
	v := budgetView(t, d, task)
	if v.TokenBudget != 7000 || v.CostBudget == nil || *v.CostBudget != 4.5 || v.CostCurrency != "EUR" || v.WarnedAt == nil {
		t.Fatalf("budget lost in the round trip: %+v", v)
	}
	if v.Tokens != 500 || math.Abs(v.Cost-0.25) > 1e-9 || v.UnknownCostCalls != 0 {
		t.Fatalf("usage lost in the round trip: %+v", v)
	}
}

// An archive written before budgets existed has none of the new columns and
// restores as a task without a budget, with unknown call costs.
func TestTaskBudgetArchiveRestoresWithoutBudgetForOldArchives(t *testing.T) {
	d, task := openBudgetTestDB(t, TaskCreateOptions{TokenBudget: i64(7000), CostBudget: f64(4.5), CostCurrency: "EUR"})
	cost := 0.25
	insertUsage(t, d, task, 400, 100, 300, &cost, "EUR")
	archiveRoundTrip(t, d, task, func(s *TaskArchiveSnapshot) {
		strip := func(table string, keys ...string) {
			var rows []map[string]any
			if err := json.Unmarshal(s.Tables[table], &rows); err != nil {
				t.Fatal(err)
			}
			for _, row := range rows {
				for _, key := range keys {
					delete(row, key)
				}
			}
			raw, _ := json.Marshal(rows)
			s.Tables[table] = raw
		}
		strip("tasks", "token_budget", "cost_budget", "cost_currency", "budget_warned_at")
		strip("llm_usage", "cost", "currency")
	})
	v := budgetView(t, d, task)
	if v.TokenBudget != 0 || v.CostBudget != nil || v.CostCurrency != "USD" || v.WarnedAt != nil {
		t.Fatalf("old archive restored with a budget: %+v", v)
	}
	if v.Tokens != 500 || v.Cost != 0 || v.UnknownCostCalls != 1 {
		t.Fatalf("old archive usage: tokens=%d cost=%v unknown=%d", v.Tokens, v.Cost, v.UnknownCostCalls)
	}
}
