package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Philocyber/counterweight/report"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/Philocyber/counterweight/agent"
	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/execution"
	"github.com/Philocyber/counterweight/intercept"
	"github.com/Philocyber/counterweight/locale"
	"github.com/Philocyber/counterweight/privatefs"
)

const settingIsolation = "isolation"

func (e *Engine) isolationOn() bool {
	return e.isolationMode() != "off"
}

func (e *Engine) isolationMode() string {
	if e.m == nil || e.m.pg == nil {
		return "off"
	}
	value, _, err := e.m.pg.GetSetting(settingIsolation)
	if err != nil {
		return execution.Container
	} // Unreadable configuration cannot enable host execution.
	mode, err := execution.ParseMode(value)
	if err != nil {
		return execution.Container
	}
	return mode
}

// The nested mux extends settings without rewriting the shared settings handler.
// Only settings JSON is buffered; streaming routes go directly to the old mux.
func (s *Server) registerExecutionRoutes(previous *http.ServeMux) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("/", previous)
	mux.HandleFunc("GET /api/execution/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, s.engine.executionStatus())
	})
	mux.HandleFunc("POST /api/execution/recheck", func(w http.ResponseWriter, r *http.Request) {
		s.engine.execution.Recheck()
		writeJSON(w, 200, s.engine.executionStatus())
	})
	settings := func(w http.ResponseWriter, r *http.Request) {
		var isolation *string
		if r.Method == http.MethodPut {
			raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if err != nil {
				writeError(w, 400, err)
				return
			}
			var fields map[string]json.RawMessage
			if err = json.Unmarshal(raw, &fields); err != nil {
				writeError(w, 400, err)
				return
			}
			if field, ok := fields[settingIsolation]; ok {
				isolation, err = parseIsolationSetting(field)
				if err != nil {
					writeError(w, 400, err)
					return
				}
				delete(fields, settingIsolation)
			}
			if field, ok := fields["isolation_mode"]; ok {
				isolation, err = parseIsolationSetting(field)
				if err != nil {
					writeError(w, 400, err)
					return
				}
				delete(fields, "isolation_mode")
			}
			if isolation != nil && *isolation == execution.Container {
				s.engine.execution.Recheck()
				status := s.engine.execution.ModeStatus(execution.Container)
				if status.Status != "ready" {
					writeError(w, 409, errors.New(status.Reason))
					return
				}
			}
			raw, _ = json.Marshal(fields)
			r = r.Clone(r.Context())
			r.Body = io.NopCloser(bytes.NewReader(raw))
		}
		rec := httptest.NewRecorder()
		previous.ServeHTTP(rec, r)
		if rec.Code >= 200 && rec.Code < 300 && isolation != nil {
			if err := s.m.pg.SetSetting(settingIsolation, persistedIsolationMode(*isolation)); err != nil {
				writeError(w, 500, err)
				return
			}
		}
		for key, values := range rec.Header() {
			w.Header()[key] = values
		}
		if rec.Code >= 200 && rec.Code < 300 {
			var payload map[string]any
			if json.Unmarshal(rec.Body.Bytes(), &payload) == nil {
				payload[settingIsolation] = s.engine.isolationOn()
				payload["isolation_mode"] = s.engine.isolationMode()
				writeJSON(w, rec.Code, payload)
				return
			}
		}
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
	}
	mux.HandleFunc("GET /api/settings", settings)
	mux.HandleFunc("PUT /api/settings", settings)
	return mux
}

// executionStatus reports the selected mode and names the tasks that still run
// under an earlier one: a running task keeps the mode it was admitted with
// until it ends, is paused or is resumed.
func (e *Engine) executionStatus() execution.Status {
	status := e.execution.ModeStatus(e.isolationMode())
	selected := e.isolationMode()
	if !e.isolationOn() {
		selected = execution.Host
	}
	e.executionModes.Range(func(id, mode any) bool {
		if mode != selected {
			status.EarlierModeTasks = append(status.EarlierModeTasks, id.(string))
		}
		return true
	})
	sort.Strings(status.EarlierModeTasks)
	return status
}

func taskExecutionInfo(t *Task) execution.Info {
	assumed := execution.Info{Mode: execution.Host, Assumed: true}
	if t == nil || t.Store == nil {
		return assumed
	}
	id, _ := strconv.ParseInt(t.ID, 10, 64)
	info, err := t.Store.ExecutionInfo(id)
	if err != nil {
		return assumed
	}
	if info.Mode == execution.Container {
		info.Capabilities = execution.Caps(execution.Container)
	}
	info.SourceOnly = taskAssessmentType(t) == db.AssessmentCodeReview
	if digest, err := t.Store.ExecutionImageDigest(id); err == nil {
		info.ImageDigest = digest
	}
	return info
}
func (e *Engine) holdExecution(t *Task, err error) error {
	reason := locale.Text(locale.ServerDefault(), "Task paused: execution isolation is unavailable. Recheck the backend and task roots before resuming.")
	reason += " " + err.Error()
	e.Pause(t.ID, errors.New(reason))
	id, _ := strconv.ParseInt(t.ID, 10, 64)
	if e.m != nil && e.m.pg != nil {
		if saveErr := e.m.pg.HoldExecution(id, reason); saveErr != nil {
			return saveErr
		}
	}
	t.updateLifecycle(func(s *taskLifecycleState) { s.Paused = true; s.Queued = false; s.QueuedAt = 0 })
	return errors.New(reason)
}

func (e *Engine) admitExecution(t *Task) (admissionErr error) {
	e.executionMu.Lock()
	defer func() {
		e.executionMu.Unlock()
		if admissionErr != nil {
			admissionErr = e.holdExecution(t, admissionErr)
		}
	}()
	if _, ok := e.executionModes.Load(t.ID); ok {
		return nil
	}
	on := e.isolationOn()
	mode := e.isolationMode()
	if !on {
		mode = execution.Host
	}
	if on {
		if err := e.executionReady(mode); err != nil {
			return err
		}
		roots, err := e.taskRoots(t, "planner", 0)
		if err != nil {
			return err
		}
		f, err := execution.Open(roots)
		if err != nil {
			return err
		}
		f.Close()
	}
	// Source-only review agents never run a command. Do not attest container
	// execution merely because the operator selected it globally.
	recordMode := mode
	if mode == execution.Container && taskAssessmentType(t) == db.AssessmentCodeReview {
		recordMode = execution.ConfinedFiles
	}
	if e.m != nil && e.m.pg != nil {
		id, _ := strconv.ParseInt(t.ID, 10, 64)
		digest := ""
		if recordMode == execution.Container {
			digest = e.execution.ModeStatus(mode).ImageDigest
		}
		if err := e.m.pg.AppendContainerExecutionSegment(id, recordMode, BuildVersion, digest); err != nil {
			return err
		}
		if err := e.m.pg.ClearExecutionHold(id); err != nil {
			return err
		}
	}
	e.executionModes.Store(t.ID, mode)
	return nil
}

func (e *Engine) checkExecution(t *Task) error {
	if _, ok := e.executionModes.Load(t.ID); ok {
		return nil
	}
	if !e.isolationOn() {
		return nil
	}
	if err := e.executionReady(e.isolationMode()); err != nil {
		return e.holdExecution(t, err)
	}
	roots, err := e.taskRoots(t, "planner", 0)
	if err != nil {
		return e.holdExecution(t, err)
	}
	files, err := execution.Open(roots)
	if err != nil {
		return e.holdExecution(t, err)
	}
	files.Close()
	return nil
}

// taskRoots never treats prompt text as a repository or worktree authority.
func (e *Engine) taskRoots(t *Task, key string, conversationID int64) (roots execution.Roots, err error) {
	defer func() {
		if err == nil {
			err = e.validateFileRoots(roots)
		}
	}()
	taskDir := filepath.Join(e.m.dir, "tasks", t.ID)
	if err := e.privateWorkingDirectory(taskDir); err != nil {
		return roots, err
	}
	roots.Read = append(roots.Read, draftUploadRoots(e.m.dir, t.Description)...)
	roots.WorkingDir = taskDir
	roots.Read = append(roots.Read, taskDir) // Includes task/uploads, also for code reviews.
	if taskAssessmentType(t) != db.AssessmentCodeReview && !isFindingRunAgent(key) {
		roots.Write = append(roots.Write, taskDir)
		return roots, nil
	}
	id, _ := strconv.ParseInt(t.ID, 10, 64)
	target, err := e.m.pg.GetTaskCodeTarget(id)
	if err != nil {
		return roots, err
	}
	if isFindingRunAgent(key) && target != nil {
		run, err := e.m.pg.FindingRetestForConversation(context.Background(), conversationID)
		if err != nil {
			return roots, err
		}
		if run != nil {
			finding, err := e.m.pg.GetFinding(run.FindingID)
			if err != nil {
				return roots, err
			}
			resolved, err := (&Server{m: e.m}).findingCodeReviewTarget(finding)
			if err != nil {
				return roots, err
			}
			target.LocalPath = resolved.clonePath
		}
	}
	if target == nil {
		if taskAssessmentType(t) == db.AssessmentCodeReview {
			return roots, errors.New("repository root unavailable")
		}
		if !isFindingRunAgent(key) || findingFilesWritable(key) {
			roots.Write = append(roots.Write, taskDir)
		}
		return roots, nil
	}
	if target.LocalPath == "" {
		return roots, errors.New("repository root unavailable")
	}
	roots.Read = append(roots.Read, target.LocalPath)
	if key == db.CodeFixerAgentKey || key == db.PatchReviewerAgentKey {
		mitigation, run, err := e.m.pg.MitigationForConversation(context.Background(), conversationID)
		if err != nil || mitigation == nil || mitigation.Run == nil {
			return roots, errors.New("fix root unavailable")
		}
		worktree := fixWorktreePath(target.LocalPath, mitigation.Run.ID)
		roots.Read = append(roots.Read, worktree)
		roots.WorkingDir = worktree
		if key == db.CodeFixerAgentKey && run != nil && run.Kind == db.RetestKindMitigate && run.Status == "running" {
			roots.Write = append(roots.Write, worktree)
		}
	}
	if key == db.CodeVerifierAgentKey && conversationID > 0 {
		run, err := e.m.pg.FindingRetestForConversation(context.Background(), conversationID)
		if err != nil {
			return roots, err
		}
		if run != nil && run.Kind == db.RetestKindRecheck {
			roots.Read = append(roots.Read, recheckWorktreePath(target.LocalPath, run.ID))
		}
	}
	return roots, nil
}

func (e *Engine) wireFileAccess() {
	if e.m == nil || e.m.pg == nil {
		return
	}
	report.ExecutionLookup = func(id string) execution.Info { t, _ := e.m.Task(id); return taskExecutionInfo(t) }
	if e.execution != nil {
		e.m.registerContainerExecution(e.execution)
		// A test binary never probes the machine's real Docker engine: with the task
		// image present that would start a real container for every engine a test
		// builds. Tests that need the backend configure it with their own runner.
		if testing.Testing() {
			return
		}
		e.execution.ConfigureDocker(execution.DockerOptions{Scope: e.m.dir, DataDir: e.m.dir})
	}
}

func (e *Engine) fileAccessFailed(ctx context.Context, err error) {
	id := agent.RunInfoFrom(ctx).TaskID
	if id == 0 {
		if run, e2 := e.m.pg.FindingRetestForConversation(ctx, intercept.ConvIDFromContext(ctx)); e2 == nil && run != nil {
			if f, e3 := e.m.pg.GetFinding(run.FindingID); e3 == nil && f != nil && f.TaskID != nil {
				id = *f.TaskID
			}
		}
	}
	if t, ok := e.m.Task(strconv.FormatInt(id, 10)); ok {
		_ = e.holdExecution(t, err)
	}
}

func (e *Engine) fileRoots(ctx context.Context, key string) (execution.Roots, bool, error) {
	ri := agent.RunInfoFrom(ctx)
	id := ri.TaskID
	convID := intercept.ConvIDFromContext(ctx)
	// A conversation outside a task is confined to its own directory, whether an
	// operator opened it or a trigger started it from task data. Only finding-run
	// conversations are task principals; arbitrary conversation task links are not.
	if id == 0 && convID > 0 && !isFindingRunAgent(key) {
		if !e.isolationOn() {
			return execution.Roots{}, false, nil
		}
		roots, err := e.conversationRoots(convID, key)
		return roots, true, err
	}
	if id == 0 && isFindingRunAgent(key) {
		run, err := e.m.pg.FindingRetestForConversation(ctx, convID)
		if err != nil || run == nil {
			if !e.isolationOn() {
				return execution.Roots{}, false, nil
			}
			if err == nil && convID > 0 {
				roots, err := e.conversationRoots(convID, key)
				return roots, true, err
			}
			return execution.Roots{}, true, errors.New("task execution identity unavailable")
		}
		finding, err := e.m.pg.GetFinding(run.FindingID)
		if err != nil || finding == nil {
			if !e.isolationOn() {
				return execution.Roots{}, false, nil
			}
			return execution.Roots{}, true, errors.New("task execution identity unavailable")
		}
		if finding.TaskID == nil {
			if !e.isolationOn() {
				return execution.Roots{}, false, nil
			}
			roots, err := e.conversationRoots(convID, key)
			return roots, true, err
		}
		id = *finding.TaskID
	}
	if id == 0 {
		return execution.Roots{}, false, nil
	}
	t, ok := e.m.Task(strconv.FormatInt(id, 10))
	if !ok {
		return execution.Roots{}, true, errors.New("task execution identity unavailable")
	}
	if err := e.admitExecution(t); err != nil {
		return execution.Roots{}, true, err
	}
	value, ok := e.executionModes.Load(t.ID)
	if !ok {
		return execution.Roots{}, true, errors.New("task execution mode unavailable")
	}
	if value.(string) == execution.Host {
		return execution.Roots{}, false, nil
	}
	if e.execution == nil || e.execution.ModeStatus(value.(string)).Status != "ready" {
		return execution.Roots{}, true, e.holdExecution(t, errors.New("self-test failed"))
	}
	roots, err := e.taskRoots(t, key, convID)
	if value.(string) == execution.Container {
		roots.Mode = execution.Container
		roots.TaskID = t.ID
		roots.Workspace = filepath.Join(e.m.dir, "tasks", t.ID)
		roots.Backend = e.execution.Docker()
		roots.CommandRead = []string{}
		for _, p := range roots.Read {
			rel, e2 := filepath.Rel(filepath.Join(e.m.dir, "drafts"), p)
			if e2 == nil && filepath.IsLocal(rel) {
				continue
			}
			roots.CommandRead = append(roots.CommandRead, p)
		}
		roots.CommandWrite = append([]string{}, roots.Write...)
	}
	if err == nil {
		var files *execution.Files
		files, err = execution.Open(roots)
		if err == nil {
			files.Close()
		}
	}
	if err != nil {
		return roots, true, e.holdExecution(t, err)
	}
	return roots, true, nil
}

// Only upload directories explicitly referenced by the operator's persisted
// description are granted; a description cannot grant arbitrary host roots.
func draftUploadRoots(dataDir, description string) []string {
	prefix := filepath.Join(dataDir, "drafts") + string(filepath.Separator)
	re := regexp.MustCompile(regexp.QuoteMeta(prefix) + `([A-Za-z0-9_-]+)[/\\]uploads[/\\]`)
	seen := map[string]bool{}
	var roots []string
	for _, match := range re.FindAllStringSubmatch(description, -1) {
		if !safeChatID.MatchString(match[1]) || strings.Contains(match[1], "..") {
			continue
		}
		p := filepath.Join(dataDir, "drafts", match[1], "uploads")
		if !seen[p] {
			roots = append(roots, p)
			seen[p] = true
		}
	}
	return roots
}

// privateWorkingDirectory rejects escapes before creating private owned storage.
func (e *Engine) privateWorkingDirectory(dir string) error {
	rel, err := filepath.Rel(e.m.dir, dir)
	if err != nil || !filepath.IsLocal(rel) || rel == "." {
		return errors.New(locale.Text(locale.ServerDefault(), "working directory escapes the data directory"))
	}
	if err := privatefs.ProtectDirectory(dir); err != nil {
		return fmt.Errorf(locale.Text(locale.ServerDefault(), "working directory could not be created or opened: %w"), err)
	}
	return nil
}

func findingFilesWritable(key string) bool {
	return key == db.FindingRetestAgentKey || key == db.CodeFixerAgentKey || key == "reporter"
}

// Persisted paths must remain inside the data directory, including link targets.
func (e *Engine) validateFileRoots(roots execution.Roots) error {
	dataDir, err := filepath.EvalSymlinks(e.m.dir)
	if err != nil {
		return err
	}
	for _, group := range [][]string{roots.Read, roots.Write} {
		for _, path := range group {
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil {
				return fmt.Errorf(locale.Text(locale.ServerDefault(), "file root could not be opened: %w"), err)
			}
			rel, err := filepath.Rel(dataDir, resolved)
			if err != nil || !filepath.IsLocal(rel) || rel == "." {
				return errors.New(locale.Text(locale.ServerDefault(), "file root escapes the data directory"))
			}
			info, err := os.Stat(resolved)
			if err != nil {
				return err
			}
			if !info.IsDir() {
				return errors.New(locale.Text(locale.ServerDefault(), "file root is not a directory"))
			}
		}
	}
	return nil
}

func (e *Engine) conversationRoots(conversationID int64, key string) (execution.Roots, error) {
	if e.execution == nil || e.execution.ModeStatus(e.isolationMode()).Status != "ready" {
		return execution.Roots{}, errors.New("self-test failed")
	}
	dir := filepath.Join(e.m.dir, "sessions", "conv-"+strconv.FormatInt(conversationID, 10))
	if err := e.privateWorkingDirectory(dir); err != nil {
		return execution.Roots{}, err
	}
	roots := execution.Roots{Read: []string{dir}, WorkingDir: dir}
	// Source readers get no write root; any other conversation owns its directory.
	if findingFilesWritable(key) || !isFindingRunAgent(key) && key != agent.CodeReviewChatAgentKey {
		roots.Write = []string{dir}
	}
	if e.isolationMode() == execution.Container {
		roots.Mode = execution.Container
		roots.TaskID = "conv-" + strconv.FormatInt(conversationID, 10)
		roots.Workspace = dir
		roots.Backend = e.execution.Docker()
		roots.CommandRead = []string{dir}
		roots.CommandWrite = append([]string{}, roots.Write...)
	}
	return roots, e.validateFileRoots(roots)
}

// Configured skills belong to this server, rather than the last assembled server.
func (s *Server) fileRoots(ctx context.Context, key string) (execution.Roots, bool, error) {
	e := s.engine
	if e == nil {
		e = NewEngine(s.m)
	}
	roots, on, err := e.fileRoots(ctx, key)
	if on && err == nil && s.skillDir != "" {
		skillDir, absErr := filepath.Abs(s.skillDir)
		if absErr != nil {
			return roots, on, absErr
		}
		roots.Read = append(roots.Read, skillDir)
	}
	return roots, on, err
}

func (s *Server) fileAccessFailed(ctx context.Context, err error) {
	e := s.engine
	if e == nil {
		e = NewEngine(s.m)
	}
	e.fileAccessFailed(ctx, err)
}
