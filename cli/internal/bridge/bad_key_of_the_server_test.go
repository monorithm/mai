package bridge

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// writeServerErr writes an error answer of the lease API with the real message of the server.
func writeServerErr(w http.ResponseWriter, status int, code, message string, retrySafe *bool) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body := `{"error":{"code":"` + code + `","message":"` + message + `"`
	if retrySafe != nil {
		if *retrySafe {
			body += `,"retry_safe":true`
		} else {
			body += `,"retry_safe":false`
		}
	}
	_, _ = w.Write([]byte(body + `}}`))
}

// The server refuses the agent key of the key file (HTTP 401 BAD_KEY, "the agent key is not
// valid"), for example after a rotate of the key with a different key file. The message of
// BAD_KEY must give the step for the owner.
func TestABadKeyOfTheServerForACallWithALabelGivesTheStep(t *testing.T) {
	h := newWrappedHarness(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isNamedRequest(r) {
				writeServerErr(w, 401, "BAD_KEY", "the agent key is not valid", nil)
				return
			}
			next.ServeHTTP(w, r)
		})
	})
	h.start(context.Background())
	e := toolErr(t, h.call(t, 1, "whoami", map[string]any{"session": "lane-a"}))
	if e["code"] != "BAD_KEY" || e["path"] != "session" || e["retry_safe"] != false {
		t.Fatalf("tool error = %v, want BAD_KEY with the path session and retry_safe false", e)
	}
	msg, _ := e["message"].(string)
	if !strings.Contains(msg, "mai agent rotate cursor") {
		t.Errorf("the message of BAD_KEY for a call with a label has no step: %q", msg)
	}
	e = toolErr(t, h.call(t, 2, "login", map[string]any{"session": "lane-a"}))
	msg, _ = e["message"].(string)
	if !strings.Contains(msg, "mai agent rotate cursor") {
		t.Errorf("the message of BAD_KEY for a login has no step: %q", msg)
	}
	h.stop(t)
}

// The same refusal at the renew of a named lease, for example after a restart of the
// container.
func TestABadKeyOfTheServerAtTheRenewOfANamedLeaseGivesTheStep(t *testing.T) {
	var refuse atomic.Bool
	h := newWrappedHarness(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if refuse.Load() && isNamedRequest(r) {
				writeServerErr(w, 401, "BAD_KEY", "the agent key is not valid", nil)
				return
			}
			next.ServeHTTP(w, r)
		})
	})
	h.srv.hbSeconds = 60
	h.start(context.Background())
	if r := result(t, h.call(t, 1, "whoami", map[string]any{"session": "lane-a"})); r["session"] != "cursor.lane-a" {
		t.Fatalf("whoami with the label lane-a = %v", r)
	}
	h.firstBeats(t, "cursor.s1", "cursor.lane-a")
	refuse.Store(true)
	h.srv.expire()
	e := toolErr(t, h.call(t, 2, "whoami", map[string]any{"session": "lane-a"}))
	msg, _ := e["message"].(string)
	if e["code"] != "BAD_KEY" || !strings.Contains(msg, "mai agent rotate cursor") {
		t.Errorf("tool error at the renew = %v, want BAD_KEY with the step mai agent rotate cursor", e)
	}
	h.stop(t)
}

// The same refusal at the start of the bridge, and at the renew of the pool lease, which
// stops the bridge. The stderr line and the JSON-RPC error must give the step.
func TestABadKeyOfTheServerAtTheStartAndAtThePoolRenewGivesTheStep(t *testing.T) {
	h := newHarness(t, memoryWithKey())
	h.srv.refuseKey = true
	h.start(context.Background())
	if code := h.wait(t); code != ExitError {
		t.Fatalf("exit = %d", code)
	}
	if text := h.stderr.String(); !strings.Contains(text, "mai agent rotate cursor") {
		t.Errorf("the start of the bridge with a key that the server refuses tells: %q", strings.TrimSpace(text))
	}

	h2 := newHarness(t, memoryWithKey())
	h2.srv.hbSeconds = 60
	h2.start(context.Background())
	h2.firstBeats(t, "cursor.s1")
	h2.srv.mu.Lock()
	h2.srv.refuseKey = true
	h2.srv.mu.Unlock()
	h2.srv.expire()
	h2.send(t, `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"whoami"}}`)
	a := h2.recv(t)
	e, _ := a["error"].(map[string]any)
	msg, _ := e["message"].(string)
	if !strings.Contains(msg, "mai agent rotate cursor") || !strings.Contains(msg, "cursor. The bridge stops.") || strings.Contains(msg, "..") {
		t.Errorf("the stop at the pool renew gives %q", msg)
	}
	h2.wait(t)
}
