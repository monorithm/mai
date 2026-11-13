package bridge

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

// renewDelay is the time that the server takes to refuse a lease request in these tests.
const renewDelay = 300 * time.Millisecond

// slowFailure gives a handler that, while fail is true, answers each lease request that match
// gives after renewDelay with HTTP 503 MAIL_SERVER_DOWN, as the server does while the mail
// server does not reply after a restart. count counts these lease requests.
func slowFailure(match func(*http.Request) bool, fail *atomic.Bool, count *atomic.Int32) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if fail.Load() && match(r) {
				count.Add(1)
				time.Sleep(renewDelay)
				yes := true
				writeServerErr(w, 503, "MAIL_SERVER_DOWN", "The mail server does not reply.", &yes)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// After a restart of the container, the server does not know the lease of a named session.
// Calls at the same time for that session must make one lease request, as the first calls
// with a label do. When that request fails, each of the calls gets its error. Then the last
// of N calls does not wait N times the time of a lease request.
func TestCallsAtTheSameTimeAfterARestartMakeOneLeaseRequestForTheNamedSession(t *testing.T) {
	var fail atomic.Bool
	var count atomic.Int32
	h := newWrappedHarness(t, slowFailure(isNamedRequest, &fail, &count))
	h.srv.hbSeconds = 60
	h.start(context.Background())
	if r := result(t, h.call(t, 1, "whoami", map[string]any{"session": "lane-a"})); r["session"] != "cursor.lane-a" {
		t.Fatalf("whoami with the label lane-a = %v", r)
	}
	h.firstBeats(t, "cursor.s1", "cursor.lane-a")

	fail.Store(true)
	h.srv.expire()
	start := time.Now()
	const n = 4
	for i := 0; i < n; i++ {
		h.sendCall(t, 10+i, "whoami", map[string]any{"session": "lane-a"})
	}
	answers := h.recvByID(t, n)
	took := time.Since(start)
	for _, a := range answers {
		wantRetrySafe(t, a, "MAIL_SERVER_DOWN")
	}
	if got := count.Load(); got != 1 {
		t.Errorf("%d calls at the same time made %d lease requests for the named session, want 1 (the last answer came after %v)", n, got, took.Round(10*time.Millisecond))
	}
	// A call after the end of the shared renew sends a new lease request.
	wantRetrySafe(t, h.call(t, 20, "whoami", map[string]any{"session": "lane-a"}), "MAIL_SERVER_DOWN")
	if got := count.Load(); got != 2 {
		t.Errorf("the call after the failed renew made %d lease requests in all, want 2", got)
	}
	fail.Store(false)
	h.stop(t)
}

// When the shared renew is successful, each of the calls gets the new lease.
func TestCallsAtTheSameTimeAfterARestartShareTheNewLease(t *testing.T) {
	var slow atomic.Bool
	var count atomic.Int32
	h := newWrappedHarness(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if slow.Load() && isNamedRequest(r) {
				count.Add(1)
				time.Sleep(renewDelay)
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

	slow.Store(true)
	h.srv.expire()
	const n = 4
	for i := 0; i < n; i++ {
		h.sendCall(t, 10+i, "whoami", map[string]any{"session": "lane-a"})
	}
	for id, a := range h.recvByID(t, n) {
		if r := result(t, a); r["session"] != "cursor.lane-a" {
			t.Errorf("call %d after the renew = %v", id, a)
		}
	}
	if got := count.Load(); got != 1 {
		t.Errorf("%d calls at the same time made %d lease requests for the named session, want 1", n, got)
	}
	h.stop(t)
}

// The same rule for the pool lease, which all the chats of the bridge share.
func TestCallsAtTheSameTimeAfterARestartMakeOneLeaseRequestForThePool(t *testing.T) {
	var fail atomic.Bool
	var count atomic.Int32
	h := newWrappedHarness(t, slowFailure(isPoolRequest, &fail, &count))
	h.srv.hbSeconds = 60
	h.start(context.Background())
	h.firstBeats(t, "cursor.s1")

	fail.Store(true)
	h.srv.expire()
	start := time.Now()
	const n = 4
	for i := 0; i < n; i++ {
		h.sendCall(t, 10+i, "whoami", nil)
	}
	answers := h.recvByID(t, n)
	took := time.Since(start)
	for _, a := range answers {
		if code := errorCode(t, a); code != "MAIL_SERVER_DOWN" {
			t.Fatalf("answer = %v, want MAIL_SERVER_DOWN", a)
		}
	}
	// The heartbeat after a call on a pool lease with no session can add one request.
	if got := count.Load(); got > 2 {
		t.Errorf("%d pool calls at the same time made %d pool lease requests, want 1 (the last answer came after %v)", n, got, took.Round(10*time.Millisecond))
	}
	fail.Store(false)
	h.stop(t)
}
