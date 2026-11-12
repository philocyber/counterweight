package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Philocyber/counterweight/privatefs"
	"github.com/Philocyber/counterweight/selfupdate"
)

func TestStarPromptAsksOnceAndStarsWithStoredToken(t *testing.T) {
	s, _, request := trafficEvidenceServer(t)
	t.Cleanup(func() { _, _ = s.m.pg.Exec(`DELETE FROM settings WHERE key=$1`, settingStarPrompt) })
	_, _ = s.m.pg.Exec(`DELETE FROM settings WHERE key=$1`, settingStarPrompt)
	minTasks := starPromptMinTasks
	starPromptMinTasks = 1
	t.Cleanup(func() { starPromptMinTasks = minTasks })

	starred, puts := false, 0
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/starred/"+selfupdate.Repo || r.Header.Get("Authorization") != "Bearer test-token" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		switch r.Method {
		case http.MethodGet:
			if starred {
				w.WriteHeader(http.StatusNoContent)
			} else {
				w.WriteHeader(http.StatusNotFound)
			}
		case http.MethodPut:
			puts++
			starred = true
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(gh.Close)
	api := githubAPI
	githubAPI = gh.URL
	t.Cleanup(func() { githubAPI = api })

	view := func() starPromptView {
		t.Helper()
		w := request("GET", "/api/star-prompt", "")
		var v starPromptView
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &v) != nil {
			t.Fatalf("GET star prompt: %d %s", w.Code, w.Body.String())
		}
		return v
	}

	// Without a token the console can only link to the repository.
	if v := view(); !v.Show || v.Direct || v.URL == "" {
		t.Fatalf("no token: %+v", v)
	}
	if w := request("POST", "/api/star-prompt", `{"action":"later"}`); w.Code != 200 {
		t.Fatalf("later: %d %s", w.Code, w.Body.String())
	}
	if v := view(); v.Show {
		t.Fatalf("shown during the wait: %+v", v)
	}
	if err := s.saveStarPrompt(starPromptState{LaterUntil: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}

	// With a token the star is one click and is never requested without it.
	if err := privatefs.PublishFile(s.githubCredentialPath(), []byte(`{"token":"test-token","login":"octocat"}`)); err != nil {
		t.Fatal(err)
	}
	if v := view(); !v.Show || !v.Direct || puts != 0 {
		t.Fatalf("with token: %+v, puts=%d", v, puts)
	}
	w := request("POST", "/api/star-prompt", `{"action":"star"}`)
	var out struct {
		Starred bool `json:"starred"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || !out.Starred || puts != 1 {
		t.Fatalf("star: %d %s puts=%d", w.Code, w.Body.String(), puts)
	}
	if v := view(); v.Show {
		t.Fatalf("shown after starring: %+v", v)
	}

	// A star set outside the console also ends the prompt.
	if err := s.saveStarPrompt(starPromptState{}); err != nil {
		t.Fatal(err)
	}
	if v := view(); v.Show || !s.loadStarPrompt().Done {
		t.Fatalf("already starred: %+v", v)
	}
	if w := request("POST", "/api/star-prompt", `{"action":"other"}`); w.Code != 400 {
		t.Fatalf("unknown action: %d", w.Code)
	}
}
