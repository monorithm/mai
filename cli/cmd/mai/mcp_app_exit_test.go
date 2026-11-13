package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/monorithm/mai/cli/v3/internal/keys"
)

const exitTestKey = "mai1:cursor:abcdefghijklmnopqrstuvwxyz234567"

// exitFake is a small lease and MCP server. A call of wait_for_mail waits until release
// closes.
type exitFake struct {
	mu      sync.Mutex
	n       int
	live    map[string]string // lease -> session
	reached chan struct{}
	release chan struct{}
}

func (f *exitFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/leases":
		if token != exitTestKey {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":{"code":"BAD_KEY","message":"test"}}`))
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		session := "cursor.s1"
		if name, ok := body["session"].(string); ok {
			session = name
			if !strings.Contains(name, ".") {
				session = "cursor." + name
			}
		}
		f.mu.Lock()
		for _, s := range f.live {
			if s == session {
				f.mu.Unlock()
				w.WriteHeader(409)
				_, _ = w.Write([]byte(`{"error":{"code":"SESSION_BUSY","message":"test"}}`))
				return
			}
		}
		f.n++
		lease := fmt.Sprintf("lease_%043d", f.n)
		f.live[lease] = session
		f.mu.Unlock()
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{"lease": lease, "session": session, "heartbeat_seconds": 30})
	case r.Method == http.MethodPost && r.URL.Path == "/api/leases/current/heartbeat":
		f.mu.Lock()
		s, ok := f.live[token]
		f.mu.Unlock()
		if !ok {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":{"code":"LEASE_UNKNOWN","message":"test"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"session": s, "new_mail": 0})
	case r.Method == http.MethodDelete && r.URL.Path == "/api/leases/current":
		f.mu.Lock()
		delete(f.live, token)
		f.mu.Unlock()
		w.WriteHeader(204)
	case r.Method == http.MethodPost && r.URL.Path == "/mcp":
		var msg map[string]any
		_ = json.NewDecoder(r.Body).Decode(&msg)
		if _, ok := msg["id"]; !ok {
			w.WriteHeader(202)
			return
		}
		f.mu.Lock()
		s, ok := f.live[token]
		f.mu.Unlock()
		if !ok {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":{"code":"LEASE_UNKNOWN","message":"test"}}`))
			return
		}
		result := map[string]any{}
		if p, _ := msg["params"].(map[string]any); msg["method"] == "tools/call" {
			if p["name"] == "wait_for_mail" {
				f.reached <- struct{}{}
				<-f.release
			}
			result = map[string]any{"structuredContent": map[string]any{"login": s}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": msg["id"], "result": result})
	default:
		w.WriteHeader(404)
	}
}

func (f *exitFake) sessions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []string{}
	for _, s := range f.live {
		out = append(out, s)
	}
	return out
}

// TestBridgeHelperForTheAppExit runs "mai mcp --agent cursor" in a child process of a test,
// with the scratch folder of MAI_TEST_BRIDGE_HOME as MAI_HOME. The bridge then uses the real
// standard input, output, and error of the process.
func TestBridgeHelperForTheAppExit(t *testing.T) {
	home := os.Getenv("MAI_TEST_BRIDGE_HOME")
	if home == "" {
		t.Skip("only a child process of a test runs the bridge")
	}
	// TestMain made a scratch MAI_HOME for this process. The bridge uses the folder of the
	// parent test in its place.
	_ = os.RemoveAll(os.Getenv("MAI_HOME"))
	_ = os.Setenv("MAI_HOME", home)
	openKeys = func() (*keys.File, error) { return keys.Open(filepath.Join(home, "state"), nil), nil }
	os.Exit(runMCP([]string{"--agent", "cursor"}))
}

// When an app exits, the kernel closes the app end of each pipe of the bridge. A chat can
// have a call in flight at that time, for example wait_for_mail. The bridge must then end
// each of its leases and remove its notice file, as for each other stop. Else the server
// keeps the lease of each named session until the lease expires, and a new bridge for the
// same session gets SESSION_BUSY.
func TestTheBridgeEndsEachLeaseWhenTheAppExitsDuringACall(t *testing.T) {
	appExitDuringACall(t, false)
}

// An app can also send SIGTERM to the bridge when it exits. The bridge then stops the call
// in flight, and it must end each of its leases also when the app end of each pipe is closed.
func TestTheBridgeEndsEachLeaseAtSIGTERMWhenTheAppExitsDuringACall(t *testing.T) {
	appExitDuringACall(t, true)
}

func appExitDuringACall(t *testing.T, term bool) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the test uses the signals of Unix")
	}
	f := &exitFake{live: map[string]string{}, reached: make(chan struct{}, 1), release: make(chan struct{})}
	ts := httptest.NewServer(f)
	defer ts.Close()
	port := ts.Listener.Addr().(*net.TCPAddr).Port

	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := fmt.Sprintf(`{"instance":"probe","port":%d}`, port)
	if err := os.WriteFile(filepath.Join(home, "config", "config.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(home, "state")
	if err := keys.Open(state, nil).Set(keys.AgentAccount("probe", "cursor"), exitTestKey); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestBridgeHelperForTheAppExit$", "-test.count=1")
	cmd.Env = append(os.Environ(), "MAI_TEST_BRIDGE_HOME="+home)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	outR, outW, _ := os.Pipe()
	errR, errW, _ := os.Pipe()
	cmd.Stdout, cmd.Stderr = outW, errW
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = outW.Close()
	_ = errW.Close()
	// The app reads the log of the bridge until it exits.
	go func() { _, _ = bufio.NewReader(errR).WriteTo(discard{}) }()
	out := bufio.NewReader(outR)
	send := func(line string) {
		if _, err := stdin.Write([]byte(line + "\n")); err != nil {
			t.Fatal(err)
		}
	}
	recv := func() string {
		line, err := out.ReadString('\n')
		if err != nil {
			t.Fatalf("no answer of the bridge: %v", err)
		}
		return line
	}
	send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	recv()
	send(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"whoami","arguments":{"session":"lane-a"}}}`)
	if a := recv(); !strings.Contains(a, "cursor.lane-a") {
		t.Fatalf("whoami with the label lane-a = %s", a)
	}
	send(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"wait_for_mail","arguments":{"session":"lane-a","timeout_sec":30}}}`)
	select {
	case <-f.reached:
	case <-time.After(10 * time.Second):
		t.Fatal("the wait did not come to the server")
	}
	notice := filepath.Join(state, "notices", fmt.Sprintf("%d-cursor.json", os.Getpid()))
	deadline := time.Now().Add(5 * time.Second)
	for _, err := os.Stat(notice); err != nil && time.Now().Before(deadline); _, err = os.Stat(notice) {
		time.Sleep(20 * time.Millisecond)
	}

	// The app exits: its ends of the three pipes close. Then the wait gets its answer.
	_ = stdin.Close()
	_ = outR.Close()
	_ = errR.Close()
	if term {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		time.Sleep(500 * time.Millisecond)
	}
	close(f.release)

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err = <-done:
	case <-time.After(60 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("the bridge did not stop")
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			t.Errorf("the bridge stopped with the signal %v, before it ended its leases", ws.Signal())
		}
	}
	deadline = time.Now().Add(5 * time.Second)
	for len(f.sessions()) > 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if left := f.sessions(); len(left) > 0 {
		t.Errorf("after the stop of the bridge, the server has the leases of the sessions %q", left)
	}
	if _, err := os.Stat(notice); err == nil {
		t.Errorf("the notice file %s stays after the stop of the bridge", notice)
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
