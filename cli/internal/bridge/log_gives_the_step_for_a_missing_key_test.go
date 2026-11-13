package bridge

import (
	"context"
	"strings"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/keys"
)

// wantStepInLog stops the test when the log of the bridge after the start does not give the
// steps for an agent key that the key file does not have.
func wantStepInLog(t *testing.T, h *harness, after int, what string) {
	t.Helper()
	text := h.stderr.String()[after:]
	if !strings.Contains(text, "mai agent rotate cursor") || !strings.Contains(text, "mai agent add cursor") {
		t.Errorf("%s: the log of the bridge does not give the steps: %q", what, strings.TrimSpace(text))
	}
}

// The key file can lose the agent key while the bridge runs, for example after "mai agent
// remove". The tool error and the stop of the bridge give the steps "mai agent rotate NAME"
// and "mai agent add NAME". The log of the bridge must also give these steps, as at the
// start of the bridge.
func TestTheLogGivesTheStepWhenTheKeyFileLosesTheKeyOfANamedSession(t *testing.T) {
	h := newHarness(t, memoryWithKey())
	h.srv.hbSeconds = 60
	h.start(context.Background())
	if r := result(t, h.call(t, 1, "whoami", map[string]any{"session": "lane-a"})); r["session"] != "cursor.lane-a" {
		t.Fatalf("whoami with the label lane-a = %v", r)
	}
	h.firstBeats(t, "cursor.s1", "cursor.lane-a")
	after := len(h.stderr.String())
	h.keys.set(keys.AgentAccount("mai", "cursor"), "")
	h.srv.expire()
	e := toolErr(t, h.call(t, 2, "whoami", map[string]any{"session": "lane-a"}))
	if e["code"] != "BAD_KEY" || !strings.Contains(e["message"].(string), "mai agent rotate cursor") {
		t.Fatalf("the tool error = %v", e)
	}
	wantStepInLog(t, h, after, "the end of the named session")
	_ = h.in.Close()
	h.wait(t)
}

func TestTheLogGivesTheStepWhenTheKeyFileLosesTheKeyAtAHeartbeatOfThePool(t *testing.T) {
	h := newHarness(t, memoryWithKey())
	h.start(context.Background())
	h.firstBeats(t, "cursor.s1")
	after := len(h.stderr.String())
	h.keys.set(keys.AgentAccount("mai", "cursor"), "")
	h.srv.expire()
	a := h.recv(t)
	if e, _ := a["error"].(map[string]any); e == nil || !strings.Contains(e["message"].(string), "mai agent rotate cursor") {
		t.Fatalf("the stop of the bridge = %v", a)
	}
	if code := h.wait(t); code != ExitError {
		t.Fatalf("exit = %d", code)
	}
	wantStepInLog(t, h, after, "the stop at a heartbeat")
}

func TestTheLogGivesTheStepWhenTheKeyFileLosesTheKeyAtACallOnThePool(t *testing.T) {
	h := newHarness(t, memoryWithKey())
	h.srv.hbSeconds = 60
	h.start(context.Background())
	h.firstBeats(t, "cursor.s1")
	after := len(h.stderr.String())
	h.keys.set(keys.AgentAccount("mai", "cursor"), "")
	h.srv.expire()
	h.send(t, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"whoami"}}`)
	a := h.recv(t)
	if e, _ := a["error"].(map[string]any); e == nil || !strings.Contains(e["message"].(string), "mai agent rotate cursor") {
		t.Fatalf("the stop of the bridge = %v", a)
	}
	if code := h.wait(t); code != ExitError {
		t.Fatalf("exit = %d", code)
	}
	wantStepInLog(t, h, after, "the stop at a call")
}

// The same rule applies when the server refuses the agent key at a call on the pool.
func TestTheLogGivesTheStepWhenTheServerRefusesTheKeyAtACallOnThePool(t *testing.T) {
	h := newHarness(t, memoryWithKey())
	h.srv.hbSeconds = 60
	h.start(context.Background())
	h.firstBeats(t, "cursor.s1")
	after := len(h.stderr.String())
	h.srv.mu.Lock()
	h.srv.refuseKey = true
	h.srv.mu.Unlock()
	h.srv.expire()
	h.send(t, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"whoami"}}`)
	a := h.recv(t)
	if e, _ := a["error"].(map[string]any); e == nil || !strings.Contains(e["message"].(string), "mai agent rotate cursor") {
		t.Fatalf("the stop of the bridge = %v", a)
	}
	if code := h.wait(t); code != ExitError {
		t.Fatalf("exit = %d", code)
	}
	wantStepInLog(t, h, after, "the stop at a call")
}
