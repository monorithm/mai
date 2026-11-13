package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/moby/moby/client"

	"github.com/monorithm/mai/cli/v3/internal/keys"
	"github.com/monorithm/mai/cli/v3/internal/manifest"
	"github.com/monorithm/mai/cli/v3/internal/paths"
	"github.com/monorithm/mai/cli/v3/internal/uninstall"
)

// These tests start from the state of a mai 3.1 install: the keys are in the keychain of
// the service dev.mai, the manifest records each keychain item, and there is no key file.
// The keychain is a keychain in memory. The commands of the CLI then move the keys to the key
// file.

const (
	legacyCursorKey = "mai1:cursor:legacycursorsecret"
	legacyCodexKey  = "mai1:codex:legacycodexsecret"
)

// leaseFake is a fake of the lease API and of /mcp for the bridge. It accepts only the key
// of the agent cursor.
type leaseFake struct {
	mu       sync.Mutex
	bearers  []string // the bearer of each lease request
	sessions []string // the argument session of each lease request, or "" for the pool
	n        int
	valid    map[string]string // lease token -> session
}

func (l *leaseFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	l.mu.Lock()
	defer l.mu.Unlock()
	bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/leases":
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		l.bearers = append(l.bearers, bearer)
		name, _ := in["session"].(string)
		l.sessions = append(l.sessions, name)
		if bearer != legacyCursorKey {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":{"code":"BAD_KEY","message":"bad key"}}`))
			return
		}
		session := "cursor.s1"
		if name != "" {
			session = "cursor." + strings.TrimPrefix(name, "cursor.")
		}
		l.n++
		lease := fmt.Sprintf("lease_%043d", l.n)
		l.valid[lease] = session
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{"lease": lease, "session": session, "heartbeat_seconds": 30, "expires_seconds": 90})
	case r.Method == http.MethodPost && r.URL.Path == "/api/leases/current/heartbeat":
		_ = json.NewEncoder(w).Encode(map[string]any{"session": l.valid[bearer], "new_mail": 0})
	case r.Method == http.MethodDelete && r.URL.Path == "/api/leases/current":
		delete(l.valid, bearer)
		w.WriteHeader(204)
	case r.Method == http.MethodPost && r.URL.Path == "/mcp":
		var msg map[string]any
		_ = json.NewDecoder(r.Body).Decode(&msg)
		if _, ok := msg["id"]; !ok {
			w.WriteHeader(202)
			return
		}
		session, ok := l.valid[bearer]
		if !ok {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":{"code":"LEASE_UNKNOWN","message":"no lease"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": msg["id"], "result": map[string]any{"session": session}})
	default:
		w.WriteHeader(404)
	}
}

// legacyInstall makes a temp HOME and MAI_HOME, a config.json with the port of a fake server,
// and the keychain items and the manifest records of a mai 3.1 install of the instance mai.
// The fake server serves the owner API and the lease API. The key file does not exist.
func legacyInstall(t *testing.T, items map[string]string) (*fakeOwner, *leaseFake, *testKeyFile, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("MAI_HOME", filepath.Join(home, "maihome"))
	owner := &fakeOwner{}
	lf := &leaseFake{valid: map[string]string{}}
	ownerAPI := owner.handler(t)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mcp" || strings.HasPrefix(r.URL.Path, "/api/leases") {
			lf.ServeHTTP(w, r)
			return
		}
		ownerAPI.ServeHTTP(w, r)
	}))
	t.Cleanup(s.Close)
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(s.URL, "http://"))
	cfgDir := filepath.Join(home, "maihome", "config")
	_ = os.MkdirAll(cfgDir, 0o700)
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(`{"instance":"mai","port":`+port+`}`), 0o600); err != nil {
		t.Fatal(err)
	}
	k := useTestKeys(t)
	m := loadTestManifest(t, home)
	for account, key := range items {
		k.legacy.Put(account, key)
		if keys.InstanceOf(account) != "mai" {
			continue
		}
		if err := m.Add(manifest.Item{Type: manifest.KeychainKey, Name: account}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(k.file.Path()); !os.IsNotExist(err) {
		t.Fatalf("a key file exists before the test: %v", err)
	}
	return owner, lf, k, home
}

// fileKeys gives the keys of the key file. It reads only the file, and never the keychain.
func fileKeys(t *testing.T, k *testKeyFile) map[string]string {
	t.Helper()
	plain := keys.Open(filepath.Dir(k.file.Path()), nil)
	accounts, err := plain.Accounts()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, a := range accounts {
		v, err := plain.Get(a)
		if err != nil {
			t.Fatal(err)
		}
		out[a] = v
	}
	return out
}

// noSecret stops the test when text holds one of the keys of the test.
func noSecret(t *testing.T, what, text string) {
	t.Helper()
	for _, s := range []string{testOwnerKey, legacyCursorKey, legacyCodexKey, "secret"} {
		if strings.Contains(text, s) {
			t.Fatalf("%s shows a key", what)
		}
	}
}

func TestTheCommandsMoveEachKeyOfA31InstallToTheKeyFile(t *testing.T) {
	owner, lf, k, home := legacyInstall(t, map[string]string{
		keys.OwnerAccount("mai"):           testOwnerKey,
		keys.AgentAccount("mai", "cursor"): legacyCursorKey,
		keys.AgentAccount("mai", "codex"):  legacyCodexKey,
	})

	// 1. mai doctor tells about the three keychain items and gives the step mai upgrade.
	ok, bad := doctorRun(t)
	if len(bad) != 1 || !strings.Contains(bad[0], "3 key(s) of the instance mai") || !strings.Contains(bad[0], "run: mai upgrade") {
		t.Fatalf("doctor before the move: bad %v", bad)
	}
	if !strings.Contains(strings.Join(ok, "\n"), "holds 0 key(s) of the instance mai") {
		t.Fatalf("doctor before the move: ok %v", ok)
	}
	noSecret(t, "doctor", strings.Join(append(ok, bad...), "\n"))

	// 2. The first command that uses the owner key moves it. The owner API accepts only the
	// owner key, thus the command operates only with the key of the keychain item.
	var out, errOut bytes.Buffer
	if code := runAgent([]string{"list"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "cursor") {
		t.Fatalf("mai agent list: exit %d, stderr %q", code, errOut.String())
	}
	if strings.Join(owner.calls, ",") != "GET /api/agents" {
		t.Fatalf("the owner API got the calls %v", owner.calls)
	}
	if got := fileKeys(t, k); len(got) != 1 || got[keys.OwnerAccount("mai")] != testOwnerKey {
		t.Fatalf("after mai agent list, the key file %s does not hold only the owner key", k.file.Path())
	}
	if got := strings.Join(k.legacy.Accounts(), ","); got != "agent:codex@mai,agent:cursor@mai" {
		t.Fatalf("after mai agent list, the keychain items are %s", got)
	}
	noSecret(t, "mai agent list", out.String()+errOut.String())

	// 3. The bridge of cursor moves the agent key at its first lease request. A call with the
	// label lane-a then gets a named lease with the same key, with no login.
	stdin := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"whoami","arguments":{"session":"lane-a"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"whoami","arguments":{}}}`,
	}, "\n") + "\n"
	got, code := runtimeCapture(t, stdin, func() int { return runMCP([]string{"--agent", "cursor"}) })
	if code != 0 {
		t.Fatalf("mai mcp --agent cursor: exit %d, output %q", code, got)
	}
	answers := map[float64]map[string]any{}
	for _, line := range strings.Split(strings.TrimSpace(got), "\n") {
		var a map[string]any
		if err := json.Unmarshal([]byte(line), &a); err != nil {
			t.Fatalf("the bridge wrote a line that is not JSON: %v", err)
		}
		id, _ := a["id"].(float64)
		answers[id] = a
	}
	session := func(id float64) any {
		r, _ := answers[id]["result"].(map[string]any)
		return r["session"]
	}
	if session(2) != "cursor.lane-a" || session(3) != "cursor.s1" {
		t.Fatalf("the bridge gave the sessions %v and %v", session(2), session(3))
	}
	lf.mu.Lock()
	bearers, sessions := lf.bearers, lf.sessions
	lf.mu.Unlock()
	if len(bearers) != 2 || bearers[0] != legacyCursorKey || bearers[1] != legacyCursorKey {
		t.Fatalf("the bridge sent %d lease requests, and not each with the key of the keychain item", len(bearers))
	}
	if strings.Join(sessions, ",") != ",lane-a" {
		t.Fatalf("the lease requests named the sessions %q", sessions)
	}
	if got := fileKeys(t, k); len(got) != 2 || got[keys.AgentAccount("mai", "cursor")] != legacyCursorKey {
		t.Fatal("after the bridge, the key file has no key for cursor, or a different key")
	}
	if got := strings.Join(k.legacy.Accounts(), ","); got != "agent:codex@mai" {
		t.Fatalf("after the bridge, the keychain items are %s", got)
	}
	noSecret(t, "mai mcp", got)

	// 4. mai upgrade moves the key that no command used, and prunes the manifest records.
	out.Reset()
	errOut.Reset()
	oldOut, oldErr := lcOut, lcErr
	lcOut, lcErr = &out, &errOut
	t.Cleanup(func() { lcOut, lcErr = oldOut, oldErr })
	m := loadTestManifest(t, home)
	if !lcMoveKeys("mai", m) {
		t.Fatalf("the move of mai upgrade failed: %s", errOut.String())
	}
	if !strings.Contains(out.String(), "moved 1 key(s)") || !strings.Contains(out.String(), "agent:codex@mai") {
		t.Fatalf("the move of mai upgrade wrote %q", out.String())
	}
	noSecret(t, "mai upgrade", out.String()+errOut.String())
	want := map[string]string{
		keys.OwnerAccount("mai"):           testOwnerKey,
		keys.AgentAccount("mai", "cursor"): legacyCursorKey,
		keys.AgentAccount("mai", "codex"):  legacyCodexKey,
	}
	if got := fileKeys(t, k); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatal("after mai upgrade, the key file does not have the three keys of the keychain items")
	}
	if n := len(k.legacy.Accounts()); n != 0 {
		t.Fatalf("after mai upgrade, %d keychain item(s) stay", n)
	}
	if items := loadTestManifest(t, home).Of(manifest.KeychainKey); len(items) != 0 {
		t.Fatalf("after mai upgrade, the manifest records %+v", items)
	}

	// 5. mai doctor finds no keychain item, and a second move does nothing.
	ok, bad = doctorRun(t)
	text := strings.Join(ok, "\n")
	if len(bad) != 0 || !strings.Contains(text, "holds 3 key(s) of the instance mai") ||
		!strings.Contains(text, "the keychain holds no key of the instance mai") {
		t.Fatalf("doctor after the move: ok %v, bad %v", ok, bad)
	}
	out.Reset()
	if !lcMoveKeys("mai", loadTestManifest(t, home)) || out.Len() != 0 {
		t.Fatalf("a second move wrote %q", out.String())
	}
}

// emptyEngine is an engine with no container, volume, or image. A call of a method that the
// type does not have stops the test with a nil pointer panic.
type emptyEngine struct{ uninstall.Engine }

func (emptyEngine) ContainerList(context.Context, client.ContainerListOptions) (client.ContainerListResult, error) {
	return client.ContainerListResult{}, nil
}

func (emptyEngine) ContainerInspect(_ context.Context, id string, _ client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
	return client.ContainerInspectResult{}, uninstall.NotFoundError{What: id}
}

func (emptyEngine) VolumeList(context.Context, client.VolumeListOptions) (client.VolumeListResult, error) {
	return client.VolumeListResult{}, nil
}

func (emptyEngine) VolumeInspect(_ context.Context, id string, _ client.VolumeInspectOptions) (client.VolumeInspectResult, error) {
	return client.VolumeInspectResult{}, uninstall.NotFoundError{What: id}
}

func (emptyEngine) VolumeRemove(_ context.Context, id string, _ client.VolumeRemoveOptions) (client.VolumeRemoveResult, error) {
	return client.VolumeRemoveResult{}, uninstall.NotFoundError{What: id}
}

func (emptyEngine) DiskUsage(context.Context, client.DiskUsageOptions) (client.DiskUsageResult, error) {
	return client.DiskUsageResult{}, uninstall.NotFoundError{What: "disk usage"}
}

func TestUninstallOfA31InstallWithNoUpgradeRemovesEachKeychainItem(t *testing.T) {
	owner, _, k, home := legacyInstall(t, map[string]string{
		keys.OwnerAccount("mai"):           testOwnerKey,
		keys.AgentAccount("mai", "cursor"): legacyCursorKey,
		keys.OwnerAccount("mai-dev"):       "mai1:owner:devsecret",
	})
	dirs, err := paths.Default()
	if err != nil {
		t.Fatal(err)
	}
	// The same key file as runUninstall: openKeys, with the keychain in memory.
	kf, err := openKeys()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	u := &uninstall.Uninstaller{Engine: emptyEngine{}, Keys: kf, Dirs: dirs, Yes: true, Out: &out}
	if code := u.Run(context.Background()); code != 0 {
		t.Fatalf("mai uninstall: exit %d\n%s", code, out.String())
	}
	text := out.String()
	noSecret(t, "mai uninstall", text)
	// The end of the leases uses the owner key of the keychain item. The fake owner API has
	// one leased session.
	if !strings.Contains(text, "Leases: 1 ended.") || !strings.Contains(strings.Join(owner.calls, ","), "DELETE /api/sessions/cursor.s2/lease") {
		t.Fatalf("the leases did not end with the owner key: calls %v\n%s", owner.calls, text)
	}
	for _, line := range []string{
		"gone     key owner@mai",
		"gone     keychain dev.mai / owner@mai",
		"gone     keychain dev.mai / agent:cursor@mai",
	} {
		if !strings.Contains(text, line+"\n") {
			t.Fatalf("the output has no line %q:\n%s", line, text)
		}
	}
	if got := strings.Join(k.legacy.Accounts(), ","); got != "owner@mai-dev" {
		t.Fatalf("after mai uninstall, the keychain items are %s", got)
	}
	if _, err := os.Stat(k.file.Path()); !os.IsNotExist(err) {
		t.Fatalf("the key file stays after mai uninstall: %v", err)
	}
	for _, d := range []string{"config", "state", "cache"} {
		if _, err := os.Stat(filepath.Join(home, "maihome", d)); !os.IsNotExist(err) {
			t.Fatalf("the folder %s stays after mai uninstall", d)
		}
	}
}
