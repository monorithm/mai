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

// fakeOwner is a fake server of the owner API. It records the calls and the bodies of the
// calls that make a session.
type fakeOwner struct {
	calls  []string
	bodies []map[string]any
	ui     bool
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
			{
				"login": "cursor.s1", "agent": "cursor", "label": "s1", "status": "active", "leased": false,
				"app": nil, "folder": nil, "pid": nil, "last_seen_at": nil,
			},
			{
				"login": "cursor.s2", "agent": "cursor", "label": "s2", "status": "active", "leased": true,
				"app": "claude", "folder": "/work", "pid": 77, "last_seen_at": "2026-11-12T10:01:00Z",
			},
			{
				"login": "cursor.lane-a", "agent": "cursor", "label": "lane-a", "status": "revoked", "leased": false,
				"app": nil, "folder": nil, "pid": nil, "last_seen_at": nil,
			},
			{
				"login": "codex.lane-b", "agent": "codex", "label": "lane-b", "status": "active", "leased": false,
				"app": nil, "folder": nil, "pid": nil, "last_seen_at": nil,
			},
		}})
	})
	mux.HandleFunc("POST /api/sessions", func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.bodies = append(f.bodies, in)
		if in["agent"] != "cursor" {
			write(w, 404, map[string]any{"error": map[string]string{"code": "AGENT_UNKNOWN", "message": "the agent is not known"}})
			return
		}
		if n, ok := in["count"].(float64); ok {
			made := []map[string]any{}
			for i := 2; i <= int(n); i++ {
				l := fmt.Sprintf("s%d", i)
				made = append(made, map[string]any{"login": "cursor." + l, "agent": "cursor", "label": l, "status": "active"})
			}
			write(w, 201, map[string]any{"sessions": made})
			return
		}
		label, _ := in["label"].(string)
		if label == "lane-a" {
			write(w, 409, map[string]any{"error": map[string]string{"code": "SESSION_EXISTS", "message": "the session exists"}})
			return
		}
		write(w, 201, map[string]any{"login": "cursor." + label, "agent": "cursor", "label": label, "status": "active"})
	})
	mux.HandleFunc("POST /api/sessions/{login}/rotate", func(w http.ResponseWriter, r *http.Request) {
		write(w, 200, map[string]any{"login": r.PathValue("login"), "status": "active"})
	})
	mux.HandleFunc("POST /api/sessions/{login}/revoke", func(w http.ResponseWriter, r *http.Request) {
		write(w, 200, map[string]any{"login": r.PathValue("login"), "status": "revoked"})
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

func TestSessionListShowsTheLabelStateAndLease(t *testing.T) {
	f, _, _ := testEnv(t)
	var out, errOut bytes.Buffer
	if code := runSession([]string{"list"}, nil, &out, &errOut); code != 0 {
		t.Fatalf("list: %d %s", code, errOut.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 5 {
		t.Fatalf("list printed:\n%s", out.String())
	}
	wantRows := [][]string{
		{"LOGIN", "AGENT", "LABEL", "STATE", "LEASED", "APP", "FOLDER", "PID", "LAST", "SEEN"},
		{"cursor.s1", "cursor", "s1", "active", "no", "-", "-", "-", "-"},
		{"cursor.s2", "cursor", "s2", "active", "yes", "claude", "/work", "77", "2026-11-12T10:01:00Z"},
		{"cursor.lane-a", "cursor", "lane-a", "revoked", "no", "-", "-", "-", "-"},
		{"codex.lane-b", "codex", "lane-b", "active", "no", "-", "-", "-", "-"},
	}
	for i, want := range wantRows {
		if got := strings.Fields(lines[i]); strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("row %d = %q, want %q", i, got, want)
		}
	}
	if len(f.calls) != 1 || f.calls[0] != "GET /api/sessions" {
		t.Fatalf("calls: %v", f.calls)
	}

	out.Reset()
	if code := runSession([]string{"list", "--agent", "codex"}, nil, &out, &errOut); code != 0 {
		t.Fatalf("list --agent: %d %s", code, errOut.String())
	}
	if s := out.String(); !strings.Contains(s, "codex.lane-b") || strings.Contains(s, "cursor.") {
		t.Fatalf("list --agent codex printed:\n%s", s)
	}

	out.Reset()
	if code := runSession([]string{"list", "--json", "--agent", "cursor"}, nil, &out, &errOut); code != 0 {
		t.Fatalf("list --json: %d %s", code, errOut.String())
	}
	var rows []map[string]any
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil || len(rows) != 3 {
		t.Fatalf("list --json printed %s: %v", out.String(), err)
	}
	if rows[1]["login"] != "cursor.s2" || rows[1]["status"] != "active" || rows[1]["label"] != "s2" || rows[1]["pid"] != float64(77) {
		t.Fatalf("list --json row = %v", rows[1])
	}
}

func TestSessionNewPrintsOnlyTheLogin(t *testing.T) {
	f, _, _ := testEnv(t)
	var out, errOut bytes.Buffer
	if code := runSession([]string{"new", "cursor", "--label", "lane-b"}, nil, &out, &errOut); code != 0 {
		t.Fatalf("new --label: %d %s", code, errOut.String())
	}
	if out.String() != "cursor.lane-b\n" {
		t.Fatalf("new --label printed %q", out.String())
	}
	if b := f.bodies[0]; len(b) != 2 || b["agent"] != "cursor" || b["label"] != "lane-b" {
		t.Fatalf("body = %v", b)
	}

	out.Reset()
	if code := runSession([]string{"new", "--count", "3", "cursor"}, nil, &out, &errOut); code != 0 {
		t.Fatalf("new --count: %d %s", code, errOut.String())
	}
	if out.String() != "cursor.s2\ncursor.s3\n" {
		t.Fatalf("new --count printed %q", out.String())
	}
	if b := f.bodies[1]; len(b) != 2 || b["agent"] != "cursor" || b["count"] != float64(3) {
		t.Fatalf("body = %v", b)
	}

	out.Reset()
	errOut.Reset()
	if code := runSession([]string{"new", "cursor", "--count", "1"}, nil, &out, &errOut); code != 0 {
		t.Fatalf("new --count 1: %d %s", code, errOut.String())
	}
	if out.Len() != 0 || !strings.Contains(errOut.String(), "No session is new") {
		t.Fatalf("new --count 1 printed %q and %q", out.String(), errOut.String())
	}
}

func TestSessionNewGivesTheErrorsOfTheServer(t *testing.T) {
	_, _, _ = testEnv(t)
	var out, errOut bytes.Buffer
	if code := runSession([]string{"new", "cursor", "--label", "lane-a"}, nil, &out, &errOut); code != 1 {
		t.Fatalf("new of a session that exists: %d", code)
	}
	if !strings.Contains(errOut.String(), "SESSION_EXISTS") || out.Len() != 0 {
		t.Fatalf("stderr = %q, stdout = %q", errOut.String(), out.String())
	}
	errOut.Reset()
	if code := runSession([]string{"new", "nobody", "--label", "x"}, nil, &out, &errOut); code != 1 {
		t.Fatalf("new for an unknown agent: %d", code)
	}
	if !strings.Contains(errOut.String(), "AGENT_UNKNOWN") || !strings.Contains(errOut.String(), "mai agent add nobody") {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestSessionRefusesBadArgumentsWithNoCall(t *testing.T) {
	f, _, _ := testEnv(t)
	bad := [][]string{
		{"new", "cursor"},
		{"new", "cursor", "--label", "a", "--count", "2"},
		{"new", "cursor", "--label", "Lane-A"},
		{"new", "cursor", "--label", "-a"},
		{"new", "cursor", "--label", strings.Repeat("a", 33)},
		{"new", "cursor", "--count", "-1"},
		{"new", "--label", "a"},
		{"new", "cursor", "codex", "--label", "a"},
		{"list", "extra"},
		{"rotate"},
		{"revoke", "a", "b"},
		{"end"},
		{"show", "cursor.s1"},
		{},
	}
	for _, args := range bad {
		var out, errOut bytes.Buffer
		if code := runSession(args, nil, &out, &errOut); code != 2 {
			t.Errorf("%q gives %d, want 2", args, code)
		}
	}
	if len(f.calls) != 0 {
		t.Fatalf("calls: %v", f.calls)
	}
	var out, errOut bytes.Buffer
	if code := runSession([]string{"new", "cursor", "--label", strings.Repeat("a", 32)}, nil, &out, &errOut); code != 0 {
		t.Fatalf("a label of 32 characters gives %d: %s", code, errOut.String())
	}
}

func TestSessionRotateAndEnd(t *testing.T) {
	f, _, _ := testEnv(t)
	var out, errOut bytes.Buffer
	if code := runSession([]string{"rotate", "cursor.lane-a"}, nil, &out, &errOut); code != 0 {
		t.Fatalf("rotate: %d %s", code, errOut.String())
	}
	if code := runSession([]string{"end", "cursor.s2@mai.internal"}, nil, &out, &errOut); code != 0 {
		t.Fatalf("end: %d %s", code, errOut.String())
	}
	if out.String() != "cursor.lane-a\ncursor.s2\n" {
		t.Fatalf("printed %q", out.String())
	}
	want := []string{"POST /api/sessions/cursor.lane-a/rotate", "DELETE /api/sessions/cursor.s2/lease"}
	if strings.Join(f.calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls: %v", f.calls)
	}
}

func TestSessionRevokeWantsTheTypedWord(t *testing.T) {
	f, _, _ := testEnv(t)
	for _, typed := range []string{"", "yes\n", "Revoke\n"} {
		var out, errOut bytes.Buffer
		if code := runSession([]string{"revoke", "cursor.lane-a"}, strings.NewReader(typed), &out, &errOut); code != 1 {
			t.Fatalf("revoke with %q gives %d", typed, code)
		}
		if !strings.Contains(out.String(), "Type revoke to agree") || !strings.Contains(errOut.String(), "stopped") {
			t.Fatalf("revoke with %q printed %q and %q", typed, out.String(), errOut.String())
		}
	}
	if len(f.calls) != 0 {
		t.Fatalf("calls with no agreement: %v", f.calls)
	}

	var out, errOut bytes.Buffer
	if code := runSession([]string{"revoke", "cursor.lane-a"}, strings.NewReader("revoke\n"), &out, &errOut); code != 0 {
		t.Fatalf("revoke with the word: %d %s", code, errOut.String())
	}
	if !strings.HasSuffix(out.String(), "cursor.lane-a\n") {
		t.Fatalf("revoke printed %q", out.String())
	}
	out.Reset()
	if code := runSession([]string{"revoke", "--yes", "cursor.lane-b@mai.internal"}, nil, &out, &errOut); code != 0 {
		t.Fatalf("revoke --yes: %d %s", code, errOut.String())
	}
	if out.String() != "cursor.lane-b\n" {
		t.Fatalf("revoke --yes printed %q", out.String())
	}
	want := []string{"POST /api/sessions/cursor.lane-a/revoke", "POST /api/sessions/cursor.lane-b/revoke"}
	if strings.Join(f.calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls: %v", f.calls)
	}
}

func TestSessionCommandsPrintNoSecret(t *testing.T) {
	_, _, _ = testEnv(t)
	runs := [][]string{
		{"new", "cursor", "--label", "lane-c"},
		{"new", "cursor", "--count", "2"},
		{"list"},
		{"list", "--json"},
		{"rotate", "cursor.lane-c"},
		{"revoke", "--yes", "cursor.lane-c"},
		{"end", "cursor.s2"},
	}
	for _, args := range runs {
		var out, errOut bytes.Buffer
		if code := runSession(args, nil, &out, &errOut); code != 0 {
			t.Fatalf("%q: %d %s", args, code, errOut.String())
		}
		for _, secret := range []string{"mai1:", "credential", "secret", testOwnerKey} {
			if strings.Contains(out.String()+errOut.String(), secret) {
				t.Fatalf("%q printed %q", args, out.String()+errOut.String())
			}
		}
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
