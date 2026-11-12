package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monorithm/mai/cli/v3/internal/connect"
	"github.com/monorithm/mai/cli/v3/internal/keychain"
	"github.com/monorithm/mai/cli/v3/internal/manifest"
)

const testOwnerKey = "mai1:owner:testownersecret"

// fakeOwner is a fake server of the owner API. It records the calls.
type fakeOwner struct {
	calls []string
	ui    bool
}

func (f *fakeOwner) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	write := func(w http.ResponseWriter, code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("POST /api/agents", func(w http.ResponseWriter, r *http.Request) {
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		write(w, 201, map[string]string{"name": in["name"], "agent_key": "mai1:" + in["name"] + ":agentsecret"})
	})
	mux.HandleFunc("GET /api/agents", func(w http.ResponseWriter, r *http.Request) {
		write(w, 200, map[string]any{"agents": []map[string]any{{"name": "cursor", "sessions": 2, "leases": 1}}})
	})
	mux.HandleFunc("DELETE /api/agents/{name}", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	mux.HandleFunc("GET /api/sessions", func(w http.ResponseWriter, r *http.Request) {
		write(w, 200, map[string]any{"sessions": []map[string]any{
			{"login": "cursor.s1", "agent": "cursor", "leased": false},
			{"login": "cursor.s2", "agent": "cursor", "leased": true},
		}})
	})
	mux.HandleFunc("GET /api/leases", func(w http.ResponseWriter, r *http.Request) {
		write(w, 200, map[string]any{"leases": []map[string]any{{
			"session": "cursor.s2", "agent": "cursor", "app": "claude", "folder": "/work", "pid": 77,
			"started_at": "2026-11-12T10:00:00Z", "last_seen_at": "2026-11-12T10:01:00Z",
		}}})
	})
	mux.HandleFunc("DELETE /api/sessions/{login}/lease", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	mux.HandleFunc("GET /api/ui", func(w http.ResponseWriter, r *http.Request) { write(w, 200, map[string]bool{"enabled": f.ui}) })
	mux.HandleFunc("PUT /api/ui", func(w http.ResponseWriter, r *http.Request) {
		var in map[string]bool
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.ui = in["enabled"]
		write(w, 200, map[string]bool{"enabled": f.ui})
	})
	mux.HandleFunc("POST /api/ui/tickets", func(w http.ResponseWriter, r *http.Request) {
		if !f.ui {
			write(w, 409, map[string]any{"error": map[string]string{"code": "UI_OFF", "message": "the UI is off"}})
			return
		}
		write(w, 201, map[string]any{"ticket": "secretticket", "expires_seconds": 60})
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testOwnerKey {
			write(w, 401, map[string]any{"error": map[string]string{"code": "BAD_KEY", "message": "bad key"}})
			return
		}
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)
		mux.ServeHTTP(w, r)
	})
}

// testEnv makes a temp HOME and MAI_HOME, a config.json with the port of a fake server, and a
// keychain in memory with the owner key.
func testEnv(t *testing.T) (*fakeOwner, *keychain.Memory, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("MAI_HOME", filepath.Join(home, "maihome"))
	f := &fakeOwner{}
	s := httptest.NewServer(f.handler(t))
	t.Cleanup(s.Close)
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(s.URL, "http://"))
	cfgDir := filepath.Join(home, "maihome", "config")
	_ = os.MkdirAll(cfgDir, 0o700)
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(`{"instance":"mai","port":`+port+`}`), 0o600); err != nil {
		t.Fatal(err)
	}
	store := keychain.NewMemory()
	_ = store.Set(keychain.Service, keychain.OwnerAccount("mai"), testOwnerKey)
	old := accountsKeychain
	accountsKeychain = func() keychain.Store { return store }
	t.Cleanup(func() { accountsKeychain = old })
	return f, store, home
}

func loadTestManifest(t *testing.T, home string) *manifest.Manifest {
	t.Helper()
	m, err := manifest.Load(filepath.Join(home, "maihome", "state", manifest.FileName), "mai")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestAgentAddListRemove(t *testing.T) {
	_, store, home := testEnv(t)
	var out, errOut bytes.Buffer
	if code := runAgent([]string{"add", "cursor"}, &out, &errOut); code != 0 {
		t.Fatalf("add: %d %s", code, errOut.String())
	}
	if out.String() != "cursor\n" {
		t.Fatalf("add printed %q", out.String())
	}
	key, err := store.Get(keychain.Service, keychain.AgentAccount("mai", "cursor"))
	if err != nil || key != "mai1:cursor:agentsecret" {
		t.Fatalf("keychain: %v", err)
	}
	if items := loadTestManifest(t, home).Of(manifest.KeychainKey); len(items) != 1 || items[0].Name != keychain.AgentAccount("mai", "cursor") {
		t.Fatalf("manifest: %+v", items)
	}
	b, _ := os.ReadFile(filepath.Join(home, "maihome", "state", manifest.FileName))
	if strings.Contains(string(b), "agentsecret") {
		t.Fatal("the manifest holds the key")
	}
	out.Reset()
	if code := runAgent([]string{"list"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "cursor") {
		t.Fatalf("list: %d %q", code, out.String())
	}
	out.Reset()
	if code := runAgent([]string{"remove", "cursor"}, &out, &errOut); code != 0 {
		t.Fatalf("remove: %d %s", code, errOut.String())
	}
	if _, err := store.Get(keychain.Service, keychain.AgentAccount("mai", "cursor")); err != keychain.ErrNotFound {
		t.Fatalf("the key stays: %v", err)
	}
	if items := loadTestManifest(t, home).Of(manifest.KeychainKey); len(items) != 0 {
		t.Fatalf("the manifest keeps %+v", items)
	}
	if code := runAgent([]string{"add"}, &out, &errOut); code != 2 {
		t.Fatalf("add with no name gives %d", code)
	}
	out.Reset()
	if code := runAgent([]string{"add", "a", "b"}, &out, &errOut); code != 0 || out.String() != "a\nb\n" {
		t.Fatalf("add of two agents: %d %q", code, out.String())
	}
	if _, err := store.Get(keychain.Service, keychain.AgentAccount("mai", "b")); err != nil {
		t.Fatalf("no key for b: %v", err)
	}
}

func TestSessionListAndEnd(t *testing.T) {
	f, _, _ := testEnv(t)
	var out, errOut bytes.Buffer
	if code := runSession([]string{"list"}, &out, &errOut); code != 0 {
		t.Fatalf("list: %d %s", code, errOut.String())
	}
	s := out.String()
	if !strings.Contains(s, "cursor.s1") || !strings.Contains(s, "/work") || !strings.Contains(s, "77") {
		t.Fatalf("list printed:\n%s", s)
	}
	if code := runSession([]string{"end", "cursor.s2"}, &out, &errOut); code != 0 {
		t.Fatalf("end: %d %s", code, errOut.String())
	}
	if f.calls[len(f.calls)-1] != "DELETE /api/sessions/cursor.s2/lease" {
		t.Fatalf("calls: %v", f.calls)
	}
}

func TestApprovalsShowsLastDays(t *testing.T) {
	_, _, home := testEnv(t)
	var out, errOut bytes.Buffer
	if code := runApprovals(nil, &out, &errOut); code != 0 || !strings.Contains(out.String(), "no approvals") {
		t.Fatalf("empty: %d %q", code, out.String())
	}
	now := time.Now().UTC()
	doc := fmt.Sprintf(`{"approvals":[
		{"time":%q,"agent":"cursor","app":"claude","folder":"/new","pid":1,"result":"approved"},
		{"time":%q,"agent":"cursor","app":"claude","folder":"/old","pid":2,"result":"refused"}]}`,
		now.Format(time.RFC3339), now.Add(-40*24*time.Hour).Format(time.RFC3339))
	state := filepath.Join(home, "maihome", "state")
	_ = os.MkdirAll(state, 0o700)
	_ = os.WriteFile(filepath.Join(state, ApprovalsFile), []byte(doc), 0o600)
	out.Reset()
	if code := runApprovals(nil, &out, &errOut); code != 0 {
		t.Fatalf("list: %d %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "/new") || strings.Contains(out.String(), "/old") {
		t.Fatalf("approvals printed:\n%s", out.String())
	}
}

func TestUIOnOff(t *testing.T) {
	f, _, _ := testEnv(t)
	var out, errOut bytes.Buffer
	if code := runUI(nil, &out, &errOut); code != 0 || out.String() != "ui off\n" {
		t.Fatalf("status: %d %q", code, out.String())
	}
	out.Reset()
	if code := runUI([]string{"on"}, &out, &errOut); code != 0 || out.String() != "ui on\n" || !f.ui {
		t.Fatalf("on: %d %q", code, out.String())
	}
	out.Reset()
	if code := runUI([]string{"off"}, &out, &errOut); code != 0 || out.String() != "ui off\n" || f.ui {
		t.Fatalf("off: %d %q", code, out.String())
	}
	if code := runUI([]string{"bad"}, &out, &errOut); code != 2 {
		t.Fatalf("bad: %d", code)
	}
}

func TestConnectCommandThenDisconnect(t *testing.T) {
	_, _, home := testEnv(t)
	// The test binary is a temporary build, which connect refuses. Use a fixed path.
	oldBinary := connect.Binary
	connect.Binary = func() (string, error) { return "/opt/mai/bin/mai", nil }
	t.Cleanup(func() { connect.Binary = oldBinary })
	settings := filepath.Join(home, ".claude", "settings.json")
	orig := "{\n  \"model\": \"opus\"\n}\n"
	_ = os.MkdirAll(filepath.Dir(settings), 0o700)
	_ = os.WriteFile(settings, []byte(orig), 0o600)

	var out, errOut bytes.Buffer
	if code := runConnect([]string{"claude", "--agent", "cursor"}, strings.NewReader("n\n"), &out, &errOut); code != 1 {
		t.Fatalf("connect with no: %d %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "+++ "+settings) || !strings.Contains(out.String(), "/opt/mai/bin/mai hook --format claude --event Stop") ||
		!strings.Contains(out.String(), "The entries name the mai binary /opt/mai/bin/mai.") {
		t.Fatalf("no diff:\n%s", out.String())
	}
	if b, _ := os.ReadFile(settings); string(b) != orig {
		t.Fatal("connect with no changed the file")
	}
	out.Reset()
	if code := runConnect([]string{"claude", "--agent", "cursor"}, strings.NewReader("y\n"), &out, &errOut); code != 0 {
		t.Fatalf("connect: %d %s", code, errOut.String())
	}
	b, _ := os.ReadFile(filepath.Join(home, ".claude.json"))
	if !strings.Contains(string(b), `"--agent"`) {
		t.Fatalf("no MCP entry:\n%s", b)
	}
	if code := runDisconnect([]string{"claude"}, &out, &errOut); code != 0 {
		t.Fatalf("disconnect: %d %s", code, errOut.String())
	}
	if b, _ := os.ReadFile(settings); string(b) != orig {
		t.Fatalf("disconnect gives:\n%s", b)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude.json")); !os.IsNotExist(err) {
		t.Fatal("the file that connect made stays")
	}
	if code := runConnect([]string{"nope", "--agent", "a"}, strings.NewReader(""), &out, &errOut); code != 2 {
		t.Fatalf("unknown app gives %d", code)
	}
}
