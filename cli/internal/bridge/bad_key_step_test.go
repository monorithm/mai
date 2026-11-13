package bridge

import (
	"context"
	"strings"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/keys"
)

// When the key file has no key for an agent that exists on the server, "mai agent add NAME"
// gives AGENT_EXISTS and writes no key. Only "mai agent rotate NAME" writes a new key for
// that agent. Thus the hint of BAD_KEY must also give the step for an agent that exists, at
// a call with a label and at the start of the bridge.
func TestTheHintOfAMissingAgentKeyGivesAStepForAnAgentThatExists(t *testing.T) {
	h := startNamed(t)
	h.keys.set(keys.AgentAccount("mai", "cursor"), "")
	e := toolErr(t, h.call(t, 1, "whoami", map[string]any{"session": "lane-a"}))
	msg, _ := e["message"].(string)
	if e["code"] != "BAD_KEY" || !strings.Contains(msg, "mai agent rotate cursor") || !strings.Contains(msg, "mai agent add cursor") {
		t.Errorf("a call with a label and no agent key gives %v; the message has no step for an agent that exists", e)
	}
	h.stop(t)

	h2 := newHarness(t, newTestKeys())
	h2.start(context.Background())
	if code := h2.wait(t); code != ExitError {
		t.Fatalf("exit = %d", code)
	}
	if text := h2.stderr.String(); !strings.Contains(text, "mai agent rotate cursor") || !strings.Contains(text, "mai agent add cursor") {
		t.Errorf("the start of the bridge with no agent key tells: %q; it has no step for an agent that exists", text)
	}
}
