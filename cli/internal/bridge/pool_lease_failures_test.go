package bridge

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// readNotice reads the notice file of the bridge, or gives nil.
func readNotice(t *testing.T, h *harness) map[string]any {
	t.Helper()
	data, err := os.ReadFile(noticePath(h))
	if err != nil {
		return nil
	}
	var n map[string]any
	if json.Unmarshal(data, &n) != nil {
		return nil
	}
	return n
}

// namedInNotice gives the sessions of the list named of a notice.
func namedInNotice(n map[string]any) []string {
	var out []string
	list, _ := n["named"].([]any)
	for _, e := range list {
		if m, ok := e.(map[string]any); ok {
			s, _ := m["session"].(string)
			out = append(out, s)
		}
	}
	return out
}

// running stops the test when the bridge stops in the time d or less.
func (h *harness) running(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case code := <-h.done:
		t.Fatalf("the bridge stopped with exit %d; stderr: %s", code, h.stderr.String())
	case <-time.After(d):
	}
}

// A bridge with no free pool session must let a chat log in to a named session. The
// owner made the named session, and a named session does not use a pool session. The server
// gives the pool lease with no session.
func TestABridgeWithNoFreePoolSessionStillServesANamedSession(t *testing.T) {
	h := newHarness(t, memoryWithKey())
	h.srv.poolFull = true
	h.start(context.Background())
	h.running(t, 500*time.Millisecond)

	h.send(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if a := h.recv(t); a["id"] != float64(1) || a["error"] != nil || a["result"] == nil {
		t.Fatalf("tools/list = %v", a)
	}
	sc := h.login(t, 2, "lane-a")
	if sc["login"] != "cursor.lane-a" {
		t.Fatalf("login = %v", sc)
	}
	if r := result(t, h.call(t, 3, "whoami", map[string]any{"session": sc["session"]})); r["session"] != "cursor.lane-a" {
		t.Fatalf("whoami with the handle = %v", r)
	}
	if e := toolErr(t, h.call(t, 4, "whoami", nil)); e["code"] != "NO_FREE_SESSION" || e["retry_safe"] != true {
		t.Fatalf("whoami with no session = %v", e)
	}
	if !strings.Contains(h.stderr.String(), "no free pool session") {
		t.Fatalf("stderr = %q", h.stderr.String())
	}
	// The notice has lane-a and no pool entry.
	waitFor(t, "notice with lane-a and no pool entry", func() bool {
		n := readNotice(t, h)
		_, pool := n["session"]
		names := namedInNotice(n)
		return n != nil && !pool && len(names) == 1 && names[0] == "cursor.lane-a"
	})
	h.stop(t)
}

// When a pool session becomes free, a tools/call with no session binds it to the pool lease.
// A subsequent heartbeat gives the session, and the notice then has the pool entry.
func TestAPoolLeaseRecordsTheSessionOfTheHeartbeatAfterABind(t *testing.T) {
	h := newHarness(t, memoryWithKey())
	h.srv.poolFull = true
	h.start(context.Background())
	h.firstBeats(t, "")
	h.srv.mu.Lock()
	h.srv.poolFull = false
	h.srv.mu.Unlock()
	r := result(t, h.call(t, 1, "whoami", nil))
	bound, _ := r["session"].(string)
	if !strings.HasPrefix(bound, "cursor.s") {
		t.Fatalf("whoami after the bind = %v", r)
	}
	waitFor(t, "notice with the pool session "+bound, func() bool {
		n := readNotice(t, h)
		return n != nil && n["session"] == bound
	})
	if !strings.Contains(h.stderr.String(), "the pool lease has the session "+bound) {
		t.Fatalf("stderr = %q", h.stderr.String())
	}
	h.stop(t)
}

// The pool renew can get 409 NO_FREE_SESSION, when the agent has too many leases with no
// session. The bridge must not stop: each named session stays, a call with no session gets
// a tool error, and the next call tries again.
func TestTheBridgeKeepsRunningWhenThePoolRenewGivesNoFreeSession(t *testing.T) {
	h := newHarness(t, memoryWithKey())
	h.start(context.Background())
	handle := h.login(t, 1, "lane-a")["session"].(string)
	h.srv.mu.Lock()
	h.srv.refuseNew = true
	h.srv.mu.Unlock()
	h.srv.expire()
	e := toolErr(t, h.call(t, 2, "whoami", nil))
	if e["code"] != "NO_FREE_SESSION" || e["retry_safe"] != true {
		t.Fatalf("whoami with no session = %v", e)
	}
	// The heartbeats of the pool lease also get NO_FREE_SESSION.
	h.running(t, 1500*time.Millisecond)
	if r := result(t, h.call(t, 3, "whoami", map[string]any{"session": handle})); r["session"] != "cursor.lane-a" {
		t.Fatalf("whoami with the handle = %v", r)
	}
	h.srv.mu.Lock()
	h.srv.refuseNew = false
	h.srv.mu.Unlock()
	if r := result(t, h.call(t, 4, "whoami", nil)); !strings.HasPrefix(r["session"].(string), "cursor.s") {
		t.Fatalf("whoami with no session after the pool is free = %v", r)
	}
	h.stop(t)
}

// wantPoolRetrySafe makes sure that a is a tool error with the code, no path, and retry_safe
// true. A call with no argument session has no path.
func wantPoolRetrySafe(t *testing.T, a map[string]any, code string) {
	t.Helper()
	e := toolErr(t, a)
	if e["code"] != code || e["path"] != nil || e["retry_safe"] != true {
		t.Fatalf("tool error = %v, want the code %s, no path, and retry_safe true", e, code)
	}
}

// A server error of the pool renew (HTTP 503 with a code, or HTTP 500 with no code) does
// not stop the bridge.
func TestTheBridgeKeepsRunningWhenThePoolRenewGivesAServerError(t *testing.T) {
	var mode atomic.Int32 // 0: no error, 1: 503 MAIL_SERVER_DOWN, 2: 500 with no code
	h := newWrappedHarness(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch m := mode.Load(); {
			case m == 1 && isPoolRequest(r):
				writeErr(w, 503, "MAIL_SERVER_DOWN")
				return
			case m == 2 && isPoolRequest(r):
				writeProblem(w)
				return
			}
			next.ServeHTTP(w, r)
		})
	})
	h.start(context.Background())
	handle := h.login(t, 1, "lane-a")["session"].(string)
	mode.Store(1)
	h.srv.expire()
	wantPoolRetrySafe(t, h.call(t, 2, "whoami", nil), "MAIL_SERVER_DOWN")
	mode.Store(2)
	wantPoolRetrySafe(t, h.call(t, 3, "whoami", nil), "SERVER_UNAVAILABLE")
	h.send(t, `{"jsonrpc":"2.0","id":4,"method":"tools/list"}`)
	if a := h.recv(t); a["id"] != float64(4) {
		t.Fatalf("tools/list = %v", a)
	}
	h.running(t, 1500*time.Millisecond)
	if r := result(t, h.call(t, 5, "whoami", map[string]any{"session": handle})); r["session"] != "cursor.lane-a" {
		t.Fatalf("whoami with the handle = %v", r)
	}
	mode.Store(0)
	if r := result(t, h.call(t, 6, "whoami", nil)); !strings.HasPrefix(r["session"].(string), "cursor.s") {
		t.Fatalf("whoami with no session after the error = %v", r)
	}
	h.stop(t)
}

// While the pool lease has no live lease, the notice file has no pool entry.
func TestTheNoticeHasNoPoolEntryAfterAFailedPoolRenew(t *testing.T) {
	h := newHarness(t, memoryWithKey())
	h.start(context.Background())
	h.login(t, 1, "lane-a")
	waitFor(t, "notice with the pool session", func() bool {
		n := readNotice(t, h)
		return n != nil && n["session"] == "cursor.s1"
	})
	h.srv.mu.Lock()
	h.srv.refuseNew = true
	for k, s := range h.srv.valid {
		if s == "cursor.s1" {
			delete(h.srv.valid, k)
		}
	}
	h.srv.mu.Unlock()
	waitFor(t, "notice with no pool entry", func() bool {
		n := readNotice(t, h)
		_, pool := n["session"]
		names := namedInNotice(n)
		return n != nil && !pool && len(names) == 1 && names[0] == "cursor.lane-a"
	})
	h.stop(t)
}
