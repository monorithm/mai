package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/monorithm/mai/cli/v3/internal/keychain"
	"github.com/monorithm/mai/cli/v3/internal/paths"
)

// Error is the error object of the server: {"error":{"code":"CODE","message":"..."}}.
type Error struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("server: %s (HTTP %d)", e.Code, e.Status)
	}
	return fmt.Sprintf("server: %s: %s (HTTP %d)", e.Code, e.Message, e.Status)
}

// Code returns the error code of the server in err, or "" when err is not an Error.
func Code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// ErrNoOwnerKey means that the keychain has no owner key for the instance.
var ErrNoOwnerKey = errors.New("api: no owner key in the keychain; run mai start first")

// Client calls the owner API. The owner key is only in the memory of the client.
type Client struct {
	base string
	key  string
	http *http.Client
}

// New returns a client for the server at base with the owner key.
func New(base, ownerKey string) *Client {
	return &Client{base: base, key: ownerKey, http: &http.Client{Timeout: 30 * time.Second}}
}

// Open reads the configuration of the CLI and the owner key from store.
func Open(store keychain.Store) (*Client, Config, error) {
	d, err := paths.Default()
	if err != nil {
		return nil, Config{}, err
	}
	cfg, err := LoadConfig(d.Config)
	if err != nil {
		return nil, cfg, err
	}
	key, err := store.Get(keychain.Service, keychain.OwnerAccount(cfg.Instance))
	if errors.Is(err, keychain.ErrNotFound) {
		return nil, cfg, ErrNoOwnerKey
	}
	if err != nil {
		return nil, cfg, err
	}
	return New(cfg.BaseURL(), key), cfg, nil
}

// OpenSystem is Open with the keychain of the OS.
func OpenSystem() (*Client, Config, error) { return Open(keychain.System()) }

func (c *Client) do(ctx context.Context, method, path string, auth bool, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if auth {
		req.Header.Set("Authorization", "Bearer "+c.key)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("api: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		var env struct {
			Error *Error `json:"error"`
		}
		if json.Unmarshal(b, &env) == nil && env.Error != nil {
			env.Error.Status = resp.StatusCode
			return env.Error
		}
		return &Error{Status: resp.StatusCode, Code: "HTTP_" + fmt.Sprint(resp.StatusCode), Message: http.StatusText(resp.StatusCode)}
	}
	if out == nil || len(b) == 0 {
		return nil
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("api: %s %s: bad answer: %w", method, path, err)
	}
	return nil
}

// VersionInfo is the answer of GET /api/version.
type VersionInfo struct {
	Version  string `json:"version"`
	Contract string `json:"contract"`
}

// Version calls GET /api/version. It sends no key.
func (c *Client) Version(ctx context.Context) (VersionInfo, error) {
	var v VersionInfo
	err := c.do(ctx, http.MethodGet, "/api/version", false, nil, &v)
	return v, err
}

// Health is the answer of GET /api/health.
type Health struct {
	Healthy  bool              `json:"healthy"`
	Children map[string]string `json:"children"`
}

// Health calls GET /api/health.
func (c *Client) Health(ctx context.Context) (Health, error) {
	var h Health
	err := c.do(ctx, http.MethodGet, "/api/health", true, nil, &h)
	return h, err
}

// Agent is one agent in GET /api/agents.
type Agent struct {
	Name     string `json:"name"`
	Sessions int    `json:"sessions"`
	Leases   int    `json:"leases"`
}

// NewAgent is the answer of POST /api/agents. AgentKey is a secret.
type NewAgent struct {
	Name     string `json:"name"`
	AgentKey string `json:"agent_key"`
}

// AddAgent calls POST /api/agents. The answer holds the agent key one time only.
func (c *Client) AddAgent(ctx context.Context, name string) (NewAgent, error) {
	var a NewAgent
	err := c.do(ctx, http.MethodPost, "/api/agents", true, map[string]string{"name": name}, &a)
	return a, err
}

// ListAgents calls GET /api/agents.
func (c *Client) ListAgents(ctx context.Context) ([]Agent, error) {
	var r struct {
		Agents []Agent `json:"agents"`
	}
	err := c.do(ctx, http.MethodGet, "/api/agents", true, nil, &r)
	return r.Agents, err
}

// RemoveAgent calls DELETE /api/agents/{name}.
func (c *Client) RemoveAgent(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, "/api/agents/"+url.PathEscape(name), true, nil, nil)
}

// RotateAgentKey calls POST /api/agents/{name}/key and returns the new agent key.
func (c *Client) RotateAgentKey(ctx context.Context, name string) (string, error) {
	var r struct {
		AgentKey string `json:"agent_key"`
	}
	err := c.do(ctx, http.MethodPost, "/api/agents/"+url.PathEscape(name)+"/key", true, nil, &r)
	return r.AgentKey, err
}

// Lease is one lease in GET /api/leases.
type Lease struct {
	Session    string `json:"session"`
	Agent      string `json:"agent"`
	App        string `json:"app"`
	Folder     string `json:"folder"`
	PID        int    `json:"pid"`
	StartedAt  string `json:"started_at"`
	LastSeenAt string `json:"last_seen_at"`
}

// ListLeases calls GET /api/leases.
func (c *Client) ListLeases(ctx context.Context) ([]Lease, error) {
	var r struct {
		Leases []Lease `json:"leases"`
	}
	err := c.do(ctx, http.MethodGet, "/api/leases", true, nil, &r)
	return r.Leases, err
}

// Session is one session in GET /api/sessions.
type Session struct {
	Login  string `json:"login"`
	Agent  string `json:"agent"`
	Leased bool   `json:"leased"`
}

// ListSessions calls GET /api/sessions.
func (c *Client) ListSessions(ctx context.Context) ([]Session, error) {
	var r struct {
		Sessions []Session `json:"sessions"`
	}
	err := c.do(ctx, http.MethodGet, "/api/sessions", true, nil, &r)
	return r.Sessions, err
}

// EndSession calls DELETE /api/sessions/{login}/lease.
func (c *Client) EndSession(ctx context.Context, login string) error {
	return c.do(ctx, http.MethodDelete, "/api/sessions/"+url.PathEscape(login)+"/lease", true, nil, nil)
}

type uiState struct {
	Enabled bool `json:"enabled"`
}

// UI calls GET /api/ui and returns true when the mail UI is on.
func (c *Client) UI(ctx context.Context) (bool, error) {
	var s uiState
	err := c.do(ctx, http.MethodGet, "/api/ui", true, nil, &s)
	return s.Enabled, err
}

// SetUI calls PUT /api/ui and returns the new state.
func (c *Client) SetUI(ctx context.Context, enabled bool) (bool, error) {
	var s uiState
	err := c.do(ctx, http.MethodPut, "/api/ui", true, uiState{Enabled: enabled}, &s)
	return s.Enabled, err
}

// Ticket is the answer of POST /api/ui/tickets. Value is a secret.
type Ticket struct {
	Value          string `json:"ticket"`
	ExpiresSeconds int    `json:"expires_seconds"`
}

// UITicket calls POST /api/ui/tickets. The server gives UI_OFF when the UI is off.
func (c *Client) UITicket(ctx context.Context) (Ticket, error) {
	var t Ticket
	err := c.do(ctx, http.MethodPost, "/api/ui/tickets", true, nil, &t)
	return t, err
}
