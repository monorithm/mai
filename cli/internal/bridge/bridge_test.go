package bridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/monorithm/mai/cli/internal/keychain"
)

const agentKey = "mai1:cursor:abcdefghijklmnopqrstuvwxyz234567"

// fakeServer speaks the lease and MCP contract of the server.
type fakeServer struct {
	mu        sync.Mutex
	valid     map[string]string // lease -> session
	n         int
	refuseNew bool
	ended     []string
	leaseReqs []map[string]any
}

func writeErr(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":{"code":"` + code + `","message":"test"}}`))
}

func bearer(r *http.Request) string { return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") }

func (f *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/leases":
		if bearer(r) != agentKey {
			writeErr(w, 401, "BAD_KEY")
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.leaseReqs = append(f.leaseReqs, body)
		if f.refuseNew && f.n > 0 {
			writeErr(w, 409, "NO_FREE_SESSION")
			return
		}
		f.n++
		lease := "lease_" + strings.Repeat(string(rune('a'+f.n)), 43)
		session := "cursor.s" + string(rune('0'+f.n))
		f.valid[lease] = session
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{"lease": lease, "session": session, "heartbeat_seconds": 1, "expires_seconds": 90})
	case r.Method == http.MethodPost && r.URL.Path == "/api/leases/current/heartbeat":
		s, ok := f.valid[bearer(r)]
		if !ok {
			writeErr(w, 401, "LEASE_UNKNOWN")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"session": s, "new_mail": 3})
	case r.Method == http.MethodDelete && r.URL.Path == "/api/leases/current":
		if _, ok := f.valid[bearer(r)]; !ok {
			writeErr(w, 401, "LEASE_UNKNOWN")
			return
		}
		delete(f.valid, bearer(r))
		f.ended = append(f.ended, bearer(r))
		w.WriteHeader(204)
	case r.Method == http.MethodPost && r.URL.Path == "/mcp":
		var msg map[string]any
		_ = json.NewDecoder(r.Body).Decode(&msg)
		if msg["method"] == "notifications/initialized" {
			w.WriteHeader(202)
			return
		}
		s, ok := f.valid[bearer(r)]
		if !ok && msg["method"] == "tools/call" {
			writeErr(w, 401, "LEASE_UNKNOWN")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": msg["id"], "result": map[string]any{"session": s}})
	default:
		writeErr(w, 404, "NOT_FOUND")
	}
}

// expire ends each lease on the server side, as a restart of the container does.
func (f *fakeServer) expire() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.valid = map[string]string{}
}

type harness struct {
	srv      *fakeServer
	b        *Bridge
	in       *io.PipeWriter
	out      *bufio.Reader
	stderr   *bytes.Buffer
	checks   int
	done     chan int
	stateDir string
}

func newHarness(t *testing.T, store keychain.Store) *harness {
	t.Helper()
	h := &harness{srv: &fakeServer{valid: map[string]string{}}, stderr: &bytes.Buffer{}, stateDir: t.TempDir()}
	ts := httptest.NewServer(h.srv)
	t.Cleanup(ts.Close)
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	h.in, h.out = inW, bufio.NewReader(outR)
	h.b = &Bridge{
		Agent: "cursor", Instance: "mai", Endpoint: ts.URL, Store: store, Client: ts.Client(),
		StateDir: h.stateDir, App: "Cursor", AppPID: 4242, PID: os.Getpid(), Folder: "/work/project",
		Stdin: inR, Stdout: outW, Stderr: h.stderr,
		Presence: func(reason string) error { h.checks++; return nil },
	}
	h.done = make(chan int, 1)
	t.Cleanup(func() { _ = inW.Close(); _ = outW.Close() })
	return h
}

func (h *harness) start(ctx context.Context) {
	go func() {
		code := h.b.Run(ctx)
		_ = h.b.Stdout.(*io.PipeWriter).Close()
		h.done <- code
	}()
}

func (h *harness) send(t *testing.T, line string) {
	t.Helper()
	if _, err := io.WriteString(h.in, line+"\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func (h *harness) recv(t *testing.T) map[string]any {
	t.Helper()
	line, err := h.out.ReadString('\n')
	if err != nil {
		t.Fatalf("read: %v (stderr: %s)", err, h.stderr.String())
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("answer %q is not JSON: %v", line, err)
	}
	return m
}

func (h *harness) wait(t *testing.T) int {
	t.Helper()
	select {
	case code := <-h.done:
		return code
	case <-time.After(10 * time.Second):
		t.Fatal("the bridge did not stop")
		return -1
	}
}

func memoryWithKey() keychain.Store {
	m := keychain.NewMemory()
	_ = m.Set(keychain.Service, keychain.AgentAccount("mai", "cursor"), agentKey)
	return m
}

func noticePath(h *harness) string {
	return filepath.Join(h.stateDir, "notices", "4242-cursor.json")
}

func waitForFile(t *testing.T, path string) []byte {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
			return data
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no file %s", path)
	return nil
}

func TestBridgeLeasesBridgesAndEnds(t *testing.T) {
	h := newHarness(t, memoryWithKey())
	h.start(context.Background())

	h.send(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	if a := h.recv(t); a["id"] != float64(1) {
		t.Fatalf("answer 1 = %v", a)
	}
	h.send(t, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	h.send(t, `{"jsonrpc":"2.0","id":"w","method":"tools/call","params":{"name":"whoami"}}`)
	a := h.recv(t)
	if a["id"] != "w" || a["result"].(map[string]any)["session"] != "cursor.s1" {
		t.Fatalf("answer w = %v", a)
	}

	data := waitForFile(t, noticePath(h))
	if bytes.Contains(data, []byte("lease_")) || bytes.Contains(data, []byte("mai1:")) {
		t.Fatalf("the notice file holds a secret: %s", data)
	}
	var n map[string]any
	if err := json.Unmarshal(data, &n); err != nil || n["session"] != "cursor.s1" || n["new_mail"] != float64(3) {
		t.Fatalf("notice = %s", data)
	}
	if _, err := time.Parse(time.RFC3339, n["updated_at"].(string)); err != nil {
		t.Fatalf("updated_at: %v", err)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(noticePath(h))
		di, _ := os.Stat(filepath.Dir(noticePath(h)))
		if fi.Mode().Perm() != 0o600 || di.Mode().Perm() != 0o700 {
			t.Fatalf("modes = %v and %v", fi.Mode().Perm(), di.Mode().Perm())
		}
	}

	_ = h.in.Close()
	if code := h.wait(t); code != ExitOK {
		t.Fatalf("exit = %d, stderr: %s", code, h.stderr.String())
	}
	if _, err := os.Stat(noticePath(h)); !os.IsNotExist(err) {
		t.Fatalf("the notice file is still there: %v", err)
	}
	h.srv.mu.Lock()
	defer h.srv.mu.Unlock()
	if len(h.srv.ended) != 1 || len(h.srv.valid) != 0 {
		t.Fatalf("ended = %v, valid = %v", h.srv.ended, h.srv.valid)
	}
	req := h.srv.leaseReqs[0]
	if req["app"] != "Cursor" || req["folder"] != "/work/project" || req["pid"] != float64(os.Getpid()) {
		t.Fatalf("lease request = %v", req)
	}
	if h.checks != 1 {
		t.Fatalf("presence checks = %d, want 1", h.checks)
	}
}

func TestBridgeGetsANewLeaseOneTime(t *testing.T) {
	h := newHarness(t, memoryWithKey())
	h.start(context.Background())
	h.send(t, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	h.recv(t)
	h.srv.expire()
	h.send(t, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"whoami"}}`)
	a := h.recv(t)
	if a["id"] != float64(2) || a["result"] == nil {
		t.Fatalf("answer = %v", a)
	}
	_ = h.in.Close()
	if code := h.wait(t); code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	if h.checks != 2 {
		t.Fatalf("presence checks = %d, want 2", h.checks)
	}
}

func TestBridgeStopsWhenNoNewLeaseIsAvailable(t *testing.T) {
	h := newHarness(t, memoryWithKey())
	h.srv.refuseNew = true
	h.start(context.Background())
	h.send(t, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	h.recv(t)
	h.srv.expire()
	h.send(t, `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"whoami"}}`)
	a := h.recv(t)
	if a["id"] != float64(7) || a["error"] == nil {
		t.Fatalf("answer = %v", a)
	}
	if code := h.wait(t); code != ExitError {
		t.Fatalf("exit = %d", code)
	}
}

func TestBridgeNamesTheAgentAddCommandWhenTheKeyIsMissing(t *testing.T) {
	h := newHarness(t, keychain.NewMemory())
	h.start(context.Background())
	if code := h.wait(t); code != ExitError {
		t.Fatalf("exit = %d", code)
	}
	lines := strings.Split(strings.TrimSpace(h.stderr.String()), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], `mai agent add cursor`) {
		t.Fatalf("stderr = %q", h.stderr.String())
	}
	if len(h.srv.leaseReqs) != 0 {
		t.Fatal("the bridge asked for a lease with no key")
	}
}

func TestBridgeEndsTheLeaseWhenTheContextEnds(t *testing.T) {
	h := newHarness(t, memoryWithKey())
	ctx, cancel := context.WithCancel(context.Background())
	h.start(ctx)
	h.send(t, `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	h.recv(t)
	cancel()
	if code := h.wait(t); code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	h.srv.mu.Lock()
	defer h.srv.mu.Unlock()
	if len(h.srv.ended) != 1 {
		t.Fatalf("ended = %v", h.srv.ended)
	}
}

func TestReasonNamesTheAgentAppFolderAndPID(t *testing.T) {
	b := &Bridge{Agent: "cursor", App: "Cursor", Folder: "/work/p", PID: 99}
	r := b.Reason()
	for _, want := range []string{"cursor", "Cursor", "/work/p", "99"} {
		if !strings.Contains(r, want) {
			t.Errorf("reason %q has no %q", r, want)
		}
	}
}

func TestLoadConfigGivesTheDefaults(t *testing.T) {
	dir := t.TempDir()
	c, err := LoadConfig(dir)
	if err != nil || c != DefaultConfig() || c.Endpoint() != "http://127.0.0.1:8765" {
		t.Fatalf("config = %+v, %v", c, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"instance":"dev","port":18790}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err = LoadConfig(dir)
	if err != nil || c.Instance != "dev" || c.Port != 18790 || c.Approval != "each-bridge" {
		t.Fatalf("config = %+v, %v", c, err)
	}
}

func TestSSEDataGivesOneLineForEachEvent(t *testing.T) {
	got := sseData([]byte("event: message\ndata: {\"id\": 1}\n\ndata: {\"id\":2}\n\n"))
	if len(got) != 2 || string(got[0]) != `{"id":1}` || string(got[1]) != `{"id":2}` {
		t.Fatalf("got %q", got)
	}
}
