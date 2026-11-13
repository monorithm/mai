package bridge

import (
	"context"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/keys"
)

// A key file that other users can read is a fault of the host, not of the server. The
// next try fails in the same way until the user runs the chmod command. Thus a call with a
// label must not get SERVER_UNAVAILABLE with retry_safe true, which tells the chat that the
// server does not reply and that a new try after some time can succeed.
func TestAKeyFileThatOthersCanReadIsNotAServerFailureForACallWithALabel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no Unix mode")
	}
	f := keys.Open(t.TempDir(), nil)
	if err := f.Set(keys.AgentAccount("mai", "cursor"), agentKey); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, newTestKeys())
	h.b.Keys = f
	h.start(context.Background())
	if r := result(t, h.call(t, 1, "whoami", nil)); r["session"] != "cursor.s1" {
		t.Fatalf("whoami = %v", r)
	}
	if err := os.Chmod(f.Path(), 0o644); err != nil {
		t.Fatal(err)
	}
	e := toolErr(t, h.call(t, 2, "whoami", map[string]any{"session": "lane-a"}))
	login := toolErr(t, h.call(t, 3, "login", map[string]any{"session": "lane-b"}))
	_ = os.Chmod(f.Path(), 0o600)
	if e["code"] == "SERVER_UNAVAILABLE" || e["retry_safe"] == true {
		t.Errorf("a call with a label and a key file with the mode 0644 gives the code %v with retry_safe %v",
			e["code"], e["retry_safe"])
	}
	for _, got := range []map[string]any{e, login} {
		msg, _ := got["message"].(string)
		if got["code"] != "KEY_FILE_ERROR" || got["retry_safe"] != false || got["path"] != "session" ||
			!strings.Contains(msg, "chmod 600") || strings.Contains(msg, agentKey) {
			t.Errorf("tool error = %v, want KEY_FILE_ERROR with the path session, retry_safe false, and the chmod command", got)
		}
	}
	// After the chmod command, the same call operates.
	if r := result(t, h.call(t, 4, "whoami", map[string]any{"session": "lane-a"})); r["session"] != "cursor.lane-a" {
		t.Fatalf("whoami after the chmod command = %v", r)
	}
	h.stop(t)
}

// A call with no session uses the pool lease. When the pool lease ends and the read of the
// agent key fails, the tool error has no path, because the call has no argument session.
func TestAKeyFileErrorOfAPoolCallHasNoPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no Unix mode")
	}
	f := keys.Open(t.TempDir(), nil)
	if err := f.Set(keys.AgentAccount("mai", "cursor"), agentKey); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, newTestKeys())
	h.b.Keys = f
	h.srv.hbSeconds = 60
	h.start(context.Background())
	if r := result(t, h.call(t, 1, "whoami", nil)); r["session"] != "cursor.s1" {
		t.Fatalf("whoami = %v", r)
	}
	if err := os.Chmod(f.Path(), 0o644); err != nil {
		t.Fatal(err)
	}
	h.srv.expire()
	e := toolErr(t, h.call(t, 2, "whoami", nil))
	_ = os.Chmod(f.Path(), 0o600)
	if e["code"] != "KEY_FILE_ERROR" || e["retry_safe"] != false || e["path"] != nil {
		t.Errorf("a pool call with a key file with the mode 0644 gives %v, want KEY_FILE_ERROR with no path and retry_safe false", e)
	}
	if r := result(t, h.call(t, 3, "whoami", nil)); r["session"] == nil {
		t.Fatalf("whoami after the chmod command = %v", r)
	}
	h.stop(t)
}

// A key file that is not valid JSON also needs a step of the user.
func TestAKeyFileThatIsNotValidJSONGivesKeyFileError(t *testing.T) {
	f := keys.Open(t.TempDir(), nil)
	if err := f.Set(keys.AgentAccount("mai", "cursor"), agentKey); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, newTestKeys())
	h.b.Keys = f
	h.start(context.Background())
	if r := result(t, h.call(t, 1, "whoami", nil)); r["session"] != "cursor.s1" {
		t.Fatalf("whoami = %v", r)
	}
	if err := os.WriteFile(f.Path(), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	e := toolErr(t, h.call(t, 2, "whoami", map[string]any{"session": "lane-a"}))
	msg, _ := e["message"].(string)
	if e["code"] != "KEY_FILE_ERROR" || e["retry_safe"] != false || e["path"] != "session" || !strings.Contains(msg, "not valid JSON") {
		t.Errorf("tool error = %v, want KEY_FILE_ERROR with the step of the key file", e)
	}
	h.stop(t)
}

// failingKeys is a key source whose read gives err.
type failingKeys struct{ err error }

func (k failingKeys) Get(string) (string, error) { return "", k.err }

// A refused read of the legacy keychain item needs a step of the user. A login gets the same
// tool error as a call with a label.
func TestARefusedReadOfALegacyItemGivesKeyFileError(t *testing.T) {
	h := startNamed(t)
	h.b.Keys = failingKeys{err: &keys.LegacyError{Account: keys.AgentAccount("mai", "cursor"), Err: errors.New("the user refused the prompt")}}
	for i, name := range []string{"login", "whoami"} {
		e := toolErr(t, h.call(t, i+1, name, map[string]any{"session": "lane-a"}))
		msg, _ := e["message"].(string)
		if e["code"] != "KEY_FILE_ERROR" || e["retry_safe"] != false || e["path"] != "session" || !strings.Contains(msg, "refused the prompt") {
			t.Errorf("%s: tool error = %v, want KEY_FILE_ERROR with the cause", name, e)
		}
	}
	h.stop(t)
}
