package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/locale"
	"github.com/Philocyber/counterweight/recontools"
)

// HTTP endpoints for the optional recon-tools installer (subfinder/httpx/naabu/dnsx).
//
// Mirrors update.go's shape (hub + SSE progress + background goroutine) at a
// smaller scale: one hub entry per tool key instead of one global hub, since
// installing httpx and installing naabu are unrelated and shouldn't block
// each other, and there is no process restart at the end — once the binary is
// renamed into place it's immediately usable.

// reconToolProgress is one progress event sent to the frontend for one tool.
type reconToolProgress struct {
	Tool    string `json:"tool"`
	Phase   string `json:"phase"` // downloading | verifying | extracting | done | failed
	Percent int    `json:"percent"`
	Message string `json:"message"`
	Version string `json:"version,omitempty"`
	Error   string `json:"error,omitempty"`
}

// reconToolHub tracks install progress per tool key and broadcasts to SSE
// subscribers of that key. running also provides exclusion: a second install
// request for the same tool while one is in flight gets 409.
type reconToolHub struct {
	mu      sync.Mutex
	running map[string]bool
	cur     map[string]reconToolProgress
	subs    map[string]map[chan reconToolProgress]struct{}
}

var reconHub = &reconToolHub{
	running: map[string]bool{},
	cur:     map[string]reconToolProgress{},
	subs:    map[string]map[chan reconToolProgress]struct{}{},
}

func (h *reconToolHub) begin(tool string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.running[tool] {
		return false
	}
	h.running[tool] = true
	h.cur[tool] = reconToolProgress{Tool: tool, Phase: "downloading", Percent: 0, Message: "Preparing…"}
	h.fanout(tool, h.cur[tool])
	return true
}

func (h *reconToolHub) publish(tool, phase string, pct int, msg string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cur[tool] = reconToolProgress{Tool: tool, Phase: phase, Percent: pct, Message: msg}
	h.fanout(tool, h.cur[tool])
}

// finish ends an install. A non-empty err means it failed; otherwise version
// (possibly empty) is the detected version string.
func (h *reconToolHub) finish(tool, version string, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.running[tool] = false
	if err != nil {
		h.cur[tool] = reconToolProgress{Tool: tool, Phase: "failed", Percent: -1, Message: "Install failed", Error: err.Error()}
	} else {
		h.cur[tool] = reconToolProgress{Tool: tool, Phase: "done", Percent: 100, Message: "Installed", Version: version}
	}
	h.fanout(tool, h.cur[tool])
}

func (h *reconToolHub) fanout(tool string, p reconToolProgress) {
	for ch := range h.subs[tool] {
		select {
		case ch <- p:
		default:
		}
	}
}

func (h *reconToolHub) snapshot(tool string) (reconToolProgress, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cur[tool], h.running[tool]
}

func (h *reconToolHub) subscribe(tool string) (<-chan reconToolProgress, func()) {
	ch := make(chan reconToolProgress, 32)
	h.mu.Lock()
	if h.subs[tool] == nil {
		h.subs[tool] = map[chan reconToolProgress]struct{}{}
	}
	h.subs[tool][ch] = struct{}{}
	h.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subs[tool], ch)
			h.mu.Unlock()
			close(ch)
		})
	}
}

// reconToolsStatus reports each cataloged tool's install state: not installed,
// installed and managed by Counterweight (under dataDir/tools/bin), or found on PATH
// (an operator installed it themselves — used either way, per Counterweight's
// existing "declare if present" Tools convention).
func (s *Server) reconToolsStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"tools": recontools.StatusAll(s.dataDir)})
}

// reconToolsInstall starts a background download+verify+install for one
// cataloged tool and returns 202 immediately; progress streams over
// /api/recontools/stream?tool=<key>.
func (s *Server) reconToolsInstall(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Tool string `json:"tool"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, 400, locale.Text(locale.FromRequest(r), "Invalid request body"))
		return
	}
	spec, ok := recontools.Find(in.Tool)
	if !ok {
		writeErr(w, 400, locale.Text(locale.FromRequest(r), "Unknown tool: %s", in.Tool))
		return
	}
	if !reconHub.begin(spec.Key) {
		writeErr(w, 409, locale.Text(locale.FromRequest(r), "An install is already in progress for %s", spec.Key))
		return
	}

	client := recontools.NewClient(s.m.GlobalProxy())
	go func() {
		version, err := recontools.Install(s.ctx, client, spec, s.dataDir, func(ph recontools.Phase, pct int, msg string) {
			reconHub.publish(spec.Key, string(ph), pct, msg)
		})
		reconHub.finish(spec.Key, version, err)
		if err != nil {
			log.Printf("[recontools] install %s failed: %v", spec.Key, err)
			return
		}
		log.Printf("[recontools] %s installed (version: %s)", spec.Key, version)

		// Register as a usable shell tool so it shows up in System → Tools right
		// away — but never touch an existing row: an operator may already have a
		// custom tool at this key with their own description/agent bindings, and
		// overwriting it silently would be exactly the "blind override" this
		// project has avoided elsewhere (see docs/CAPABILITIES-IMPORT-2026-10-22.md).
		if s.m.pg != nil {
			if existing, _ := s.m.pg.GetTool(spec.Key); existing == nil {
				if err := s.m.pg.CreateCustomTool(&db.Tool{
					Key:         spec.Key,
					Description: spec.Description,
					Kind:        "shell",
					Enabled:     true,
				}); err != nil {
					log.Printf("[recontools] %s installed but could not register as a tool: %v", spec.Key, err)
				}
			}
		}
	}()

	writeJSON(w, 202, map[string]any{"ok": true, "tool": spec.Key})
}

// reconToolsStream sends one tool's install progress over SSE.
func (s *Server) reconToolsStream(w http.ResponseWriter, r *http.Request) {
	tool := r.URL.Query().Get("tool")
	spec, ok := recontools.Find(tool)
	if !ok {
		writeErr(w, 400, locale.Text(locale.FromRequest(r), "Unknown tool: %s", tool))
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, 500, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch, unsub := reconHub.subscribe(spec.Key)
	defer unsub()

	send := func(p reconToolProgress) {
		b, _ := json.Marshal(p)
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}
	// Send current state immediately so a page that opens the stream mid-install
	// (or refreshes) sees where things stand, rather than waiting for the next event.
	if cur, running := reconHub.snapshot(spec.Key); running || cur.Phase != "" {
		send(cur)
	}

	ctx := r.Context()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case p, ok := <-ch:
			if !ok {
				return
			}
			send(p)
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}
