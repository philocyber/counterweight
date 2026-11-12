package server

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	actool "github.com/Autumn-27/norma/tool"
	"github.com/Philocyber/counterweight/codesrc"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/locale"
	"github.com/Philocyber/counterweight/report"
)

// codereviewAgentKey is the built-in white-box code review chat agent (db.builtinAgents).
// It is used here only to label the launch activities' worker lane.
const codereviewAgentKey = "codereview"

// parseCodeReviewCommand recognises the chat command
//
//	/code-review <owner/repo | github URL> [light|balanced|comprehensive]
//
// It returns isCmd=true for any message whose first token is /code-review, so the
// caller can report a usage error rather than silently sending the literal command
// to the model. repo/mode are only valid when err is nil.
func parseCodeReviewCommand(msg string) (repo codesrc.Repo, mode codesrc.Mode, isCmd bool, err error) {
	cmd, err := parseCodeReviewChangeCommand(msg)
	if err != nil {
		return codesrc.Repo{}, "", cmd.IsCmd, err
	}
	return cmd.Repo, cmd.Mode, cmd.IsCmd, nil
}

// composeCodeReviewGoal is the task goal (and goal-decomposition text) for a white-box
// review. It names the repo, the local clone path the agents must read, and the depth,
// bounds the review to the source code, and points at the methodology skill. The task
// engine (planner/worker) runs it; because it is a real task, report_finding has the
// exploration context it needs and findings land under the task in the UI.
func composeCodeReviewGoal(repo codesrc.Repo, mode codesrc.Mode, path string, lang locale.Lang) string {
	ref := repo.Ref
	if ref == "" {
		ref = locale.Text(lang, "default branch")
	}
	modeDesc := map[codesrc.Mode]string{
		codesrc.ModeLight:         locale.Text(lang, "only security-critical files (auth/authorization, request handlers, input parsing, deserialization, file upload, secrets, sensitive config)"),
		codesrc.ModeBalanced:      locale.Text(lang, "the security-critical files plus their immediate callers and callees, with full source-to-sink tracing on paths carrying external input"),
		codesrc.ModeComprehensive: locale.Text(lang, "all files, first building the application-workflow threat model, then analysing against it including cross-module attack chains"),
	}[mode]

	return locale.Text(lang, "Perform a white-box security code review of repository ") + repo.Slug() +
		locale.Text(lang, " (ref: ") + ref + locale.Text(lang, "). The full source has been cloned locally to ") + path +
		locale.Text(lang, ". Review mode: ") + string(mode) + " (" + modeDesc + "). " +
		locale.Text(lang, "Review the SOURCE CODE ONLY: do not attack, scan, or send any traffic to a live host or network target. Load the whitebox-code-review-methodology skill and follow it: build the threat model, trace source-to-sink, disprove each candidate before reporting, and judge severity by the threat model. Record every confirmed vulnerability with report_finding, placing the code evidence in the evidence field as an ordered data flow where each step is file:line with a short snippet. Objective: enumerate and confirm the real security vulnerabilities in the cloned source, each with reproducible code evidence.")
}

// handleCodeReviewCommand launches an independent white-box review as a real task.
// It clones the repository (host-allowlisted, see codesrc), creates a task whose goal
// carries the clone path + mode, and starts it via the normal task engine. Progress,
// the clone result, and the started task id are posted back into the originating
// conversation as activities. Runs in the background so the HTTP handler returns at once;
// it uses the server context (not the request's, which ends when the handler returns).
func (s *Server) handleCodeReviewCommand(c *db.Conversation, repo codesrc.Repo, mode codesrc.Mode, change *codesrc.ChangeRequest, lang locale.Lang) {
	go func() {
		ctx := locale.WithLang(s.ctx, lang)

		s.appendConvActivity(c.ID, db.Activity{
			Worker:  codereviewAgentKey,
			Kind:    "text",
			Summary: locale.Text(lang, "Cloning ") + repo.Slug() + locale.Text(lang, " (mode: ") + string(mode) + ")...",
		})

		t, _, err := s.createCodeReviewChange(ctx, repo, mode, change, "", db.TaskCreateOptions{Language: lang}, false)
		if err != nil {
			s.appendConvActivity(c.ID, db.Activity{
				Worker: codereviewAgentKey, Kind: "text", IsError: true,
				Summary: locale.ErrorMessage(lang, err),
				Detail:  locale.ErrorMessage(lang, err),
			})
			return
		}

		s.launchTask(t, t.Description+" "+t.Goal, false)

		s.appendConvActivity(c.ID, db.Activity{
			Worker: codereviewAgentKey, Kind: "text",
			Summary: locale.Text(lang, "Started code review task #") + t.ID + locale.Text(lang, " for ") + repo.Slug() +
				locale.Text(lang, " (mode: ") + string(mode) + locale.Text(lang, "). Findings will appear under the task."),
		})
	}()
}

// cloneRepository is codesrc.Clone, replaceable in tests so they need no network.
var cloneRepository = codesrc.Clone

// cloneForReview clones the repository into a fresh workspace and returns the clone
// path and its exact commit. A dedicated workspace outside any single task dir:
// deterministic parent, empty leaf so codesrc.Clone can write the tree. The commit
// makes the review reproducible; failing to read it only leaves it unrecorded.
//
// The first attempt is anonymous, so a public review never presents a credential.
// If it fails and the operator has stored a GitHub token, the clone is retried
// with it: that is how a private repository is reviewed.
func (s *Server) cloneForReview(ctx context.Context, repo codesrc.Repo, mode codesrc.Mode) (path, commit string, err error) {
	dest := filepath.Join(s.m.dir, "codereview", repo.Owner+"-"+repo.Name+"-"+shortToken(), "src")
	opts := codesrc.CloneOptions{Repo: repo, Mode: mode, Dest: dest}
	if path, err = cloneRepository(ctx, opts); err != nil {
		if ctx.Err() != nil {
			return "", "", err
		}
		cred, credErr := s.loadGitHubCredential()
		if credErr != nil {
			return "", "", locale.Errorf("%w. If the repository is private, add a GitHub token with read access to it under System settings", err)
		}
		// A failed clone can leave a partial tree; the retry needs an empty destination.
		if rmErr := os.RemoveAll(dest); rmErr != nil {
			return "", "", err
		}
		opts.Token = cred.Token
		if path, err = cloneRepository(ctx, opts); err != nil {
			return "", "", err
		}
	}
	commit, _ = codesrc.HeadCommit(ctx, path)
	return path, commit, nil
}

// startCodeReviewTask creates the review task over a clone, records its target (the
// marker that turns on verification and mitigation) and starts it. instructions are
// the operator's own extra directions (a scope or a stopping point, for example);
// they are added to the goal as guidance, not enforced by the engine.
func (s *Server) startCodeReviewTask(repo codesrc.Repo, mode codesrc.Mode, path, commit, instructions string, lang locale.Lang) (*Task, error) {
	t, err := s.createCodeReviewFromClone(repo, mode, path, commit, instructions, db.TaskCreateOptions{Language: lang})
	if err != nil {
		return nil, err
	}
	// Same post-creation path as the task-create HTTP handler: seeding, background
	// goal decomposition, and engine start.
	s.launchTask(t, t.Description+" "+t.Goal, false)
	return t, nil
}

// toolStartCodeReview lets the Auto agent start a white-box review from a request in
// plain language ("review the source of owner/repo", "whitebox pentest on ..."): the
// same clone, task and marker as /code-review, so verification and mitigation apply.
// spawn_task would create a generic task without the clone or the marker.
func (s *Server) toolStartCodeReview(langs ...locale.Lang) actool.CoreTool {
	lang := locale.First(langs)
	return wrTool("start_code_review",
		locale.Text(lang, "Start a white-box security code review of a GitHub repository: clones it, creates the review task and starts it, returning task_id. Use this instead of spawn_task whenever the user asks to review, audit or white-box test source code. Pick the mode from the user's wording: light (quick, only security-critical files), balanced (default) or comprehensive (all files, full workflow). Put any other direction from the user, such as a stopping point or a focus, in instructions."),
		objSchema(map[string]any{
			"repository":   strParam(locale.Text(lang, "GitHub repository as owner/repo or a github.com URL")),
			"mode":         map[string]any{"type": "string", "enum": []string{"light", "balanced", "comprehensive"}, "description": locale.Text(lang, "Review depth; balanced when the user does not say")},
			"instructions": strParam(locale.Text(lang, "Optional extra directions from the user, in their words, for example: stop after three confirmed vulnerabilities")),
		}, "repository"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			lang := locale.FromContext(ctx)
			var a struct {
				Repository   string `json:"repository"`
				Mode         string `json:"mode"`
				Instructions string `json:"instructions"`
			}
			_ = json.Unmarshal(in, &a)
			repo, err := codesrc.ParseRepo(strings.TrimSpace(a.Repository))
			if err != nil {
				return actool.Errorf(locale.ErrorMessage(lang, err)), nil
			}
			mode, err := codesrc.ParseMode(a.Mode)
			if err != nil {
				return actool.Errorf(locale.ErrorMessage(lang, err)), nil
			}
			t, err := s.createCodeReview(ctx, repo, mode, a.Instructions, db.TaskCreateOptions{Language: lang})
			if err != nil {
				return actool.Errorf(locale.ErrorMessage(lang, err)), nil
			}
			s.launchTask(t, t.Description+" "+t.Goal, false)
			return actool.Text(fmt.Sprintf("task created: %s", t.ID)), nil
		})
}

// recordCodeReviewTarget persists a white-box review's target for reports.
// Reports degrade to inference from the task objective if this write fails.
func (s *Server) recordCodeReviewTarget(taskID string, cr report.CodeReview) {
	id, err := strconv.ParseInt(taskID, 10, 64)
	if err != nil {
		return
	}
	raw, err := json.Marshal(cr)
	if err != nil {
		return
	}
	_ = s.m.pg.SetSetting(db.TaskCodeReviewKey(id), string(raw))
}

// taskCodeReview selects the report variant from the explicit type and target.
// Only legacy, unclassified tasks may fall back to labelled text inference.
func (s *Server) taskCodeReview(t *Task) *report.CodeReview {
	kind := taskAssessmentType(t)
	if kind != db.AssessmentCodeReview && kind != db.AssessmentGeneral {
		return nil
	}
	if id, err := strconv.ParseInt(t.ID, 10, 64); err == nil {
		if target, err := s.m.pg.GetTaskCodeTarget(id); err == nil && target != nil {
			return &report.CodeReview{Repository: target.Repository, URL: target.URL, Ref: target.Ref, Commit: target.CommitSHA, Mode: target.Mode}
		}
	}
	if kind == db.AssessmentCodeReview {
		return &report.CodeReview{}
	}
	if t.lifecycleSnapshot().AssessmentTypeExplicit {
		return nil
	}
	return inferCodeReview(t.lifecycleSnapshot().Name, t.Description, t.Goal)
}

var (
	codeReviewWording = regexp.MustCompile(`(?i)\b(white[- ]?box|code[- ]review|source[- ]code (?:review|audit|assessment)|sast)\b`)
	githubRepoRef     = regexp.MustCompile(`(?i)(?:https?://)?github\.com/([A-Za-z0-9-]+/[A-Za-z0-9._-]+)`)
	bareRepoRef       = regexp.MustCompile(`\b([A-Za-z0-9-]+/[A-Za-z0-9._-]+)\b`)
)

// inferCodeReview recognises tasks created by hand as source reviews: the text
// must describe a white-box/code review and name exactly one GitHub repository.
// Ref, commit and mode are left unrecorded rather than guessed.
func inferCodeReview(texts ...string) *report.CodeReview {
	all := strings.Join(texts, "\n")
	if !codeReviewWording.MatchString(all) {
		return nil
	}
	found := map[string]bool{}
	for _, m := range githubRepoRef.FindAllStringSubmatch(all, -1) {
		found[strings.TrimSuffix(strings.TrimRight(m[1], ".,;:"), ".git")] = true // drop sentence punctuation
	}
	if len(found) == 0 {
		for _, m := range bareRepoRef.FindAllStringSubmatch(all, -1) {
			if !strings.Contains(m[1], ".") { // skip paths and domains such as a/b.c
				found[m[1]] = true
			}
		}
	}
	if len(found) != 1 {
		return nil
	}
	for slug := range found {
		repo, err := codesrc.ParseRepo(slug)
		if err != nil {
			return nil
		}
		return &report.CodeReview{Repository: repo.Slug(), URL: "https://github.com/" + repo.Slug(), Inferred: true}
	}
	return nil
}

// appendConvActivity is a logging-swallowing wrapper for the code review launch flow.
func (s *Server) appendConvActivity(convID int64, a db.Activity) {
	_, _ = s.m.pg.AppendConvActivity(convID, a)
}

// shortToken returns a short random token for the clone workspace directory name.
func shortToken() string {
	t := rand.Text()
	if len(t) > 10 {
		t = t[:10]
	}
	return strings.ToLower(t)
}
