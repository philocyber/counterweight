package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestSkillFilePathsRoundTripThroughAPI(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	s := &Server{skillDir: root}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /skills/{name}/files", s.fsListFiles)
	mux.HandleFunc("GET /skills/{name}/file/{file...}", s.fsReadFile)
	mux.HandleFunc("PUT /skills/{name}/file/{file...}", s.fsWriteFile)
	mux.HandleFunc("DELETE /skills/{name}/file/{file...}", s.fsDeletePath)
	mux.HandleFunc("POST /skills/{name}/directory", s.fsCreateDir)
	request := func(method, path, body string, status int) map[string]any {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != status {
			t.Fatalf("%s %s: status %d, body %s", method, path, rr.Code, rr.Body)
		}
		var result map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	request("PUT", "/skills/fixture/file/scripts/nested/run.py", `{"content":"print('fixture')"}`, 200)
	created := request("POST", "/skills/fixture/directory", `{"path":"references/empty"}`, 201)
	if created["dir"] != "references/empty" {
		t.Fatalf("directory path changed: %+v", created)
	}
	listed := request("GET", "/skills/fixture/files", "", 200)
	var got []string
	for _, raw := range listed["files"].([]any) {
		got = append(got, raw.(string))
	}
	sort.Strings(got)
	want := []string{"references/", "references/empty/", "scripts/", "scripts/nested/", "scripts/nested/run.py"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paths cannot build the frontend slash-delimited tree: got %q, want %q", got, want)
	}
	read := request("GET", "/skills/fixture/file/"+got[len(got)-1], "", 200)
	if read["file"] != "scripts/nested/run.py" || read["content"] != "print('fixture')" {
		t.Fatalf("listed file failed read round-trip: %+v", read)
	}
	deleted := request("DELETE", "/skills/fixture/file/scripts/nested/run.py", "", 200)
	if deleted["deleted"] != "scripts/nested/run.py" {
		t.Fatalf("deleted path changed: %+v", deleted)
	}
	request("GET", "/skills/fixture/file/scripts/nested/run.py", "", 404)
}
