package server

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Philocyber/counterweight/privatefs"
)

func TestWorkspaceRefusesIntegrationCredentialFiles(t *testing.T) {
	dir := t.TempDir()
	const secret = "credential-secret-value"
	for _, name := range []string{githubCredentialFile, jiraCredentialFile, "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(secret), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{m: &Manager{dir: dir}}
	call := func(h http.HandlerFunc, req *http.Request) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h(rec, req)
		return rec
	}
	jsonReq := func(method, target string, body any) *http.Request {
		raw, _ := json.Marshal(body)
		return httptest.NewRequest(method, target, bytes.NewReader(raw))
	}
	uploadReq := func(dirPath, name string) *http.Request {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		fw, _ := mw.CreateFormFile("file", name)
		_, _ = fw.Write([]byte("uploaded"))
		_ = mw.Close()
		req := httptest.NewRequest(http.MethodPost, "/api/workspace/upload?path="+url.QueryEscape(dirPath), &buf)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		return req
	}

	// The listing hides both credential files but keeps ordinary files.
	rec := call(s.wsList, httptest.NewRequest(http.MethodGet, "/api/workspace/list?path=", nil))
	if rec.Code != 200 {
		t.Fatalf("list=%d %s", rec.Code, rec.Body.String())
	}
	body := strings.ToLower(rec.Body.String())
	if strings.Contains(body, githubCredentialFile) || strings.Contains(body, jiraCredentialFile) || !strings.Contains(body, "notes.txt") {
		t.Fatalf("list exposed credentials or lost ordinary file: %s", rec.Body.String())
	}

	variants := []string{
		githubCredentialFile, jiraCredentialFile,
		"/" + jiraCredentialFile, "./" + githubCredentialFile, "sub/../" + jiraCredentialFile,
		strings.ToUpper(jiraCredentialFile), "GitHub-Token.JSON", jiraCredentialFile + ".", jiraCredentialFile + "::$DATA",
	}
	for _, p := range variants {
		q := url.QueryEscape(p)
		for label, rec := range map[string]*httptest.ResponseRecorder{
			"read":     call(s.wsRead, httptest.NewRequest(http.MethodGet, "/api/workspace/read?path="+q, nil)),
			"download": call(s.wsDownload, httptest.NewRequest(http.MethodGet, "/api/workspace/download?path="+q, nil)),
			"list":     call(s.wsList, httptest.NewRequest(http.MethodGet, "/api/workspace/list?path="+q, nil)),
			"write":    call(s.wsWrite, jsonReq(http.MethodPost, "/api/workspace/write", map[string]string{"path": p, "content": "overwritten"})),
			"mkdir":    call(s.wsMkdir, jsonReq(http.MethodPost, "/api/workspace/mkdir", map[string]string{"path": p})),
			"delete":   call(s.wsDelete, httptest.NewRequest(http.MethodDelete, "/api/workspace/delete?path="+q, nil)),
			"upload":   call(s.wsUpload, uploadReq(p, "x.txt")),
		} {
			if rec.Code != 400 || strings.Contains(rec.Body.String(), secret) {
				t.Errorf("%s %q: code=%d body=%s", label, p, rec.Code, rec.Body.String())
			}
		}
	}

	// Uploading a file named like a credential into the root does not create or replace it.
	for _, name := range []string{jiraCredentialFile, strings.ToUpper(githubCredentialFile)} {
		rec = call(s.wsUpload, uploadReq("", name))
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"uploaded":0`) {
			t.Errorf("upload %q: code=%d body=%s", name, rec.Code, rec.Body.String())
		}
	}

	// Credentials remain intact after every refused request.
	for _, name := range []string{githubCredentialFile, jiraCredentialFile} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(raw) != secret {
			t.Fatalf("%s changed: %q %v", name, raw, err)
		}
	}

	// A link elsewhere in the workspace that resolves to a credential is refused too.
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(dir, jiraCredentialFile), filepath.Join(dir, "sub", "copy.json")); err == nil {
		rec = call(s.wsRead, httptest.NewRequest(http.MethodGet, "/api/workspace/read?path=sub/copy.json", nil))
		if rec.Code != 400 || strings.Contains(rec.Body.String(), secret) {
			t.Errorf("hard link read: code=%d body=%s", rec.Code, rec.Body.String())
		}
		rec = call(s.wsList, httptest.NewRequest(http.MethodGet, "/api/workspace/list?path=sub", nil))
		if strings.Contains(rec.Body.String(), "copy.json") {
			t.Errorf("hard link listed: %s", rec.Body.String())
		}
	}

	// A file with the same name below the root is an ordinary file.
	if err := os.WriteFile(filepath.Join(dir, "sub", jiraCredentialFile), []byte("plain"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec = call(s.wsRead, httptest.NewRequest(http.MethodGet, "/api/workspace/read?path=sub/"+jiraCredentialFile, nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "plain") {
		t.Errorf("nested file read: code=%d body=%s", rec.Code, rec.Body.String())
	}

	// Ordinary files still work through every handler.
	if rec = call(s.wsRead, httptest.NewRequest(http.MethodGet, "/api/workspace/read?path=notes.txt", nil)); rec.Code != 200 || !strings.Contains(rec.Body.String(), secret) {
		t.Errorf("read ordinary: %d %s", rec.Code, rec.Body.String())
	}
	if rec = call(s.wsDownload, httptest.NewRequest(http.MethodGet, "/api/workspace/download?path=notes.txt", nil)); rec.Code != 200 || rec.Body.String() != secret {
		t.Errorf("download ordinary: %d %s", rec.Code, rec.Body.String())
	}
	if rec = call(s.wsWrite, jsonReq(http.MethodPost, "/api/workspace/write", map[string]string{"path": "notes.txt", "content": "new"})); rec.Code != 200 {
		t.Errorf("write ordinary: %d %s", rec.Code, rec.Body.String())
	}
	if rec = call(s.wsMkdir, jsonReq(http.MethodPost, "/api/workspace/mkdir", map[string]string{"path": "made"})); rec.Code != 200 {
		t.Errorf("mkdir ordinary: %d %s", rec.Code, rec.Body.String())
	}
	if rec = call(s.wsUpload, uploadReq("", "up.txt")); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"uploaded":1`) {
		t.Errorf("upload ordinary: %d %s", rec.Code, rec.Body.String())
	}
	if rec = call(s.wsDelete, httptest.NewRequest(http.MethodDelete, "/api/workspace/delete?path=up.txt", nil)); rec.Code != 200 {
		t.Errorf("delete ordinary: %d %s", rec.Code, rec.Body.String())
	}
}

func wsTestCall(h http.HandlerFunc, method, target string, body any) *httptest.ResponseRecorder {
	var req *http.Request
	if body != nil {
		raw, _ := json.Marshal(body)
		req = httptest.NewRequest(method, target, bytes.NewReader(raw))
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestWorkspaceRefusesCredentialStagingFiles(t *testing.T) {
	dir := t.TempDir()
	const secret = "staged-credential-secret"
	staging := privatefs.PublicationTempPrefix + "0123456789abcdef0123456789abcdef"
	if err := os.WriteFile(filepath.Join(dir, staging), []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &Server{m: &Manager{dir: dir}}
	rec := wsTestCall(s.wsList, http.MethodGet, "/api/workspace/list?path=", nil)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), privatefs.PublicationTempPrefix) {
		t.Fatalf("list exposed staging file: %d %s", rec.Code, rec.Body.String())
	}
	for _, p := range []string{staging, strings.ToUpper(staging), "/" + staging, staging + ".", staging + "/child"} {
		q := url.QueryEscape(p)
		for label, rec := range map[string]*httptest.ResponseRecorder{
			"read":     wsTestCall(s.wsRead, http.MethodGet, "/api/workspace/read?path="+q, nil),
			"download": wsTestCall(s.wsDownload, http.MethodGet, "/api/workspace/download?path="+q, nil),
			"write":    wsTestCall(s.wsWrite, http.MethodPost, "/api/workspace/write", map[string]string{"path": p, "content": "x"}),
			"delete":   wsTestCall(s.wsDelete, http.MethodDelete, "/api/workspace/delete?path="+q, nil),
			"mkdir":    wsTestCall(s.wsMkdir, http.MethodPost, "/api/workspace/mkdir", map[string]string{"path": p}),
		} {
			if rec.Code != 400 || strings.Contains(rec.Body.String(), secret) {
				t.Errorf("%s %q: %d %s", label, p, rec.Code, rec.Body.String())
			}
		}
	}
	if raw, err := os.ReadFile(filepath.Join(dir, staging)); err != nil || string(raw) != secret {
		t.Fatalf("staging file changed: %q %v", raw, err)
	}
}

func TestWorkspaceRefusesReservedFirstComponent(t *testing.T) {
	dir := t.TempDir()
	s := &Server{m: &Manager{dir: dir}}
	// No credential exists yet: the reserved names must not become directories.
	for _, p := range []string{jiraCredentialFile, strings.ToUpper(githubCredentialFile), jiraCredentialFile + ".", jiraCredentialFile + "/child", githubCredentialFile + "/a/b.txt"} {
		if rec := wsTestCall(s.wsMkdir, http.MethodPost, "/api/workspace/mkdir", map[string]string{"path": p}); rec.Code != 400 {
			t.Errorf("mkdir %q: %d %s", p, rec.Code, rec.Body.String())
		}
		if rec := wsTestCall(s.wsWrite, http.MethodPost, "/api/workspace/write", map[string]string{"path": p, "content": "x"}); rec.Code != 400 {
			t.Errorf("write %q: %d %s", p, rec.Code, rec.Body.String())
		}
	}
	for _, name := range []string{githubCredentialFile, jiraCredentialFile} {
		if _, err := os.Lstat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s was created: %v", name, err)
		}
	}

	// A directory alias that points back to the root cannot reach reserved names.
	if err := os.Symlink(dir, filepath.Join(dir, "alias")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	for _, p := range []string{"alias/" + jiraCredentialFile, "alias/" + jiraCredentialFile + "/child", "alias/alias/" + strings.ToUpper(githubCredentialFile)} {
		if rec := wsTestCall(s.wsWrite, http.MethodPost, "/api/workspace/write", map[string]string{"path": p, "content": "x"}); rec.Code != 400 {
			t.Errorf("write %q: %d %s", p, rec.Code, rec.Body.String())
		}
		if rec := wsTestCall(s.wsMkdir, http.MethodPost, "/api/workspace/mkdir", map[string]string{"path": p}); rec.Code != 400 {
			t.Errorf("mkdir %q: %d %s", p, rec.Code, rec.Body.String())
		}
	}
	for _, name := range []string{githubCredentialFile, jiraCredentialFile} {
		if _, err := os.Lstat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s was created through the alias: %v", name, err)
		}
	}
	if rec := wsTestCall(s.wsWrite, http.MethodPost, "/api/workspace/write", map[string]string{"path": "alias/ordinary.txt", "content": "x"}); rec.Code != 200 {
		t.Errorf("ordinary write through alias: %d %s", rec.Code, rec.Body.String())
	}
}
