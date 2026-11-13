package bridge

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

// slowNamedRefusal gives a handler that refuses each named lease request with the HTTP status
// and the code. While hold is true, it sends the answer of the next named lease request only
// after slowAnswer. count counts the named lease requests.
func slowNamedRefusal(status int, code string, hold *atomic.Bool, count *atomic.Int32, reached chan<- struct{}) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isNamedRequest(r) {
				next.ServeHTTP(w, r)
				return
			}
			count.Add(1)
			if hold.CompareAndSwap(true, false) {
				select {
				case reached <- struct{}{}:
				default:
				}
				select {
				case <-time.After(slowAnswer):
				case <-r.Context().Done():
					return
				}
			}
			writeErr(w, status, code)
		})
	}
}

// Calls at the same time for the same session make one lease request, also when that request
// is not successful: each call gets the tool error of that request. Without this, each call
// waits for the lease request of the call before it and then sends its own request. With a
// server that does not reply, the last of N calls then waits N times the time limit of a lease
// request.
func TestFirstCallsAtTheSameTimeGetTheErrorOfOneLeaseRequest(t *testing.T) {
	for _, c := range []struct {
		status    int
		code      string
		retrySafe bool
	}{
		{503, "MAIL_SERVER_DOWN", true},
		{409, "SESSION_BUSY", false},
	} {
		t.Run(c.code, func(t *testing.T) {
			var hold atomic.Bool
			var count atomic.Int32
			reached := make(chan struct{}, 1)
			h := newWrappedHarness(t, slowNamedRefusal(c.status, c.code, &hold, &count, reached))
			h.start(context.Background())
			h.firstBeats(t, "cursor.s1")

			hold.Store(true)
			h.sendCall(t, 1, "whoami", map[string]any{"session": "lane-a"})
			awaitReached(t, reached, "lease request for the first call with lane-a")
			others := []string{"cursor.lane-a", "lane-a", "lane-a", "cursor.lane-a"}
			for i, name := range others {
				h.sendCall(t, 2+i, "whoami", map[string]any{"session": name})
			}
			h.sendCall(t, 9, "login", map[string]any{"session": "lane-a"})
			answers := h.recvByID(t, len(others)+2)
			for _, a := range answers {
				wantSessionError(t, a, c.code, c.retrySafe)
			}
			if n := count.Load(); n != 1 {
				t.Fatalf("the calls at the same time made %d lease requests, want 1", n)
			}

			// The bridge keeps no error: the next call sends a new lease request.
			wantSessionError(t, h.call(t, 20, "whoami", map[string]any{"session": "lane-a"}), c.code, c.retrySafe)
			if n := count.Load(); n != 2 {
				t.Fatalf("the lease requests are %d after the next call, want 2", n)
			}
			if calls := h.srv.toolCalls(); len(calls) != 0 {
				t.Fatalf("a call reached the server: %v", calls)
			}
			h.stop(t)
		})
	}
}
