package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/Philocyber/counterweight/locale"
	"github.com/Philocyber/counterweight/selfupdate"
)

// The star prompt is a one-time, dismissible invitation to star the project on
// GitHub, shown only after the installation has produced results. With a stored
// GitHub token the star is one click; otherwise the console opens the repository
// page. Nothing is sent to GitHub before the operator has stored a token, and a
// star is only ever set by an explicit click.

const (
	settingStarPrompt   = "ui.star_prompt"
	starPromptLaterWait = 14 * 24 * time.Hour
	starPromptTimeout   = 10 * time.Second
)

// starPromptMinTasks is the number of tasks an installation must have created
// before the invitation appears; tests lower it.
var starPromptMinTasks = 2

var starPromptRepoURL = "https://github.com/" + selfupdate.Repo

// starPromptState is persisted so the invitation is answered once per
// installation, not once per browser.
type starPromptState struct {
	Done       bool      `json:"done"`
	LaterUntil time.Time `json:"later_until,omitzero"`
}

func (s *Server) loadStarPrompt() starPromptState {
	var st starPromptState
	if raw, ok, _ := s.m.pg.GetSetting(settingStarPrompt); ok && raw != "" {
		_ = json.Unmarshal([]byte(raw), &st)
	}
	return st
}

func (s *Server) saveStarPrompt(st starPromptState) error {
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return s.m.pg.SetSetting(settingStarPrompt, string(raw))
}

// starPromptEarned reports whether the installation has been used enough for
// the invitation to be fair: several tasks and at least one finding.
func (s *Server) starPromptEarned() bool {
	var tasks, findings int
	if err := s.m.pg.QueryRow(`SELECT (SELECT COUNT(*) FROM tasks), (SELECT COUNT(*) FROM findings)`).Scan(&tasks, &findings); err != nil {
		return false
	}
	return tasks >= starPromptMinTasks && findings >= 1
}

// githubStarred asks GitHub whether the token's user has starred the project.
// known is false when the answer could not be established (no permission,
// network failure), in which case the caller must not assume either way.
func githubStarred(ctx context.Context, token string) (starred, known bool) {
	switch githubStarRequest(ctx, token, http.MethodGet) {
	case http.StatusNoContent:
		return true, true
	case http.StatusNotFound:
		return false, true
	}
	return false, false
}

// githubStarRequest calls /user/starred/<repo> and returns the HTTP status, or 0
// when the request did not complete.
func githubStarRequest(ctx context.Context, token, method string) int {
	ctx, cancel := context.WithTimeout(ctx, starPromptTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, githubAPI+"/user/starred/"+selfupdate.Repo, nil)
	if err != nil {
		return 0
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "Counterweight")
	resp, err := githubHTTP.Do(req)
	if err != nil {
		return 0
	}
	resp.Body.Close()
	return resp.StatusCode
}

type starPromptView struct {
	Show bool `json:"show"`
	// Direct means a stored token can set the star without leaving the console.
	Direct bool   `json:"direct"`
	URL    string `json:"url"`
}

// GET /api/star-prompt
func (s *Server) getStarPromptHandler(w http.ResponseWriter, r *http.Request) {
	view := starPromptView{URL: starPromptRepoURL}
	st := s.loadStarPrompt()
	if st.Done || time.Now().Before(st.LaterUntil) || !s.starPromptEarned() {
		writeJSON(w, 200, view)
		return
	}
	view.Show = true
	if cred, err := s.loadGitHubCredential(); err == nil && cred.Token != "" {
		starred, known := githubStarred(r.Context(), cred.Token)
		if known && starred {
			// Already starred outside the console: never ask.
			st.Done = true
			_ = s.saveStarPrompt(st)
			view.Show = false
		}
		view.Direct = known && !starred
	}
	writeJSON(w, 200, view)
}

// POST /api/star-prompt {"action":"star"|"later"|"dismiss"}
func (s *Server) postStarPromptHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); err != nil {
		writeErr(w, 400, locale.Text(responseLanguage(w), "Invalid request"))
		return
	}
	st := s.loadStarPrompt()
	out := map[string]any{"starred": false, "url": starPromptRepoURL}
	switch req.Action {
	case "star":
		// The answer is final either way: the star is set here, or the operator
		// is sent to the repository page and is not asked again.
		st.Done = true
		if cred, err := s.loadGitHubCredential(); err == nil && cred.Token != "" {
			out["starred"] = githubStarRequest(r.Context(), cred.Token, http.MethodPut) == http.StatusNoContent
		}
	case "later":
		st.LaterUntil = time.Now().Add(starPromptLaterWait)
	case "dismiss":
		st.Done = true
	default:
		writeErr(w, 400, locale.Text(responseLanguage(w), "Invalid request"))
		return
	}
	if err := s.saveStarPrompt(st); err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) registerStarPromptRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/star-prompt", s.getStarPromptHandler)
	mux.HandleFunc("POST /api/star-prompt", s.postStarPromptHandler)
}
