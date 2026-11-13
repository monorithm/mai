package bridge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// closeLimit is the time that the server takes to answer DELETE /api/leases/current when
// the mail server does not reply, or when a wait_for_mail of the session holds the IMAP
// lock: the server removes the lease, and then waits for the close of the mail connections
// for CLOSE_LIMIT_SECONDS (2 s) of the server.
const closeLimit = 2 * time.Second

// slowEnd gives a handler that removes the lease of each DELETE at once, as the server does
// (the server removes the lease before the logout), and answers only after
// closeLimit, while slow is true.
func slowEnd(slow *atomic.Bool) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !slow.Load() || r.Method != http.MethodDelete || r.URL.Path != "/api/leases/current" {
				next.ServeHTTP(w, r)
				return
			}
			rec := httptest.NewRecorder()
			next.ServeHTTP(rec, r)
			select {
			case <-time.After(closeLimit):
			case <-r.Context().Done():
				return
			}
			w.WriteHeader(rec.Code)
		})
	}
}

// Before it stops, the bridge ends each lease. The server can take
// CLOSE_LIMIT_SECONDS (2 s) or LOGOUT_LIMIT_SECONDS (3 s) to answer each DELETE when the
// mail server does not reply. The bridge must still end each lease: a lease that stays
// makes the next login to its named session get SESSION_BUSY for EXPIRES_SECONDS (90 s).
func TestTheStopEndsEachLeaseWhenEachEndTakesTheCloseLimitOfTheServer(t *testing.T) {
	var slow atomic.Bool
	h := newWrappedHarness(t, slowEnd(&slow))
	h.srv.hbSeconds = 60
	h.start(context.Background())
	h.login(t, 1, "lane-a")
	h.login(t, 2, "lane-b")
	h.login(t, 3, "lane-c")
	h.firstBeats(t, "cursor.s1", "cursor.lane-a", "cursor.lane-b", "cursor.lane-c")
	slow.Store(true)
	started := time.Now()
	h.stop(t)
	if left := h.srv.leasedSessions(); len(left) != 0 {
		t.Fatalf("%.1f s after the stop, the server has leases of the sessions %q; stderr: %s",
			time.Since(started).Seconds(), left, h.stderr.String())
	}
}
