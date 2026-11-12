package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/Philocyber/counterweight/agent"
	"github.com/Philocyber/counterweight/subscription"
)

func (s *Server) configureSubscription(cfg *agent.Config, id string) error {
	if id == "" {
		return nil
	}
	if s.subscriptions == nil {
		return errors.New("ChatGPT subscription connections are unavailable")
	}
	account, err := s.subscriptions.Account(id)
	if err != nil {
		return err
	}
	if !account.Connected {
		return errors.New("ChatGPT is disconnected. Sign in again under System > LLM")
	}
	if cfg.CyberAccessProgram != "" && cfg.CyberAccessProgram != "standard" && cfg.CyberAccessProgram != "daybreakBlue" && cfg.CyberAccessProgram != "daybreakRed" {
		return errors.New("Invalid Codex access program")
	}
	if cfg.Proxy != "" || cfg.SessionHeaderKey != "" || cfg.ThinkingType != "" || cfg.MaxTokens > 0 {
		return errors.New("Native Codex manages transport, thinking, and output limits. Clear proxy, session header, thinking type, and output limit for this connection")
	}
	if cfg.Provider() != "openai-responses" || (cfg.BaseURL != "" && cfg.BaseURL != subscription.APIBase) || !cfg.Stream {
		return errors.New("Native Codex requires Responses format and streaming")
	}
	cfg.BaseURL = subscription.APIBase
	cfg.APIKey = ""
	cfg.SubscriptionID = id
	cfg.SubscriptionNative = s.subscriptions
	return nil
}
func (s *Server) registerSubscriptionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/llm/subscription/accounts", s.subscriptionAccounts)
	mux.HandleFunc("POST /api/llm/subscription/start", s.subscriptionStart)
	mux.HandleFunc("GET /api/llm/subscription/attempts/{id}", s.subscriptionStatus)
	mux.HandleFunc("DELETE /api/llm/subscription/attempts/{id}", s.subscriptionCancel)
}
func (s *Server) subscriptionAccounts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	accounts, err := s.subscriptions.Accounts()
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"accounts": accounts})
}
func (s *Server) subscriptionStart(w http.ResponseWriter, r *http.Request) {
	// The OAuth callback is on this backend's loopback interface. Remote browsers
	// must reach that same loopback (for example using an SSH tunnel).
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host != "localhost" && (net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback()) {
		writeErr(w, 400, "Open Counterweight through localhost on the backend host to connect ChatGPT. Remote installations need a loopback tunnel")
		return
	}
	var body struct {
		SubscriptionID string `json:"subscription_id"`
	}
	if !decodeAuthRequest(w, r, &body) {
		return
	}
	ctx := s.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	attempt, err := s.subscriptions.Start(ctx, body.SubscriptionID)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, attempt)
}
func (s *Server) subscriptionStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	attempt, err := s.subscriptions.Status(r.PathValue("id"))
	if err != nil {
		writeError(w, 404, err)
		return
	}
	writeJSON(w, 200, attempt)
}
func (s *Server) subscriptionCancel(w http.ResponseWriter, r *http.Request) {
	if err := s.subscriptions.Cancel(r.PathValue("id")); err != nil {
		writeError(w, 404, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
