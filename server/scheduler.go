package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/locale"
)

// Scheduler drives P3 triggers: on each tick it fires due interval triggers and
// scans for new findings / newly-met goals (any task) to fire event triggers.
// Each fire enqueues a NEW conversation on the agent's per-agent trigger queue
// (StartTriggeredRun): fires for the same agent run one at a time in FIFO order,
// distinct agents still run concurrently. State is persisted (per-trigger last_fire
// + finding watermark + fired-goal set) so a restart resumes without double-firing.
// Triggers only attach to CUSTOM agents.
type Scheduler struct {
	s    *Server
	pg   *db.DB
	tick time.Duration
	// start launches a triggered run; nil means Server.StartTriggeredRun. Tests
	// replace it to record fires.
	start func(agentKey, title, message string, taskID int64, mergeable bool, taskDesc, taskGoal string, langs ...locale.Lang)
	// Aggregate lookups used to seed and advance watermarks; nil means the real
	// pg method. Tests replace them to force a failure.
	maxFinding  func() (int64, error)
	maxToolCall func() (int64, error)
	metGoalIDs  func() ([]int64, error)
	newestTask  func() (db.TaskCursor, error)
	seeded      seededStreams
}

// seededStreams records, per event stream, whether its watermark is known: read
// back from persisted state or freshly written. An unseeded stream is not
// processed, since a missing watermark would read as zero and replay history.
type seededStreams struct {
	finding, goals, timeouts, toolCall, taskCreate bool
}

func (sc *Scheduler) maxFindingID() (int64, error) {
	if sc.maxFinding != nil {
		return sc.maxFinding()
	}
	return sc.pg.MaxFindingID()
}

func (sc *Scheduler) maxToolCallID() (int64, error) {
	if sc.maxToolCall != nil {
		return sc.maxToolCall()
	}
	return sc.pg.MaxToolCallID()
}

func (sc *Scheduler) metGoalIDList() ([]int64, error) {
	if sc.metGoalIDs != nil {
		return sc.metGoalIDs()
	}
	return sc.pg.MetGoalIDs()
}

func (sc *Scheduler) newestTaskCursor() (db.TaskCursor, error) {
	if sc.newestTask != nil {
		return sc.newestTask()
	}
	return sc.pg.NewestTaskCursor()
}

// readState returns the stored value, "" when none is stored, and an error when
// the read itself failed.
func (sc *Scheduler) readState(key string) (string, error) {
	return sc.pg.GetSchedState(key)
}

func (sc *Scheduler) startRun(agentKey, title, message string, taskID int64, mergeable bool, taskDesc, taskGoal string, langs ...locale.Lang) {
	if sc.start != nil {
		sc.start(agentKey, title, message, taskID, mergeable, taskDesc, taskGoal, langs...)
		return
	}
	sc.s.StartTriggeredRun(agentKey, title, message, taskID, mergeable, taskDesc, taskGoal, langs...)
}

// logWatermarkErr reports a failed watermark query. The stream's state is left
// untouched so the next pass retries; writing 0 would replay the whole history.
func logWatermarkErr(stream string, err error) {
	log.Printf(locale.Text(locale.ServerDefault(), "[scheduler] watermark query failed (%s): %v"), stream, err)
}

const (
	schedKeyLastFinding    = "last_finding_id"        // watermark: max finding node id fired for
	schedKeyFiredGoals     = "fired_goals"            // JSON array of goal node ids already fired
	schedKeyFiredTimeouts  = "fired_timeouts"         // JSON array of TaskCursor strings already fired for task timeout
	schedKeyLastToolCall   = "last_toolcall_id"       // watermark: max activity id fired for tool call
	schedKeyLastTaskCreate = "last_taskcreate_cursor" // watermark: (created_at, id) of the last task-create event
	// schedKeyLegacyTaskCreate is the earlier id-only watermark, read once to migrate.
	schedKeyLegacyTaskCreate = "last_taskcreate_id"
)

const (
	toolCallPage      = 500  // Max tool-call events loaded per query.
	toolCallDetailMax = 1500 // Characters of tool input/output kept in a trigger message.
)

func newScheduler(s *Server) *Scheduler {
	return &Scheduler{s: s, pg: s.m.pg, tick: 5 * time.Second}
}

// Run loops until ctx is done, ticking the scheduler. Started once from server New.
func (sc *Scheduler) Run(ctx context.Context) {
	if sc.pg == nil {
		return
	}
	sc.init()
	t := time.NewTicker(sc.tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sc.s.reconcileConcurrency() // Fill available concurrency slots with queued tasks.
			sc.step()
		}
	}
}

// init seeds the watermarks on first run so pre-existing findings/goals don't all
// fire at once — only events created AFTER the scheduler first starts count.
func (sc *Scheduler) init() {
	sc.seeded = seededStreams{
		finding:  sc.seedID(schedKeyLastFinding, "finding", sc.maxFindingID),
		toolCall: sc.seedID(schedKeyLastToolCall, "tool call", sc.maxToolCallID),
	}
	sc.seeded.goals = sc.seed(schedKeyFiredGoals, "goal", func() error {
		ids, err := sc.metGoalIDList()
		if err != nil {
			return err
		}
		set := map[int64]bool{}
		for _, id := range ids {
			set[id] = true
		}
		return sc.saveFiredGoalSet(set)
	})
	// First start: tasks already timed out are history, not new events.
	sc.seeded.timeouts = sc.seed(schedKeyFiredTimeouts, "timeout", func() error {
		evs, err := sc.pg.TimedOutTasks()
		if err != nil {
			return err
		}
		return sc.saveFiredTimeouts(timeoutKeys(evs))
	})
	raw, err := sc.readState(schedKeyLastTaskCreate)
	if err != nil {
		logWatermarkErr("task create", err)
	} else if _, ok := db.ParseTaskCursor(raw); ok {
		sc.seeded.taskCreate = true
	} else if c, err := sc.initialTaskCursor(); err != nil {
		logWatermarkErr("task create", err)
	} else if err := sc.pg.SetSchedState(schedKeyLastTaskCreate, c.String()); err != nil {
		logWatermarkErr("task create", err)
	} else {
		sc.seeded.taskCreate = true
	}
}

// seed reports whether the state at key is known, running write to create it
// when none is stored. A failed read or write leaves the stream unseeded.
func (sc *Scheduler) seed(key, stream string, write func() error) bool {
	v, err := sc.readState(key)
	if err == nil && v == "" {
		err = write()
	}
	if err != nil {
		logWatermarkErr(stream, err)
		return false
	}
	return true
}

// seedID is seed for an id watermark initialised to the stream's current maximum.
func (sc *Scheduler) seedID(key, stream string, newest func() (int64, error)) bool {
	return sc.seed(key, stream, func() error {
		id, err := newest()
		if err != nil {
			return err
		}
		return sc.pg.SetSchedState(key, strconv.FormatInt(id, 10))
	})
}

func (sc *Scheduler) step() {
	triggers, err := sc.pg.ListEnabledTriggers()
	if err != nil {
		return
	}
	sc.stepWith(triggers)
}

// stepWith runs one scheduler pass for the given enabled triggers.
func (sc *Scheduler) stepWith(triggers []*db.AgentTrigger) {
	sc.init() // Seeds any stream whose first-boot seeding failed; a no-op otherwise.
	if len(triggers) == 0 {
		sc.advanceWatermarks()
		return
	}
	sc.fireIntervals(triggers)
	if sc.seeded.finding {
		sc.fireFindings(triggers)
	}
	if sc.seeded.goals {
		sc.fireGoals(triggers)
	}
	if sc.seeded.timeouts {
		sc.fireTaskTimeouts(triggers)
	}
	if sc.seeded.toolCall {
		sc.fireToolCalls(triggers)
	}
	if sc.seeded.taskCreate {
		sc.fireTaskCreates(triggers)
	}
}

// advanceWatermarks moves every event watermark to the current end of its stream
// without loading the events. It runs when no trigger is enabled, so a trigger
// enabled later fires only for events created after it was enabled.
func (sc *Scheduler) advanceWatermarks() {
	advanceID := func(key, stream string, seeded bool, newest func() (int64, error)) {
		if !seeded {
			return
		}
		id, err := newest()
		if err != nil {
			logWatermarkErr(stream, err)
			return
		}
		if last, _ := strconv.ParseInt(sc.mustState(key), 10, 64); id > last {
			_ = sc.pg.SetSchedState(key, strconv.FormatInt(id, 10))
		}
	}
	advanceID(schedKeyLastFinding, "finding", sc.seeded.finding, sc.maxFindingID)
	advanceID(schedKeyLastToolCall, "tool call", sc.seeded.toolCall, sc.maxToolCallID)
	if sc.seeded.goals {
		if ids, err := sc.metGoalIDList(); err != nil {
			logWatermarkErr("goal", err)
		} else {
			fired := sc.firedGoalSet()
			changed := false
			for _, id := range ids {
				if !fired[id] {
					fired[id] = true
					changed = true
				}
			}
			if changed {
				_ = sc.saveFiredGoalSet(fired)
			}
		}
	}
	if sc.seeded.timeouts {
		sc.fireTaskTimeouts(nil)
	}
	if !sc.seeded.taskCreate {
		return
	}
	if newest, err := sc.newestTaskCursor(); err != nil {
		logWatermarkErr("task create", err)
	} else {
		last, _ := db.ParseTaskCursor(sc.mustState(schedKeyLastTaskCreate))
		if newest.CreatedAt.After(last.CreatedAt) || (newest.CreatedAt.Equal(last.CreatedAt) && newest.ID > last.ID) {
			_ = sc.pg.SetSchedState(schedKeyLastTaskCreate, newest.String())
		}
	}
}

// fireIntervals fires triggers whose interval has elapsed since last_fire.
func (sc *Scheduler) fireIntervals(triggers []*db.AgentTrigger) {
	now := time.Now()
	lang := locale.ServerDefault()
	for _, tr := range triggers {
		if tr.IntervalSec <= 0 {
			continue
		}
		due := tr.LastFire == nil || now.Sub(*tr.LastFire) >= time.Duration(tr.IntervalSec)*time.Second
		if !due {
			continue
		}
		_ = sc.pg.TouchTriggerFire(tr.ID)
		ctx := locale.Text(lang, "\n\n[Scheduled trigger]") + now.Format(" 2006-01-02 15:04:05 MST")
		sc.startRun(tr.AgentKey, fmt.Sprintf(locale.Text(lang, "Scheduled trigger · %s"), now.Format("15:04")), reporterTriggerText(tr.IntervalMessage, lang)+ctx, 0, false, "", "", lang)
	}
}

// fireFindings fires on_finding triggers for findings above the persisted
// watermark (monotonic node id → no double-fire across restarts).
func (sc *Scheduler) fireFindings(triggers []*db.AgentTrigger) {
	var want []*db.AgentTrigger
	for _, tr := range triggers {
		if tr.OnFinding {
			want = append(want, tr)
		}
	}
	last, _ := strconv.ParseInt(sc.mustState(schedKeyLastFinding), 10, 64)
	events, err := sc.pg.NewFindingsSince(last)
	if err != nil || len(events) == 0 {
		return
	}
	// Advance the watermark whether or not any on_finding trigger is active: a
	// finding fires only for triggers live at the moment it appears. Otherwise a
	// trigger enabled later would replay the entire historical backlog at once.
	maxID := last
	for _, e := range events {
		lang := taskLanguage(sc.pg, strconv.FormatInt(e.TaskID, 10))
		if e.NodeID > maxID {
			maxID = e.NodeID
		}
		if len(want) == 0 {
			continue
		}
		msgCtx := fmt.Sprintf(locale.Text(lang, "\n\n[Triggered by a task finding]\nFinding: [%s/%s] %s"),
			e.VulnClass, e.Severity, e.Summary)
		for _, tr := range want {
			sc.startRun(tr.AgentKey, fmt.Sprintf(locale.Text(lang, "Finding trigger · task#%d"), e.TaskID), reporterTriggerText(tr.FindingMessage, lang)+msgCtx, e.TaskID, true, e.TaskDesc, e.TaskGoal, lang)
		}
	}
	_ = sc.pg.SetSchedState(schedKeyLastFinding, strconv.FormatInt(maxID, 10))
}

// fireGoals fires on_goal_met triggers for met goals not yet in the fired set.
func (sc *Scheduler) fireGoals(triggers []*db.AgentTrigger) {
	var want []*db.AgentTrigger
	for _, tr := range triggers {
		if tr.OnGoalMet {
			want = append(want, tr)
		}
	}
	events, err := sc.pg.MetGoals()
	if err != nil || len(events) == 0 {
		return
	}
	// Mark goals as consumed whether or not a trigger is active, so enabling an
	// on_goal_met trigger later doesn't replay every already-met goal.
	fired := sc.firedGoalSet()
	changed := false
	for _, e := range events {
		lang := taskLanguage(sc.pg, strconv.FormatInt(e.TaskID, 10))
		if fired[e.NodeID] {
			continue
		}
		fired[e.NodeID] = true
		changed = true
		if len(want) == 0 {
			continue
		}
		msgCtx := fmt.Sprintf(locale.Text(lang, "\n\n[Triggered by task goal completion]\nCompleted goal: %s"), e.Summary)
		for _, tr := range want {
			sc.startRun(tr.AgentKey, fmt.Sprintf(locale.Text(lang, "Goal trigger · task#%d"), e.TaskID), reporterTriggerText(tr.GoalMessage, lang)+msgCtx, e.TaskID, true, e.TaskDesc, e.TaskGoal, lang)
		}
	}
	if changed {
		sc.saveFiredGoalSet(fired)
	}
}

// initialTaskCursor seeds the task-create watermark. A fresh install starts after
// the newest task. An upgrade from the id-only watermark keeps its meaning: it
// resumes just before the earliest task whose id is above the legacy value, so
// tasks created while the scheduler was offline still fire.
func (sc *Scheduler) initialTaskCursor() (db.TaskCursor, error) {
	raw, err := sc.readState(schedKeyLegacyTaskCreate)
	if err != nil {
		return db.TaskCursor{}, err
	}
	legacy, perr := strconv.ParseInt(raw, 10, 64)
	if perr != nil {
		return sc.newestTaskCursor()
	}
	evs, err := sc.pg.NewTasksSince(db.TaskCursor{})
	if err != nil || len(evs) == 0 {
		return db.TaskCursor{}, err
	}
	at := func(i int) db.TaskCursor {
		if i < 0 {
			return db.TaskCursor{}
		}
		return db.TaskCursor{CreatedAt: evs[i].CreatedAt, ID: evs[i].TaskID}
	}
	for i, e := range evs {
		if e.TaskID > legacy {
			return at(i - 1), nil
		}
	}
	return at(len(evs) - 1), nil
}

// fireTaskTimeouts fires on_task_timeout triggers once for each task that newly
// reached status='timeout'. Fired tasks are remembered by (created_at, id), so a
// reused id or an out-of-order timeout still fires; entries are dropped once the
// task leaves the timeout status.
func (sc *Scheduler) fireTaskTimeouts(triggers []*db.AgentTrigger) {
	var want []*db.AgentTrigger
	for _, tr := range triggers {
		if tr.OnTaskTimeout {
			want = append(want, tr)
		}
	}
	events, err := sc.pg.TimedOutTasks()
	if err != nil {
		return
	}
	fired := sc.firedTimeouts()
	current := timeoutKeys(events)
	for i, e := range events {
		if fired[current[i]] || len(want) == 0 {
			continue // Still record it below even with no active trigger — see fireFindings.
		}
		lang := taskLanguage(sc.pg, strconv.FormatInt(e.TaskID, 10))
		msgCtx := locale.Text(lang, "\n\n[Triggered by a task timeout]")
		for _, tr := range want {
			sc.startRun(tr.AgentKey, fmt.Sprintf(locale.Text(lang, "Timeout trigger · task#%d"), e.TaskID), reporterTriggerText(tr.TaskTimeoutMessage, lang)+msgCtx, e.TaskID, true, e.TaskDesc, e.TaskGoal, lang)
		}
	}
	if len(current) != len(fired) || !sameKeys(fired, current) {
		sc.saveFiredTimeouts(current)
	}
}

func timeoutKeys(events []db.TaskEvent) []string {
	keys := make([]string, len(events))
	for i, e := range events {
		keys[i] = db.TaskCursor{CreatedAt: e.CreatedAt, ID: e.TaskID}.String()
	}
	return keys
}

func sameKeys(set map[string]bool, keys []string) bool {
	for _, k := range keys {
		if !set[k] {
			return false
		}
	}
	return true
}

func (sc *Scheduler) firedTimeouts() map[string]bool {
	out := map[string]bool{}
	var keys []string
	if json.Unmarshal([]byte(sc.mustState(schedKeyFiredTimeouts)), &keys) == nil {
		for _, k := range keys {
			out[k] = true
		}
	}
	return out
}

func (sc *Scheduler) saveFiredTimeouts(keys []string) error {
	b, _ := json.Marshal(keys)
	err := sc.pg.SetSchedState(schedKeyFiredTimeouts, string(b))
	if err != nil {
		log.Printf(locale.Text(locale.ServerDefault(), "[scheduler] save fired timeouts failed: %v"), err)
	}
	return err
}

// fireTaskCreates fires on_task_create triggers for tasks created after the
// persisted (created_at, id) watermark: no double-fire across restarts, and
// reused task ids still fire.
func (sc *Scheduler) fireTaskCreates(triggers []*db.AgentTrigger) {
	var want []*db.AgentTrigger
	for _, tr := range triggers {
		if tr.OnTaskCreate {
			want = append(want, tr)
		}
	}
	last, _ := db.ParseTaskCursor(sc.mustState(schedKeyLastTaskCreate))
	events, err := sc.pg.NewTasksSince(last)
	if err != nil || len(events) == 0 {
		return
	}
	// Advance the watermark even with no active trigger — see fireFindings.
	for _, e := range events {
		lang := taskLanguage(sc.pg, strconv.FormatInt(e.TaskID, 10))
		if len(want) == 0 {
			continue
		}
		msgCtx := locale.Text(lang, "\n\n[Triggered by task creation]")
		for _, tr := range want {
			sc.startRun(tr.AgentKey, fmt.Sprintf(locale.Text(lang, "Task creation trigger · task#%d"), e.TaskID), reporterTriggerText(tr.TaskCreateMessage, lang)+msgCtx, e.TaskID, true, e.TaskDesc, e.TaskGoal, lang)
		}
	}
	end := events[len(events)-1]
	_ = sc.pg.SetSchedState(schedKeyLastTaskCreate, db.TaskCursor{CreatedAt: end.CreatedAt, ID: end.TaskID}.String())
}

// fireToolCalls fires on_tool_call triggers for tool calls (tool_result rows) above
// the persisted watermark whose tool name is in the trigger's selected set. The fire
// message carries the task id/desc/goal + tool name + (truncated) input & output.
func (sc *Scheduler) fireToolCalls(triggers []*db.AgentTrigger) {
	var want []*db.AgentTrigger
	for _, tr := range triggers {
		if tr.OnToolCall && len(tr.ToolNames) > 0 {
			want = append(want, tr)
		}
	}
	last, _ := strconv.ParseInt(sc.mustState(schedKeyLastToolCall), 10, 64)
	langs := map[int64]locale.Lang{} // Per-pass cache: one lookup per task.
	// Drain the backlog one bounded page at a time, persisting the watermark after
	// each page. It advances even with no active trigger: see fireFindings.
	for {
		events, err := sc.pg.NewToolCallsSince(last, toolCallPage, toolCallDetailMax)
		if err != nil || len(events) == 0 {
			return
		}
		for _, e := range events {
			last = e.NodeID
			if len(want) == 0 {
				continue
			}
			// A failed finding write has no committed finding to report. Keep other
			// tool-error triggers available for user-defined automation.
			if e.ToolIsErr && e.Tool == "report_finding" {
				continue
			}
			var matched []*db.AgentTrigger
			for _, tr := range want {
				if containsFold(tr.ToolNames, e.Tool) {
					matched = append(matched, tr)
				}
			}
			if len(matched) == 0 {
				continue
			}
			lang, ok := langs[e.TaskID]
			if !ok {
				lang = taskLanguage(sc.pg, strconv.FormatInt(e.TaskID, 10))
				langs[e.TaskID] = lang
			}
			errTag := ""
			if e.ToolIsErr {
				errTag = locale.Text(lang, "[error] ")
			}
			msgCtx := fmt.Sprintf(locale.Text(lang, "\n\n[Triggered by a tool call]\nTool: %s\nInput: %s\nOutput: %s%s"),
				e.Tool, truncTotal(e.ToolInput, e.ToolInputLen, toolCallDetailMax, lang), errTag, truncTotal(e.ToolOutput, e.ToolOutputLen, toolCallDetailMax, lang))
			for _, tr := range matched {
				sc.startRun(tr.AgentKey, fmt.Sprintf(locale.Text(lang, "Tool trigger · %s · task#%d"), e.Tool, e.TaskID), reporterTriggerText(tr.ToolCallMessage, lang)+msgCtx, e.TaskID, true, e.TaskDesc, e.TaskGoal, lang)
			}
		}
		_ = sc.pg.SetSchedState(schedKeyLastToolCall, strconv.FormatInt(last, 10))
		if len(events) < toolCallPage {
			return
		}
	}
}

// containsFold reports whether name is in set (case-insensitive).
func containsFold(set []string, name string) bool {
	for _, s := range set {
		if strings.EqualFold(s, name) {
			return true
		}
	}
	return false
}

// trunc caps s to max runes, appending an ellipsis + original length when cut.
func trunc(s string, max int, langs ...locale.Lang) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return truncTotal(string(r[:max]), len(r), max, locale.First(langs))
}

// truncTotal is trunc for text already cut to max runes in SQL: total is the
// original length, and the truncation note is appended only when it exceeds max.
func truncTotal(s string, total, max int, lang locale.Lang) string {
	if total <= max {
		return s
	}
	return s + fmt.Sprintf(locale.Text(lang, "…(truncated; %d characters total)"), total)
}

func (sc *Scheduler) mustState(key string) string {
	v, _ := sc.pg.GetSchedState(key)
	return v
}

func (sc *Scheduler) firedGoalSet() map[int64]bool {
	out := map[int64]bool{}
	raw := sc.mustState(schedKeyFiredGoals)
	if raw == "" {
		return out
	}
	var ids []int64
	if json.Unmarshal([]byte(raw), &ids) == nil {
		for _, id := range ids {
			out[id] = true
		}
	}
	return out
}

func (sc *Scheduler) saveFiredGoalSet(set map[int64]bool) error {
	ids := make([]int64, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	b, _ := json.Marshal(ids)
	err := sc.pg.SetSchedState(schedKeyFiredGoals, string(b))
	if err != nil {
		log.Printf(locale.Text(locale.ServerDefault(), "[scheduler] save fired goals failed: %v"), err)
	}
	return err
}
