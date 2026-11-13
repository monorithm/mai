package bridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monorithm/mai/cli/v3/internal/keychain"
)

const agentKey = "mai1:cursor:abcdefghijklmnopqrstuvwxyz234567"

// fakeServer speaks the lease and MCP contract of the server, with named sessions.
type fakeServer struct {
	mu        sync.Mutex
	valid     map[string]string // lease -> session
	n         int
	refuseNew bool
	ended     []string
	leaseReqs []map[string]any
	named     map[string]string // login of a named session -> "active" or "revoked"
	beats     map[string]int    // session -> count of heartbeats
	mail      map[string]int    // session -> count of new mail; the default is 3
	mcpCalls  []map[string]any  // each message that reached /mcp
	hbSeconds int               // heartbeat_seconds of each new lease; the default is 1
	block     chan struct{}     // when set, wait_for_mail waits until it closes
}

func newFakeServer() *fakeServer {
	return &fakeServer{
		valid: map[string]string{},
		named: map[string]string{"cursor.lane-a": "active", "cursor.lane-b": "active", "cursor.lane-c": "active",
			"cursor.gone": "revoked", "codex.lane-a": "active"},
		beats: map[string]int{},
		mail:  map[string]int{},
	}
}

func writeErr(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":{"code":"` + code + `","message":"test"}}`))
}

func bearer(r *http.Request) string {
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}

var testLabelRule = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// grant gives a new lease on session.
func (f *fakeServer) grant(w http.ResponseWriter, session string) {
	f.n++
	lease := fmt.Sprintf("lease_%043d", f.n)
	f.valid[lease] = session
	hb := f.hbSeconds
	if hb == 0 {
		hb = 1
	}
	w.WriteHeader(201)
	_ = json.NewEncoder(w).Encode(map[string]any{"lease": lease, "session": session, "heartbeat_seconds": hb, "expires_seconds": 90})
}

// leased is true when a live lease holds session.
func (f *fakeServer) leased(session string) bool {
	for _, s := range f.valid {
		if s == session {
			return true
		}
	}
	return false
}

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
		if name, ok := body["session"].(string); ok {
			login := name
			if !strings.Contains(name, ".") {
				if !testLabelRule.MatchString(name) {
					writeErr(w, 400, "BAD_LABEL")
					return
				}
				login = "cursor." + name
			}
			switch {
			case !strings.HasPrefix(login, "cursor.") || f.named[login] == "":
				writeErr(w, 404, "SESSION_UNKNOWN")
			case f.named[login] == "revoked":
				writeErr(w, 409, "SESSION_REVOKED")
			case f.leased(login):
				writeErr(w, 409, "SESSION_BUSY")
			default:
				f.grant(w, login)
			}
			return
		}
		if f.refuseNew && f.n > 0 {
			writeErr(w, 409, "NO_FREE_SESSION")
			return
		}
		f.grant(w, fmt.Sprintf("cursor.s%d", f.n+1))
	case r.Method == http.MethodPost && r.URL.Path == "/api/leases/current/heartbeat":
		s, ok := f.valid[bearer(r)]
		if !ok {
			writeErr(w, 401, "LEASE_UNKNOWN")
			return
		}
		f.beats[s]++
		n, ok := f.mail[s]
		if !ok {
			n = 3
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"session": s, "new_mail": n})
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
		f.mcpCalls = append(f.mcpCalls, msg)
		if msg["method"] == "notifications/initialized" {
			w.WriteHeader(202)
			return
		}
		if p, ok := msg["params"].(map[string]any); ok && p["name"] == "wait_for_mail" && f.block != nil {
			block := f.block
			f.mu.Unlock()
			<-block
			f.mu.Lock()
		}
		s, ok := f.valid[bearer(r)]
		if !ok && msg["method"] == "tools/call" {
			writeErr(w, 401, "LEASE_UNKNOWN")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		result := map[string]any{"session": s}
		if p, ok := msg["params"].(map[string]any); ok {
			if name := p["name"]; name == "login" || name == "logout" {
				result = map[string]any{"isError": true, "structuredContent": map[string]any{"error": map[string]any{"code": "BRIDGE_ONLY"}}}
			} else if args, ok := p["arguments"].(map[string]any); ok {
				result["arguments"] = args
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": msg["id"], "result": result})
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
	stderr   *syncBuffer
	checks   atomic.Int32
	done     chan int
	stateDir string
}

// syncBuffer is a bytes.Buffer that many goroutines can write.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func newHarness(t *testing.T, store keychain.Store) *harness {
	t.Helper()
	h := &harness{srv: newFakeServer(), stderr: &syncBuffer{}, stateDir: t.TempDir()}
	ts := httptest.NewServer(h.srv)
	t.Cleanup(ts.Close)
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	h.in, h.out = inW, bufio.NewReader(outR)
	h.b = &Bridge{
		Agent: "cursor", Instance: "mai", Endpoint: ts.URL, Store: store, Client: ts.Client(),
		StateDir: h.stateDir, App: "Cursor", AppPID: 4242, PID: os.Getpid(), Folder: "/work/project",
		Stdin: inR, Stdout: outW, Stderr: h.stderr,
		Presence: func(reason string) error { h.checks.Add(1); return nil },
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
	if h.checks.Load() != 1 {
		t.Fatalf("presence checks = %d, want 1", h.checks.Load())
	}
}

func TestBridgeGetsANewLeaseWithNoNewPresenceCheck(t *testing.T) {
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
	h.srv.mu.Lock()
	defer h.srv.mu.Unlock()
	if len(h.srv.leaseReqs) != 2 {
		t.Fatalf("lease requests = %v", h.srv.leaseReqs)
	}
	if h.checks.Load() != 1 {
		t.Fatalf("presence checks = %d, want 1", h.checks.Load())
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
	// The heartbeat can find the end of the lease first. Then an error with no id comes
	// before the answer of the call.
	var answered bool
	for _, a := range h.recvAll(t) {
		if a["id"] == float64(7) {
			if a["error"] == nil {
				t.Fatalf("answer = %v", a)
			}
			answered = true
		}
	}
	if !answered {
		t.Fatal("no answer for the call")
	}
	if code := h.wait(t); code != ExitError {
		t.Fatalf("exit = %d", code)
	}
}

// recvAll reads each answer until the bridge closes stdout.
func (h *harness) recvAll(t *testing.T) []map[string]any {
	t.Helper()
	var all []map[string]any
	for {
		line, err := h.out.ReadString('\n')
		if err != nil {
			return all
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("answer %q is not JSON: %v", line, err)
		}
		all = append(all, m)
	}
}

func TestBridgeNamesTheAgentAddCommandWhenTheKeyIsNotThere(t *testing.T) {
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
