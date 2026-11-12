// Package subscription connects Counterweight to a local, independently authenticated Codex runtime.
package subscription

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Philocyber/counterweight/privatefs"
	"github.com/google/uuid"
)

// APIBase is retained for stored profile compatibility. Native inference uses Codex RPC.
const APIBase = "https://api.openai.com/v1"
const attemptTTL = 5 * time.Minute

type Account struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	Connected bool   `json:"connected"`
	Plan      string `json:"plan,omitempty"`
}
type Attempt struct {
	ID        string    `json:"id"`
	URL       string    `json:"url,omitempty"`
	Status    string    `json:"status"`
	Error     string    `json:"error,omitempty"`
	Account   *Account  `json:"account,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
}
type pending struct {
	Attempt
	cancel    context.CancelFunc
	client    *rpc
	loginID   string
	accountID string
}
type Manager struct {
	root     string
	mu       sync.Mutex
	accounts map[string]Account
	attempts map[string]*pending
	slots    map[string]chan struct{}
	command  func(context.Context) *exec.Cmd
	starting bool
}

func New(dir string) *Manager {
	root := filepath.Join(dir, "chatgpt-subscription", "native-codex")
	// Codex runs with its working directory set to CODEX_HOME, so a relative
	// path would be resolved against itself and not exist.
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	return &Manager{root: root,accounts: map[string]Account{}, attempts: map[string]*pending{}, slots: map[string]chan struct{}{}}
}
func (m *Manager) load() error {
	if err := os.MkdirAll(m.root, 0700); err != nil {
		return err
	}
	if err := privatefs.ProtectDirectory(m.root); err != nil {
		return err
	}
	b, err := privatefs.ReadFile(filepath.Join(m.root, "connections.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(b, &m.accounts)
}
func (m *Manager) save() error {
	b, err := json.Marshal(m.accounts)
	if err != nil {
		return err
	}
	f, err := privatefs.CreateTemp(m.root, ".connections-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(m.root, "connections.json"))
}
func (m *Manager) Accounts() ([]Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.load(); err != nil {
		return nil, err
	}
	out := []Account{}
	for _, a := range m.accounts {
		out = append(out, a)
	}
	return out, nil
}
func (m *Manager) Account(id string) (Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.load(); err != nil {
		return Account{}, err
	}
	a, ok := m.accounts[id]
	if !ok {
		return Account{}, errors.New("Reconnect this profile using Continue with ChatGPT to enable native Codex authentication")
	}
	return a, nil
}
func (m *Manager) directory(id string) (string, error) {
	if _, err := uuid.Parse(id); err != nil {
		return "", errors.New("Invalid Codex connection")
	}
	dir := filepath.Join(m.root, id)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	if err := privatefs.ProtectDirectory(dir); err != nil {
		return "", err
	}
	// Never inherit the user's Codex configuration, tools, hooks, or login session.
	config := `cli_auth_credentials_store = "file"
model_provider = "openai"
web_search = "disabled"
sandbox_mode = "read-only"
approval_policy = "never"
[features]
shell_tool = false
apply_patch_freeform = false
multi_agent = false
apps = false
plugins = false
hooks = false
remote_plugin = false
memories = false
shell_snapshot = false
[skills.bundled]
enabled = false
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(config), 0600); err != nil {
		return "", err
	}
	return dir, nil
}
func (m *Manager) acquire(ctx context.Context, id string) (func(), error) {
	m.mu.Lock()
	slot := m.slots[id]
	if slot == nil {
		slot = make(chan struct{}, 1)
		m.slots[id] = slot
	}
	m.mu.Unlock()
	select {
	case slot <- struct{}{}:
		return func() { <-slot }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (m *Manager) Start(ctx context.Context, id string) (Attempt, error) {
	m.mu.Lock()
	if err := m.load(); err != nil {
		m.mu.Unlock()
		return Attempt{}, err
	}
	for key, p := range m.attempts {
		if time.Now().After(p.ExpiresAt.Add(attemptTTL)) {
			delete(m.attempts, key)
			continue
		}
		if p.Status == "pending" || p.Status == "exchanging" {
			m.mu.Unlock()
			return Attempt{}, errors.New("A Codex sign-in is already in progress. Cancel it before starting another")
		}
	}
	if m.starting {
		m.mu.Unlock()
		return Attempt{}, errors.New("A Codex sign-in is already starting")
	}
	m.starting = true
	m.mu.Unlock()
	defer func() { m.mu.Lock(); m.starting = false; m.mu.Unlock() }()
	if id == "" {
		id = uuid.NewString()
	}
	dir, err := m.directory(id)
	if err != nil {
		return Attempt{}, err
	}
	loginCtx, cancel := context.WithTimeout(ctx, attemptTTL)
	release, err := m.acquire(loginCtx, id)
	if err != nil {
		cancel()
		return Attempt{}, err
	}
	c, err := m.launch(loginCtx, dir)
	if err != nil {
		release()
		cancel()
		return Attempt{}, err
	}
	result, err := c.call(loginCtx, "account/login/start", map[string]any{"type": "chatgpt"})
	if err != nil {
		c.close()
		release()
		cancel()
		return Attempt{}, err
	}
	var login struct {
		LoginID string `json:"loginId"`
		AuthURL string `json:"authUrl"`
	}
	if err = json.Unmarshal(result, &login); err != nil || login.AuthURL == "" {
		c.close()
		release()
		cancel()
		return Attempt{}, errors.New("Codex did not return a browser sign-in URL")
	}
	p := &pending{Attempt: Attempt{ID: uuid.NewString(), URL: login.AuthURL, Status: "pending", ExpiresAt: time.Now().Add(attemptTTL)}, cancel: cancel, client: c, loginID: login.LoginID, accountID: id}
	m.mu.Lock()
	m.attempts[p.ID] = p
	out := p.Attempt
	m.mu.Unlock()
	go func() {
		defer cancel()
		defer release()
		defer c.close()
		for {
			event, err := c.next(loginCtx)
			if err != nil {
				m.finish(p, nil, "Codex sign-in was cancelled or expired")
				return
			}
			if len(event.ID) > 0 {
				c.deny(event)
				continue
			}
			if event.Method != "account/login/completed" {
				continue
			}
			var status struct {
				LoginID string `json:"loginId"`
				Success bool   `json:"success"`
				Error   string `json:"error"`
			}
			if json.Unmarshal(event.Params, &status) != nil || status.LoginID != p.loginID {
				continue
			}
			if !status.Success {
				m.finish(p, nil, "Codex sign-in failed. Check your account and workspace permissions")
				return
			}
			result, err := c.call(loginCtx, "account/read", map[string]any{"refreshToken": false})
			if err != nil {
				m.finish(p, nil, err.Error())
				return
			}
			var v struct {
				Account *struct {
					Type  string `json:"type"`
					Email string `json:"email"`
					Plan  string `json:"planType"`
				} `json:"account"`
			}
			if json.Unmarshal(result, &v) != nil || v.Account == nil || v.Account.Type != "chatgpt" {
				m.finish(p, nil, "Codex did not confirm ChatGPT authentication")
				return
			}
			a := Account{ID: id, Email: v.Account.Email, Plan: v.Account.Plan, Connected: true}
			m.finish(p, &a, "")
			return
		}
	}()
	return out, nil
}
func (m *Manager) finish(p *pending, a *Account, msg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p.Status != "pending" {
		return
	}
	p.URL = ""
	if a == nil {
		p.Status = "failed"
		p.Error = msg
		return
	}
	m.accounts[a.ID] = *a
	if err := m.save(); err != nil {
		p.Status = "failed"
		p.Error = "Could not save the Codex connection"
		return
	}
	p.Status = "connected"
	p.Account = a
}
func (m *Manager) Status(id string) (Attempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.attempts[id]
	if p == nil {
		return Attempt{}, errors.New("Codex sign-in not found")
	}
	a := p.Attempt
	a.URL = ""
	return a, nil
}
func (m *Manager) Cancel(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.attempts[id]
	if p == nil {
		return errors.New("Codex sign-in not found")
	}
	if p.Status == "pending" {
		p.Status = "cancelled"
		p.URL = ""
		p.cancel()
	}
	return nil
}

type Model struct {
	Slug          string   `json:"slug"`
	DisplayName   string   `json:"display_name"`
	CyberPrograms []string `json:"cyber_programs,omitempty"`
}

func (m *Manager) Models(ctx context.Context, id string) ([]Model, error) {
	if _, err := m.Account(id); err != nil {
		return nil, err
	}
	release, err := m.acquire(ctx, id)
	if err != nil {
		return nil, err
	}
	defer release()
	dir, err := m.directory(id)
	if err != nil {
		return nil, err
	}
	c, err := m.launch(ctx, dir)
	if err != nil {
		return nil, err
	}
	defer c.close()
	out := []Model{}
	cursor := ""
	for page := 0; page < 50; page++ {
		params := map[string]any{"includeHidden": false}
		if cursor != "" {
			params["cursor"] = cursor
		}
		b, err := c.call(ctx, "model/list", params)
		if err != nil {
			return nil, err
		}
		var catalog struct {
			Data []struct {
				Model                   string `json:"model"`
				DisplayName             string `json:"displayName"`
				Hidden                  bool   `json:"hidden"`
				AvailableAccessPrograms *struct {
					Cyber []string `json:"cyber"`
				} `json:"availableAccessPrograms"`
			} `json:"data"`
			NextCursor string `json:"nextCursor"`
		}
		if err = json.Unmarshal(b, &catalog); err != nil {
			return nil, err
		}
		for _, model := range catalog.Data {
			if model.Hidden || model.Model == "" {
				continue
			}
			name := strings.TrimSpace(model.DisplayName)
			if name == "" {
				name = model.Model
			}
			choice := Model{Slug: model.Model, DisplayName: name}
			if model.AvailableAccessPrograms != nil {
				choice.CyberPrograms = model.AvailableAccessPrograms.Cyber
			}
			out = append(out, choice)
		}
		if catalog.NextCursor == "" {
			return out, nil
		}
		if catalog.NextCursor == cursor {
			return nil, errors.New("Codex model pagination did not advance")
		}
		cursor = catalog.NextCursor
	}
	return nil, errors.New("Codex model catalog exceeded the page limit")
}
