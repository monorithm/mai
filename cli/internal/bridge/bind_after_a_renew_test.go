package bridge

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// unboundRenew gives a handler that answers the next pool lease request with a lease with no
// session (no pool session is free at that time), while on is true. The tool call that
// follows on that lease binds a pool session (a pool session is free again).
func unboundRenew(on *atomic.Bool) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		srv := next.(*fakeServer)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !on.Load() || !isPoolRequest(r) {
				next.ServeHTTP(w, r)
				return
			}
			on.Store(false)
			srv.mu.Lock()
			srv.poolFull = true
			srv.mu.Unlock()
			next.ServeHTTP(w, r)
			srv.mu.Lock()
			srv.poolFull = false
			srv.mu.Unlock()
		})
	}
}

// The pool lease has the session cursor.s1, and the server ends the lease (for example a
// restart of the container). A tool call with no session gets LEASE_UNKNOWN, and the renew
// gets a lease with no session. The call then goes again on the new lease and binds a pool
// session. The bridge must send a heartbeat after that call, thus the notice gets the pool
// entry of the bind before the next periodic heartbeat (60 s here).
func TestACallThatBindsAfterARenewWithNoSessionSendsAHeartbeat(t *testing.T) {
	var on atomic.Bool
	h := newWrappedHarness(t, unboundRenew(&on))
	h.srv.hbSeconds = 60
	h.start(context.Background())
	h.firstBeats(t, "cursor.s1")
	h.srv.expire()
	on.Store(true)
	r := result(t, h.call(t, 2, "whoami", nil))
	bound, _ := r["session"].(string)
	if !strings.HasPrefix(bound, "cursor.s") || bound == "cursor.s1" {
		t.Fatalf("whoami after the renew = %v", r)
	}
	deadline := time.Now().Add(3 * time.Second)
	var n map[string]any
	for time.Now().Before(deadline) {
		n = readNotice(t, h)
		if n != nil && n["session"] == bound {
			h.stop(t)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	h.srv.mu.Lock()
	beats := h.srv.beats[bound]
	h.srv.mu.Unlock()
	t.Fatalf("3 s after the call that bound %s, the notice has no pool entry: %v; heartbeats of %s: %d",
		bound, n, bound, beats)
}
