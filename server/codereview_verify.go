package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"slices"
	"strconv"
	"strings"

	actool "github.com/Autumn-27/norma/tool"
	"github.com/Philocyber/counterweight/agent"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/intercept"
	"github.com/Philocyber/counterweight/locale"
)

// Independent verification of white-box code review findings.
//
// Every finding recorded by a /code-review task is handed, automatically, to a
// separate conversation of the codeverifier agent that did not write it and tries
// to refute it from the cloned source (the candidate-verifier contract adapted
// from cloudflare/security-audit-skill). Runs reuse the finding_retests machinery
// with kind='verify': the server binds each conversation to exactly one finding,
// seals the run when the turn ends, and maps the verdict onto the finding status
// (confirmed → confirmed, rejected → false_positive, needs_validation → unchanged).

// codeVerifyParallel bounds concurrent verifier conversations across all tasks.
// Queued verifications stay 'pending' (visible in the finding details) until a slot frees.
const codeVerifyParallel = 2

var codeVerifySlots = make(chan struct{}, codeVerifyParallel)

var codeVerifierTools = []string{"get_finding_verification_context", "record_finding_verification_result"}

// verifyingRecorder decorates the task finding recorder: after a finding commits
// in a code review task, its independent verification is queued. Recording never
// fails or waits because of verification.
type verifyingRecorder struct {
	inner agent.FindingRecorder
	after func(taskID, findingID int64)
}

func (r verifyingRecorder) Record(ctx context.Context, in db.RecordFindingInput, refs []db.TrafficRef) (*db.RecordedFinding, error) {
	out, err := r.inner.Record(ctx, in, refs)
	if err == nil && out != nil && out.FindingID > 0 && in.TaskID > 0 && r.after != nil {
		r.after(in.TaskID, out.FindingID)
	}
	return out, err
}

// taskFindingRecorder is the recorder wired into task planner/worker/main agents.
func (s *Server) taskFindingRecorder() agent.FindingRecorder {
	return verifyingRecorder{inner: s.evidenceStore(), after: s.queueCodeReviewVerification}
}

// queueCodeReviewVerification starts verification for findings of code review tasks
// only. It runs in the background on the server context so the reporting worker's
// tool call returns at once.
func (s *Server) queueCodeReviewVerification(taskID, findingID int64) {
	if s.m == nil || s.m.pg == nil {
		return
	}
	lang := taskLanguage(s.m.pg, strconv.FormatInt(taskID, 10))
	go func() {
		ctx := locale.WithLang(s.ctx, lang)
		// Only tasks launched by /code-review carry the recorded target; inferred
		// metadata (taskCodeReview's fallback) never triggers verification.
		target, err := s.m.pg.GetTaskCodeTarget(taskID)
		if err != nil {
			log.Printf("[codeverify] task %d lookup: %v", taskID, err)
			return
		}
		if target == nil {
			return
		}
		// Manual by default: the operator picks which findings to verify unless
		// the task's switch turns automatic verification on.
		if auto, err := s.m.pg.GetTaskAutomation(taskID); err != nil || !auto.Verify {
			return
		}
		if _, _, err := s.startFindingVerification(ctx, findingID, false); err != nil {
			log.Printf("[codeverify] finding %d: %v", findingID, err)
		}
	}()
}

// startFindingVerification creates the verify run and, when newly created, runs its
// conversation once a parallel slot is free. manual is an operator's request, which
// may verify a finding again; the automatic path verifies each finding once.
func (s *Server) startFindingVerification(ctx context.Context, findingID int64, manual bool) (*db.FindingRetest, bool, error) {
	if err := s.findingBudgetCheck(findingID); err != nil {
		return nil, false, err
	}
	if err := s.agentReady(ctx, db.CodeVerifierAgentKey, codeVerifierTools); err != nil {
		return nil, false, err
	}
	create := s.m.pg.CreateFindingVerification
	if manual {
		create = s.m.pg.CreateManualFindingVerification
	}
	run, conv, created, err := create(ctx, findingID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil // Finding deleted before verification started.
	}
	if err != nil || !created {
		return run, false, err
	}
	lang := locale.FromContext(ctx)
	s.runFindingConversation(conv, run.VerificationMessage(lang), lang, func() { s.afterFindingVerification(run.ID, lang) })
	return run, true, nil
}

// afterFindingVerification starts the fix of a finding the verifier confirmed, but
// only when the task's automatic mitigation switch is on.
func (s *Server) afterFindingVerification(runID int64, lang locale.Lang) {
	ctx := locale.WithLang(s.ctx, lang)
	run, err := s.m.pg.GetFindingRetest(ctx, runID)
	if err != nil || run == nil || run.Status != "completed" || run.Verdict != "confirmed" {
		return
	}
	f, err := s.m.pg.GetFinding(run.FindingID)
	if err != nil || f == nil || f.TaskID == nil {
		return
	}
	if auto, err := s.m.pg.GetTaskAutomation(*f.TaskID); err != nil || !auto.Mitigate {
		return
	}
	if _, _, err := s.startFindingMitigation(ctx, run.FindingID, false); err != nil {
		log.Printf("[codefix] auto fix of finding %d: %v", run.FindingID, err)
	}
}

// findingVerificationTools derive the finding from the server-owned conversation
// binding; arguments cannot redirect a verdict to another finding.
func (s *Server) findingVerificationTools(langs ...locale.Lang) []actool.CoreTool {
	lang := findingToolLanguage(langs)
	return []actool.CoreTool{
		roTool("get_finding_verification_context", locale.Text(lang, "Read the code-review finding linked to this verification conversation: its summary, evidence and report, the code review task goal, repository, ref, and absolute local clone path. Takes no arguments and can read only this conversation."),
			objSchema(map[string]any{}), func(ctx context.Context, _ json.RawMessage) (actool.Result, error) {
				r, err := s.m.pg.FindingRetestForConversation(ctx, intercept.ConvIDFromContext(ctx))
				if err != nil {
					return actool.Errorf(locale.ErrorMessage(locale.FromContext(ctx), err)), nil
				}
				if r == nil || (r.Kind != db.RetestKindVerify && r.Kind != db.RetestKindRecheck) {
					return actool.Errorf(locale.Text(locale.FromContext(ctx), "This conversation has no verification record; verifications start automatically for code review findings")), nil
				}
				out := map[string]any{"verification": r}
				noteFindingRunEnded(ctx, out, r)
				f, err := s.m.pg.GetFinding(r.FindingID)
				if err != nil {
					return actool.Errorf(locale.ErrorMessage(locale.FromContext(ctx), err)), nil
				}
				if f != nil && f.TaskID != nil {
					if task, ok := s.m.Task(strconv.FormatInt(*f.TaskID, 10)); ok {
						out["code_review"] = s.taskCodeReview(task)
						out["task_goal"] = task.Goal
						if p := codeReviewClonePath(task.Goal); p != "" {
							out["clone_path"] = p
							if r.Kind == db.RetestKindRecheck {
								// A re-check reads the merged code, not the reviewed clone.
								out["clone_path"] = recheckWorktreePath(p, r.ID)
								out["recheck"] = s.recheckContext(ctx, r)
							}
						}
					}
				}
				return jsonResult(out)
			}),
		wrTool("record_finding_verification_result", locale.Text(lang, "Save the sole verdict of this independent verification. confirmed marks the finding confirmed, rejected marks it false positive, needs_validation leaves it pending for a human. The original finding evidence and report are preserved. Supply the locations you re-read and the basis for the verdict."),
			objSchema(map[string]any{
				"verdict":  map[string]any{"type": "string", "enum": []string{"confirmed", "needs_validation", "rejected"}},
				"summary":  strParam(locale.Text(lang, "One-line verification conclusion")),
				"evidence": strParam(locale.Text(lang, "Markdown: file:line locations re-read, controls checked, and the basis for the verdict; for needs_validation the exact blocker and a safe validation plan; for confirmed the smallest fix and a regression test")),
			}, "verdict", "summary", "evidence"), func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
				var a struct {
					Verdict  string `json:"verdict"`
					Summary  string `json:"summary"`
					Evidence string `json:"evidence"`
				}
				if err := json.Unmarshal(in, &a); err != nil {
					return actool.Errorf(locale.ErrorMessage(locale.FromContext(ctx), err)), nil
				}
				if err := s.m.pg.RecordFindingVerificationResult(ctx, intercept.ConvIDFromContext(ctx), a.Verdict, a.Summary, a.Evidence); err != nil {
					return actool.Errorf(locale.ErrorMessage(locale.FromContext(ctx), err)), nil
				}
				return jsonResult(map[string]any{"saved": true, "verdict": a.Verdict})
			}),
	}
}

// seedCodeVerifier seeds the tools and the editable agent once, like the retester.
func (s *Server) seedCodeVerifier() error {
	return s.seedFindingAgent(findingAgentSeed{
		flag: "code_verifier_seed_v1", key: db.CodeVerifierAgentKey, tools: s.findingVerificationTools(),
		name:   "Code review verifier",
		desc:   "Started automatically for each code review finding; independently tries to refute it from the cloned source and saves confirmed, needs_validation or rejected.",
		prompt: agent.CodeVerifierDefaultPrompt,
	})
}

type findingAgentSeed struct {
	flag, key, name, desc, prompt string
	tools                         []actool.CoreTool
}

// seedFindingAgent seeds an agent's tools (bound to it alone) on every start and
// creates the editable agent with its default prompt once; later edits survive.
func (s *Server) seedFindingAgent(seed findingAgentSeed) error {
	for _, t := range seed.tools {
		schema, _ := json.Marshal(t.InputSchema())
		bindings, _ := json.Marshal([]string{seed.key})
		if err := s.m.pg.SeedTool(t.Name(), t.Description(), schema, bindings); err != nil {
			return err
		}
	}
	tx, err := s.m.pg.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`SELECT pg_advisory_xact_lock(7337741011)`); err != nil {
		return err
	}
	var done string
	err = tx.QueryRow(`SELECT value FROM settings WHERE key=$1`, seed.flag).Scan(&done)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if done == "true" {
		return nil
	}
	var id int64
	err = tx.QueryRow(`INSERT INTO agents(key,name,description,role,builtin,enabled)
	VALUES ($1,$2,$3,'assistant',false,true)
	ON CONFLICT (key) DO NOTHING RETURNING id`, seed.key, locale.Text(locale.En, seed.name), locale.Text(locale.En, seed.desc)).Scan(&id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if id > 0 {
		var pid int64
		if err = tx.QueryRow(`INSERT INTO agent_prompts(agent_id,version,template_text,note,updated_by)
		VALUES ($1,1,$2,$3,'system') RETURNING id`, id, seed.prompt, locale.Text(locale.En, "Built-in default")).Scan(&pid); err != nil {
			return err
		}
		if _, err = tx.Exec(`UPDATE agents SET current_prompt_id=$1 WHERE id=$2`, pid, id); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`INSERT INTO settings(key,value) VALUES ($1,'true') ON CONFLICT(key) DO UPDATE SET value='true'`, seed.flag); err != nil {
		return err
	}
	return tx.Commit()
}

// agentReady checks that a finding-run agent can start: enabled, its tools bound,
// and a chat model available.
func (s *Server) agentReady(ctx context.Context, key string, tools []string) error {
	a, err := s.m.pg.GetAgentByKey(key)
	if err != nil {
		return err
	}
	if a == nil || !a.Enabled {
		return locale.Errorf("agent %s is missing or disabled", key)
	}
	for _, name := range tools {
		t, err := s.m.pg.GetTool(name)
		if err != nil {
			return err
		}
		if t == nil || !t.Enabled || !slices.Contains(t.Agents, a.Key) {
			return locale.Errorf("tool %s is not enabled and bound to the %s agent", name, key)
		}
	}
	if s.resolveChatAgent(&db.Conversation{AgentKey: a.Key}) == nil {
		return errors.New(s.chatUnavailableReason(locale.FromContext(ctx)))
	}
	return nil
}

// runFindingConversation marks the conversation busy at once and runs its first
// turn when a code-review slot is free, then calls after (if any).
func (s *Server) runFindingConversation(conv *db.Conversation, msg string, lang locale.Lang, after func()) {
	busyKey := s.convBusyKey(conv.ID)
	s.chatMu.Lock()
	s.chatBusy[busyKey] = true
	s.chatMu.Unlock()
	go func() {
		select {
		case codeVerifySlots <- struct{}{}:
		case <-s.ctx.Done():
			s.chatMu.Lock()
			delete(s.chatBusy, busyKey)
			s.chatMu.Unlock()
			return // RecoverFindingRetests seals the pending row on the next start.
		}
		// A run stopped while it waited for a slot is already sealed: skip it
		// rather than spend the slot on a turn nobody wants.
		if run, err := s.m.pg.FindingRetestForConversation(context.Background(), conv.ID); err == nil && run != nil && run.Status != "pending" {
			<-codeVerifySlots
			s.chatMu.Lock()
			delete(s.chatBusy, busyKey)
			s.chatMu.Unlock()
			return
		}
		s.runConversationSync(conv, msg, busyKey, lang)
		<-codeVerifySlots
		if after != nil && s.ctx.Err() == nil {
			after()
		}
	}()
}

// codeReviewClonePath extracts the absolute clone path that composeCodeReviewGoal
// embeds in the task goal, or "" when the goal has another shape.
func codeReviewClonePath(goal string) string {
	const start, end = "cloned locally to ", ". Review mode: "
	i := strings.Index(goal, start)
	if i < 0 {
		return ""
	}
	rest := goal[i+len(start):]
	j := strings.Index(rest, end)
	if j <= 0 {
		return ""
	}
	return rest[:j]
}
