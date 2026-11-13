package main

import (
	"bufio"
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/monorithm/mai/cli/v3/internal/keys"
)

// When an app exits, the kernel closes the app end of each pipe of the bridge. A call of the
// chat can then be in flight on the server, for example wait_for_mail with timeout_sec 50.
// Nothing can read its result. The bridge must end its leases when the app exits, not only
// at the end of that call. Else the bridge keeps the lease of each named session, and a new
// bridge of the app (for example after a restart of the app) gets SESSION_BUSY for the same
// session until the call ends.
func TestTheBridgeEndsEachLeaseWhenTheAppExitsAndTheCallDoesNotEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the test uses the pipes of Unix")
	}
	f := &exitFake{live: map[string]string{}, reached: make(chan struct{}, 1), release: make(chan struct{})}
	ts := httptest.NewServer(f)
	defer ts.Close()
	released := false
	release := func() {
		if !released {
			released = true
			close(f.release)
		}
	}
	defer release()
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
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	_ = outW.Close()
	_ = errW.Close()
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
	send(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"wait_for_mail","arguments":{"session":"lane-a","timeout_sec":50}}}`)
	select {
	case <-f.reached:
	case <-time.After(10 * time.Second):
		t.Fatal("the wait did not come to the server")
	}

	// The app exits: its ends of the three pipes close. The wait stays on the server.
	_ = stdin.Close()
	_ = outR.Close()
	_ = errR.Close()
	deadline := time.Now().Add(15 * time.Second)
	for len(f.sessions()) > 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if left := f.sessions(); len(left) > 0 {
		t.Errorf("15 s after the exit of the app, the server has the leases of the sessions %q, because the call did not end", left)
	}
	release()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("the bridge did not stop")
	}
}
