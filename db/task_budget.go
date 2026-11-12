package db

import (
	"database/sql"
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Philocyber/counterweight/locale"
)

// Per-task usage budget.
//
// Counting rule: a task's token usage is SUM(input_tokens + output_tokens) over
// its llm_usage rows. The SDK normalises usage so that cache_read and
// cache_write are a SUBSET of input_tokens, so the cache columns are never added
// again. Estimated cost is SUM(cost) over rows whose stored currency matches the
// task's cost currency; rows that carry tokens but no stored cost (the profile
// had no prices, or another currency) are counted as unknown, never as zero.

const (
	// SettingDefaultTaskTokenBudget is the global token budget applied to tasks
	// created without an explicit one. Absent or 0 means no limit.
	SettingDefaultTaskTokenBudget = "task_token_budget_default"

	// BudgetWarnPercent is the share of a budget at which the one warning fires.
	BudgetWarnPercent = 80

	// BudgetHoldOutcome marks a task_approval_holds row that pauses a task because
	// a usage budget was reached. Its reason column carries the figures.
	BudgetHoldOutcome = "budget"

	// DefaultCurrency is used when a profile or task does not name one.
	DefaultCurrency = "USD"

	// MaxTaskTokenBudget bounds a token budget to a sane value (one quadrillion).
	MaxTaskTokenBudget = int64(1_000_000_000_000_000)
)

var (
	ErrBudgetInvalid   = locale.NewError("Budget values must be zero or positive numbers")
	ErrCurrencyInvalid = locale.NewError("Currency must be a three-letter code such as USD")

	currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)
)

// NormalizeCurrency upper-cases a currency code and defaults an empty one to USD.
func NormalizeCurrency(code string) (string, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return DefaultCurrency, nil
	}
	if !currencyPattern.MatchString(code) {
		return "", ErrCurrencyInvalid
	}
	return code, nil
}

// ModelPricing is a profile's optional price list, per million tokens. A nil
// price means unset. Cost is computable only when both Input and Output are set.
type ModelPricing struct {
	Input       *float64
	CachedInput *float64
	Output      *float64
	Currency    string
}

// Priced reports whether the list is complete enough to estimate a call.
func (p ModelPricing) Priced() bool { return p.Input != nil && p.Output != nil }

// CallCost estimates one call. inputTokens includes the cached subset
// (cacheRead), so the cached part is billed at the cached price (the input price
// when no cached price is set) and the rest at the input price. Cache writes are
// billed as ordinary input. ok=false means the cost is unknown.
func (p ModelPricing) CallCost(inputTokens, outputTokens, cacheRead int) (cost float64, ok bool) {
	if !p.Priced() {
		return 0, false
	}
	cacheRead = min(max(cacheRead, 0), max(inputTokens, 0))
	cachedPrice := *p.Input
	if p.CachedInput != nil {
		cachedPrice = *p.CachedInput
	}
	fresh := float64(max(inputTokens, 0) - cacheRead)
	cost = (fresh**p.Input + float64(cacheRead)*cachedPrice + float64(max(outputTokens, 0))**p.Output) / 1e6
	return cost, true
}

// validPrice reports whether a price/limit is absent or a finite non-negative number.
func validPrice(v *float64) bool {
	return v == nil || (!math.IsNaN(*v) && !math.IsInf(*v, 0) && *v >= 0)
}

// Pricing returns the profile's price list.
func (p *LLMProfile) Pricing() ModelPricing {
	return ModelPricing{Input: p.PriceInput, CachedInput: p.PriceCachedInput, Output: p.PriceOutput, Currency: p.PriceCurrency}
}

// ValidatePricing normalises the currency and rejects negative or non-finite prices.
func (p *LLMProfile) ValidatePricing() error {
	if !validPrice(p.PriceInput) || !validPrice(p.PriceCachedInput) || !validPrice(p.PriceOutput) {
		return ErrBudgetInvalid
	}
	currency, err := NormalizeCurrency(p.PriceCurrency)
	if err != nil {
		return err
	}
	p.PriceCurrency = currency
	return nil
}

// ProfilePricing loads one profile's current price list (a missing profile is unpriced).
func (d *DB) ProfilePricing(id int64) (ModelPricing, error) {
	var in, cached, out sql.NullFloat64
	var currency string
	err := d.QueryRow(`SELECT price_input_per_million, price_cached_input_per_million, price_output_per_million, COALESCE(price_currency,'USD')
FROM llm_profiles WHERE id=$1`, id).Scan(&in, &cached, &out, &currency)
	if errors.Is(err, sql.ErrNoRows) {
		return ModelPricing{}, nil
	}
	if err != nil {
		return ModelPricing{}, err
	}
	p := ModelPricing{Currency: currency}
	if in.Valid {
		p.Input = &in.Float64
	}
	if cached.Valid {
		p.CachedInput = &cached.Float64
	}
	if out.Valid {
		p.Output = &out.Float64
	}
	return p, nil
}

// saveProfilePricing writes a profile's price fields.
func (d *DB) saveProfilePricing(id int64, p *LLMProfile) error {
	_, err := d.Exec(`UPDATE llm_profiles SET price_input_per_million=$2, price_cached_input_per_million=$3, price_output_per_million=$4, price_currency=$5 WHERE id=$1`,
		id, p.PriceInput, p.PriceCachedInput, p.PriceOutput, p.PriceCurrency)
	return err
}

// TaskBudgetView is a task's budget settings together with its recorded usage.
type TaskBudgetView struct {
	TaskID       int64
	TokenBudget  int64    // 0 = no token limit
	CostBudget   *float64 // nil = no cost limit
	CostCurrency string
	WarnedAt     *time.Time

	Tokens           int64   // input + output tokens recorded for the task
	Calls            int64   // ledger rows
	Cost             float64 // estimated cost over rows priced in CostCurrency
	UnknownCostCalls int64   // calls with tokens but no usable stored cost
	MaxCallTokens    int64   // largest single call so far, the per-call reservation estimate
	MaxCallCost      float64
	PausedForBudget  bool // an active budget hold exists
}

// Limited reports whether any budget is set.
func (v TaskBudgetView) Limited() bool { return v.TokenBudget > 0 || v.CostBudget != nil }

// ReachedKind returns "tokens" or "cost" when that budget is used up, else "".
func (v TaskBudgetView) ReachedKind() string {
	if v.TokenBudget > 0 && v.Tokens >= v.TokenBudget {
		return "tokens"
	}
	if v.CostBudget != nil && v.Cost >= *v.CostBudget {
		return "cost"
	}
	return ""
}

// Percent is the larger of the token and cost shares used, 0 when no budget is set.
func (v TaskBudgetView) Percent() float64 {
	pct := 0.0
	if v.TokenBudget > 0 {
		pct = float64(v.Tokens) / float64(v.TokenBudget) * 100
	}
	if v.CostBudget != nil {
		pct = math.Max(pct, v.Cost / *v.CostBudget * 100)
	}
	return math.Round(pct*10) / 10
}

// State is ok, warning (80 percent or more) or reached.
func (v TaskBudgetView) State() string {
	switch {
	case !v.Limited():
		return "ok"
	case v.ReachedKind() != "":
		return "reached"
	case v.Percent() >= BudgetWarnPercent:
		return "warning"
	default:
		return "ok"
	}
}

// MinCallEstimate is the token estimate reserved for a call when the task has
// not yet made a larger one.
const MinCallEstimate = 4096

// Fits reports whether one more call fits when inflight calls are already
// running: recorded usage plus an estimate (the largest call seen, at least
// MinCallEstimate tokens) for each of them and for the new one must stay under
// every limit. With nothing in flight a call always fits while the budget is not
// reached.
func (v TaskBudgetView) Fits(inflight int) bool {
	if inflight <= 0 {
		return true
	}
	n := float64(inflight + 1)
	if v.TokenBudget > 0 && float64(v.Tokens)+n*float64(max(v.MaxCallTokens, MinCallEstimate)) > float64(v.TokenBudget) {
		return false
	}
	if v.CostBudget != nil && v.Cost+n*v.MaxCallCost > *v.CostBudget {
		return false
	}
	return true
}

const taskBudgetViewSQL = `SELECT t.id, t.token_budget, t.cost_budget, t.cost_currency, t.budget_warned_at,
  COALESCE(SUM(u.input_tokens+u.output_tokens),0)::bigint,
  COUNT(u.id)::bigint,
  COALESCE(SUM(u.cost) FILTER (WHERE u.currency = t.cost_currency),0)::float8,
  (COUNT(*) FILTER (WHERE u.id IS NOT NULL AND u.input_tokens+u.output_tokens>0 AND (u.cost IS NULL OR u.currency IS DISTINCT FROM t.cost_currency)))::bigint,
  COALESCE(MAX(u.input_tokens+u.output_tokens),0)::bigint,
  COALESCE(MAX(u.cost) FILTER (WHERE u.currency = t.cost_currency),0)::float8,
  EXISTS(SELECT 1 FROM task_approval_holds h WHERE h.task_id=t.id AND h.ended_at IS NULL AND h.outcome='budget')
FROM tasks t LEFT JOIN llm_usage u ON u.task_id = t.id::text
WHERE t.deleted_at IS NULL`

func scanTaskBudgetView(sc interface{ Scan(...any) error }) (TaskBudgetView, error) {
	var v TaskBudgetView
	var cost sql.NullFloat64
	var warned sql.NullTime
	if err := sc.Scan(&v.TaskID, &v.TokenBudget, &cost, &v.CostCurrency, &warned, &v.Tokens, &v.Calls, &v.Cost,
		&v.UnknownCostCalls, &v.MaxCallTokens, &v.MaxCallCost, &v.PausedForBudget); err != nil {
		return v, err
	}
	if cost.Valid && cost.Float64 > 0 {
		v.CostBudget = &cost.Float64
	}
	if warned.Valid {
		v.WarnedAt = &warned.Time
	}
	return v, nil
}

// TaskBudgetView returns the budget settings and recorded usage of one task, or
// nil when the task does not exist.
func (d *DB) TaskBudgetView(taskID int64) (*TaskBudgetView, error) {
	v, err := scanTaskBudgetView(d.QueryRow(taskBudgetViewSQL+` AND t.id=$1 GROUP BY t.id`, taskID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// TaskBudgetViews returns the view of every live task in one pass, for task lists.
func (d *DB) TaskBudgetViews() (map[int64]TaskBudgetView, error) {
	rows, err := d.Query(taskBudgetViewSQL + ` GROUP BY t.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]TaskBudgetView{}
	for rows.Next() {
		v, err := scanTaskBudgetView(rows)
		if err != nil {
			return nil, err
		}
		out[v.TaskID] = v
	}
	return out, rows.Err()
}

// TaskBudgetView lets the task DTO read its own task's view through the store.
func (s *ExplorationStore) TaskBudgetView(taskID int64) (*TaskBudgetView, error) {
	return s.db.TaskBudgetView(taskID)
}

// UpdateTaskBudget changes a task's budgets. A nil argument leaves that value
// unchanged; a token budget of 0 or a cost budget of 0 removes that limit. The
// 80 percent warning is re-armed because the limits moved.
func (d *DB) UpdateTaskBudget(taskID int64, tokenBudget *int64, costBudget *float64, currency *string) error {
	if tokenBudget != nil && (*tokenBudget < 0 || *tokenBudget > MaxTaskTokenBudget) || !validPrice(costBudget) {
		return ErrBudgetInvalid
	}
	var code *string
	if currency != nil {
		c, err := NormalizeCurrency(*currency)
		if err != nil {
			return err
		}
		code = &c
	}
	res, err := d.Exec(`UPDATE tasks SET
 token_budget=COALESCE($2,token_budget),
 cost_budget=CASE WHEN $3::float8 IS NULL THEN cost_budget WHEN $3::float8 <= 0 THEN NULL ELSE $3::float8 END,
 cost_currency=COALESCE($4,cost_currency),
 budget_warned_at=NULL
WHERE id=$1 AND deleted_at IS NULL`, taskID, tokenBudget, costBudget, code)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// applyNewTaskBudgetTx stores the budgets of a task being created. An absent
// token budget takes the global default.
func applyNewTaskBudgetTx(tx *sql.Tx, taskID int64, opts TaskCreateOptions) error {
	if !validPrice(opts.CostBudget) || (opts.TokenBudget != nil && (*opts.TokenBudget < 0 || *opts.TokenBudget > MaxTaskTokenBudget)) {
		return ErrBudgetInvalid
	}
	currency, err := NormalizeCurrency(opts.CostCurrency)
	if err != nil {
		return err
	}
	var token int64
	if opts.TokenBudget != nil {
		token = *opts.TokenBudget
	} else {
		var raw string
		switch err := tx.QueryRow(`SELECT value FROM settings WHERE key=$1`, SettingDefaultTaskTokenBudget).Scan(&raw); {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return err
		default:
			token, _ = strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		}
	}
	var cost *float64
	if opts.CostBudget != nil && *opts.CostBudget > 0 {
		cost = opts.CostBudget
	}
	if token == 0 && cost == nil && currency == DefaultCurrency {
		return nil
	}
	_, err = tx.Exec(`UPDATE tasks SET token_budget=$2, cost_budget=$3, cost_currency=$4 WHERE id=$1`, taskID, max(token, 0), cost, currency)
	return err
}

// DefaultTaskTokenBudget returns the global default token budget (0 = no limit).
func (d *DB) DefaultTaskTokenBudget() int64 {
	raw, ok, err := d.GetSetting(SettingDefaultTaskTokenBudget)
	if err != nil || !ok {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// SetDefaultTaskTokenBudget stores the global default; 0 removes the default.
func (d *DB) SetDefaultTaskTokenBudget(n int64) error {
	if n < 0 || n > MaxTaskTokenBudget {
		return ErrBudgetInvalid
	}
	return d.SetSetting(SettingDefaultTaskTokenBudget, strconv.FormatInt(n, 10))
}

// BeginBudgetHold pauses a task because a budget was reached and records the
// reason (the figures) as an active hold, the same persisted pause channel that
// permission requests use. inserted is false when the task is finished or
// another hold is already active, in which case nothing is written except the
// paused flag.
func (d *DB) BeginBudgetHold(taskID int64, reason string) (inserted bool, err error) {
	tx, err := d.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback() //nolint:errcheck
	var status string
	if err = tx.QueryRow(`SELECT status FROM tasks WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, taskID).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if IsTerminal(status) {
		return false, nil
	}
	if _, err = tx.Exec(`UPDATE tasks SET paused=true,queued=false,queued_at=NULL WHERE id=$1`, taskID); err != nil {
		return false, err
	}
	res, err := tx.Exec(`INSERT INTO task_approval_holds(task_id,outcome,reason) VALUES($1,$2,$3) ON CONFLICT(task_id) WHERE ended_at IS NULL DO NOTHING`, taskID, BudgetHoldOutcome, reason)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, tx.Commit()
}

// MarkBudgetWarned records the 80 percent warning and reports whether this call
// was the one that recorded it, so the warning is emitted exactly once.
func (d *DB) MarkBudgetWarned(taskID int64) (bool, error) {
	res, err := d.Exec(`UPDATE tasks SET budget_warned_at=now() WHERE id=$1 AND deleted_at IS NULL AND budget_warned_at IS NULL`, taskID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
