package bridge

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
)

// A call with no argument session has no path session. When the renew of the pool lease
// fails, the tool error of the call must have no path, as for a failed read of the agent key
// and as for NO_FREE_SESSION of the server.
func TestAFailedRenewOfThePoolLeaseGivesACallWithNoSessionNoPath(t *testing.T) {
	var failPool atomic.Bool
	h := newWrappedHarness(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if failPool.Load() && isPoolRequest(r) {
				writeErr(w, 503, "MAIL_SERVER_DOWN")
				return
			}
			next.ServeHTTP(w, r)
		})
	})
	h.srv.hbSeconds = 60
	h.start(context.Background())
	h.firstBeats(t, "cursor.s1")
	failPool.Store(true)
	h.srv.expire()
	e := toolErr(t, h.call(t, 1, "whoami", map[string]any{}))
	t.Logf("pool renew failure: %v", e)
	if e["path"] != nil {
		t.Fatalf("the tool error of a call with no argument session has the path %v: %v", e["path"], e)
	}
	failPool.Store(false)
	h.stop(t)
}

// When no pool session is free after a restart of the server, the renew of the pool lease
// gets NO_FREE_SESSION. The server gives the same code with no path to a call on a lease with
// no session. The bridge must also give it with no path to a call with no argument session.
func TestNoFreeSessionOfAPoolRenewHasNoPathForACallWithNoSession(t *testing.T) {
	h := newHarness(t, memoryWithKey())
	h.srv.hbSeconds = 60
	h.srv.refuseNew = true
	h.start(context.Background())
	h.firstBeats(t, "cursor.s1")
	h.srv.expire()
	e := toolErr(t, h.call(t, 1, "whoami", map[string]any{}))
	if e["code"] != "NO_FREE_SESSION" || e["retry_safe"] != true || e["path"] != nil {
		t.Fatalf("tool error = %v, want NO_FREE_SESSION with retry_safe true and no path", e)
	}
	h.stop(t)
}
