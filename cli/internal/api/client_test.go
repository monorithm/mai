package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/monorithm/mai/cli/internal/keychain"
)

const ownerKey = "mai1:owner:testsecret"

func server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+ownerKey {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":{"code":"BAD_KEY","message":"no owner key"}}`))
				return
			}
			h(w, r)
		}
	}
	write := func(w http.ResponseWriter, code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("version call sent a key")
		}
		write(w, 200, map[string]string{"version": "3.0.0", "contract": "3.0.0"})
	})
	mux.HandleFunc("GET /api/health", auth(func(w http.ResponseWriter, r *http.Request) {
		write(w, 200, map[string]any{"healthy": true, "children": map[string]string{"postgres": "ok"}})
	}))
	mux.HandleFunc("POST /api/agents", auth(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in["name"] == "taken" {
			write(w, 409, map[string]any{"error": map[string]string{"code": "AGENT_EXISTS", "message": "the agent exists"}})
			return
		}
		write(w, 201, map[string]string{"name": in["name"], "agent_key": "mai1:" + in["name"] + ":agentsecret"})
	}))
	mux.HandleFunc("GET /api/agents", auth(func(w http.ResponseWriter, r *http.Request) {
		write(w, 200, map[string]any{"agents": []map[string]any{{"name": "cursor", "sessions": 2, "leases": 1}}})
	}))
	mux.HandleFunc("DELETE /api/agents/{name}", auth(func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("name") != "cursor" {
			write(w, 404, map[string]any{"error": map[string]string{"code": "NOT_FOUND", "message": "no agent"}})
			return
		}
		w.WriteHeader(204)
	}))
	mux.HandleFunc("POST /api/agents/{name}/key", auth(func(w http.ResponseWriter, r *http.Request) {
		write(w, 200, map[string]string{"agent_key": "mai1:" + r.PathValue("name") + ":newsecret"})
	}))
	mux.HandleFunc("GET /api/leases", auth(func(w http.ResponseWriter, r *http.Request) {
		write(w, 200, map[string]any{"leases": []map[string]any{{
			"session": "cursor.s2", "agent": "cursor", "app": "claude", "folder": "/w", "pid": 12,
			"started_at": "2026-11-12T10:00:00Z", "last_seen_at": "2026-11-12T10:01:00Z",
		}}})
	}))
	mux.HandleFunc("GET /api/sessions", auth(func(w http.ResponseWriter, r *http.Request) {
		write(w, 200, map[string]any{"sessions": []map[string]any{{"login": "cursor.s2", "agent": "cursor", "leased": true}}})
	}))
	mux.HandleFunc("DELETE /api/sessions/{login}/lease", auth(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	}))
	enabled := false
	mux.HandleFunc("GET /api/ui", auth(func(w http.ResponseWriter, r *http.Request) {
		write(w, 200, map[string]bool{"enabled": enabled})
	}))
	mux.HandleFunc("PUT /api/ui", auth(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]bool
		_ = json.NewDecoder(r.Body).Decode(&in)
		enabled = in["enabled"]
		write(w, 200, map[string]bool{"enabled": enabled})
	}))
	mux.HandleFunc("POST /api/ui/tickets", auth(func(w http.ResponseWriter, r *http.Request) {
		if !enabled {
			write(w, 409, map[string]any{"error": map[string]string{"code": "UI_OFF", "message": "the UI is off"}})
			return
		}
		write(w, 201, map[string]any{"ticket": "tk", "expires_seconds": 60})
	}))
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func TestClientCalls(t *testing.T) {
	s := server(t)
	c := New(s.URL, ownerKey)
	ctx := context.Background()

	v, err := c.Version(ctx)
	if err != nil || v.Version != "3.0.0" || v.Contract != "3.0.0" {
		t.Fatalf("version: %+v %v", v, err)
	}
	h, err := c.Health(ctx)
	if err != nil || !h.Healthy || h.Children["postgres"] != "ok" {
		t.Fatalf("health: %+v %v", h, err)
	}
	a, err := c.AddAgent(ctx, "cursor")
	if err != nil || a.Name != "cursor" || a.AgentKey != "mai1:cursor:agentsecret" {
		t.Fatalf("add agent: %+v %v", a, err)
	}
	_, err = c.AddAgent(ctx, "taken")
	if Code(err) != "AGENT_EXISTS" {
		t.Fatalf("add agent taken: %v", err)
	}
	agents, err := c.ListAgents(ctx)
	if err != nil || len(agents) != 1 || agents[0].Sessions != 2 || agents[0].Leases != 1 {
		t.Fatalf("list agents: %+v %v", agents, err)
	}
	if err := c.RemoveAgent(ctx, "cursor"); err != nil {
		t.Fatalf("remove agent: %v", err)
	}
	if err := c.RemoveAgent(ctx, "nobody"); Code(err) != "NOT_FOUND" {
		t.Fatalf("remove missing agent: %v", err)
	}
	k, err := c.RotateAgentKey(ctx, "cursor")
	if err != nil || k != "mai1:cursor:newsecret" {
		t.Fatalf("rotate: %q %v", k, err)
	}
	leases, err := c.ListLeases(ctx)
	if err != nil || len(leases) != 1 || leases[0].PID != 12 || leases[0].Session != "cursor.s2" {
		t.Fatalf("leases: %+v %v", leases, err)
	}
	sessions, err := c.ListSessions(ctx)
	if err != nil || len(sessions) != 1 || !sessions[0].Leased {
		t.Fatalf("sessions: %+v %v", sessions, err)
	}
	if err := c.EndSession(ctx, "cursor.s2"); err != nil {
		t.Fatalf("end session: %v", err)
	}
	if on, err := c.UI(ctx); err != nil || on {
		t.Fatalf("ui: %v %v", on, err)
	}
	if _, err := c.UITicket(ctx); Code(err) != "UI_OFF" {
		t.Fatalf("ticket with UI off: %v", err)
	}
	if on, err := c.SetUI(ctx, true); err != nil || !on {
		t.Fatalf("set ui: %v %v", on, err)
	}
	tk, err := c.UITicket(ctx)
	if err != nil || tk.Value != "tk" || tk.ExpiresSeconds != 60 {
		t.Fatalf("ticket: %+v %v", tk, err)
	}
}

func TestBadKeyGivesErrorObject(t *testing.T) {
	s := server(t)
	_, err := New(s.URL, "mai1:owner:wrong").Health(context.Background())
	var e *Error
	if !asError(err, &e) || e.Status != 401 || e.Code != "BAD_KEY" || e.Message == "" {
		t.Fatalf("got %v", err)
	}
}

func asError(err error, target **Error) bool {
	e, ok := err.(*Error)
	if ok {
		*target = e
	}
	return ok
}

func TestLoadConfigDefaultsAndFields(t *testing.T) {
	dir := t.TempDir()
	c, err := LoadConfig(dir)
	if err != nil || c.Instance != "mai" || c.Port != 8765 || c.Approval != "each-bridge" {
		t.Fatalf("defaults: %+v %v", c, err)
	}
	if err := os.WriteFile(filepath.Join(dir, ConfigFile), []byte(`{"instance":"x","port":9000}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err = LoadConfig(dir)
	if err != nil || c.Instance != "x" || c.Port != 9000 || c.Approval != "each-bridge" || c.BaseURL() != "http://127.0.0.1:9000" {
		t.Fatalf("fields: %+v %v", c, err)
	}
}

func TestOpenReadsOwnerKey(t *testing.T) {
	t.Setenv("MAI_HOME", t.TempDir())
	store := keychain.NewMemory()
	if _, _, err := Open(store); err != ErrNoOwnerKey {
		t.Fatalf("no key: %v", err)
	}
	_ = store.Set(keychain.Service, keychain.OwnerAccount("mai"), ownerKey)
	c, cfg, err := Open(store)
	if err != nil || cfg.Instance != "mai" || c.key != ownerKey || c.base != "http://127.0.0.1:8765" {
		t.Fatalf("open: %+v %v", cfg, err)
	}
}
