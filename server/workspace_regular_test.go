package server

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Philocyber/counterweight/db"
)

func workspaceRequest(t *testing.T, h http.HandlerFunc, method, target string, body []byte) (int, map[string]any) {
	t.Helper()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(method, target, bytes.NewReader(body)))
		done <- rec
	}()
	select {
	case rec := <-done:
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	case <-time.After(5 * time.Second):
		t.Fatalf("%s %s did not return", method, target)
		return 0, nil
	}
}

func TestWorkspaceReadCapsBytesAndKeepsRegularFileBehaviour(t *testing.T) {
	dir := t.TempDir()
	s := &Server{m: &Manager{dir: dir}}
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), bytes.Repeat([]byte("a"), maxWorkspaceRead+10), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "small.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "folder"), 0o700); err != nil {
		t.Fatal(err)
	}
	code, out := workspaceRequest(t, s.wsRead, http.MethodGet, "/api/workspace/read?path=big.txt", nil)
	if code != 200 || out["too_large"] != true {
		t.Fatalf("big file: %d %v", code, out)
	}
	code, out = workspaceRequest(t, s.wsRead, http.MethodGet, "/api/workspace/read?path=small.txt", nil)
	if code != 200 || out["content"] != "hello" {
		t.Fatalf("small file: %d %v", code, out)
	}
	if code, _ = workspaceRequest(t, s.wsRead, http.MethodGet, "/api/workspace/read?path=folder", nil); code != 400 {
		t.Fatalf("directory read = %d, want 400", code)
	}
	if code, _ = workspaceRequest(t, s.wsRead, http.MethodGet, "/api/workspace/read?path=missing", nil); code != 404 {
		t.Fatalf("missing read = %d, want 404", code)
	}
	if code, _ = workspaceRequest(t, s.wsDownload, http.MethodGet, "/api/workspace/download?path=small.txt", nil); code != 200 {
		t.Fatalf("download = %d, want 200", code)
	}
}

func newMultipart(buf *bytes.Buffer, field, name, content string) string {
	mw := multipart.NewWriter(buf)
	fw, _ := mw.CreateFormFile(field, name)
	_, _ = fw.Write([]byte(content))
	_ = mw.Close()
	return mw.FormDataContentType()
}

func TestChatUploadRefusesPlantedLinks(t *testing.T) {
	dataDir := t.TempDir()
	outside := t.TempDir()
	s := &Server{m: &Manager{dir: dataDir}}
	upload := func() (int, map[string]any) {
		var buf bytes.Buffer
		contentType := newMultipart(&buf, "file", "note.txt", "payload")
		req := httptest.NewRequest(http.MethodPost, "/api/chat/upload?scope=session&id=conv-1", &buf)
		req.Header.Set("Content-Type", contentType)
		rec := httptest.NewRecorder()
		s.chatUpload(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	chat := filepath.Join(dataDir, "sessions", "conv-1")
	if err := os.MkdirAll(filepath.Join(chat, "uploads"), 0o755); err != nil {
		t.Fatal(err)
	}

	// A dangling link at the destination name: the file lands under another name inside the chat directory.
	if err := os.Symlink(filepath.Join(outside, "created.txt"), filepath.Join(chat, "uploads", "note.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if code, out := upload(); code != 200 {
		t.Fatalf("upload = %d %v", code, out)
	}
	if _, err := os.Lstat(filepath.Join(outside, "created.txt")); !os.IsNotExist(err) {
		t.Fatalf("upload created a file at the link target: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(chat, "uploads", "note-1.txt")); err != nil || string(got) != "payload" {
		t.Fatalf("upload did not land beside the link: %q %v", got, err)
	}

	// The uploads directory replaced by a link to another directory.
	if err := os.RemoveAll(filepath.Join(chat, "uploads")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(chat, "uploads")); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	if code, _ := upload(); code == 200 {
		t.Fatal("upload through a linked uploads directory succeeded")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("files appeared outside the chat directory: %v", entries)
	}
}

func TestKeepTaskFilesMovesWorkspaceOutOfTasksAndRollsBack(t *testing.T) {
	dataDir := t.TempDir()
	file := filepath.Join(dataDir, "tasks", "9", "uploads", "secret.txt")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	stage, kept, err := keepTaskFiles(dataDir, "9")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(kept, keptTasksDir+string(filepath.Separator)) {
		t.Fatalf("kept path %q is not under %s", kept, keptTasksDir)
	}
	if _, err := os.Lstat(filepath.Join(dataDir, "tasks", "9")); !os.IsNotExist(err) {
		t.Fatalf("task directory still in place: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, kept, "uploads", "secret.txt")); err != nil {
		t.Fatalf("kept file missing: %v", err)
	}
	if err := stage.rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("rollback did not restore the workspace: %v", err)
	}
	// A missing workspace is not an error and keeps nothing.
	stage, kept, err = keepTaskFiles(dataDir, "10")
	if err != nil || kept != "" || stage.commit() != nil {
		t.Fatalf("missing workspace: %v %q", err, kept)
	}
}

func TestDeleteTaskKeepsFilesOutsideReusedIDAndCreationMovesStaleDirectory(t *testing.T) {
	m, _ := executionFixture(t)
	first, err := m.CreateTaskWithOptions("keep files", "goal", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(m.dir, "tasks", first.ID, "old.txt")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := m.DeleteTask(first.ID, DeleteTaskOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.FilesKeptAt == "" {
		t.Fatal("kept location not reported")
	}
	if _, err := os.Stat(filepath.Join(m.dir, result.FilesKeptAt, "old.txt")); err != nil {
		t.Fatalf("kept file missing: %v", err)
	}
	again, err := m.CreateTaskWithOptions("reuse", "goal", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = m.DeleteTask(again.ID, DeleteTaskOptions{DeleteFiles: true}) }()
	if again.ID != first.ID {
		t.Skipf("id %s not reused (got %s)", first.ID, again.ID)
	}
	if _, err := os.Stat(filepath.Join(m.dir, "tasks", again.ID, "old.txt")); !os.IsNotExist(err) {
		t.Fatalf("new task sees the deleted task's file: %v", err)
	}
	// A directory left under a free id by an older delete is moved aside on creation.
	if _, err := m.DeleteTask(again.ID, DeleteTaskOptions{DeleteFiles: true}); err != nil {
		t.Fatal(err)
	}
	leftover := filepath.Join(m.dir, "tasks", again.ID, "left.txt")
	if err := os.MkdirAll(filepath.Dir(leftover), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(leftover, []byte("left"), 0o600); err != nil {
		t.Fatal(err)
	}
	third, err := m.CreateTaskWithOptions("reuse again", "goal", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = m.DeleteTask(third.ID, DeleteTaskOptions{DeleteFiles: true}) }()
	if third.ID != again.ID {
		t.Skipf("id %s not reused (got %s)", again.ID, third.ID)
	}
	if _, err := os.Stat(filepath.Join(m.dir, "tasks", third.ID, "left.txt")); !os.IsNotExist(err) {
		t.Fatalf("stale file still under the new task: %v", err)
	}
}
