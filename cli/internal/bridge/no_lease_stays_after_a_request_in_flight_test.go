package bridge

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// slowAnswer is the time that slowNamedAnswer waits before it sends the answer.
const slowAnswer = 400 * time.Millisecond

// slowNamedAnswer gives a handler that makes the lease of the next named lease request while
// hold is true, and sends the answer only after slowAnswer. A real server also makes the
// lease first and answers after some time, because it logs in to the mail server. When the
// bridge stops the request before the answer, the server keeps a lease that the bridge does
// not know.
func slowNamedAnswer(hold *atomic.Bool, reached chan<- struct{}) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isNamedRequest(r) || !hold.CompareAndSwap(true, false) {
				next.ServeHTTP(w, r)
				return
			}
			rec := httptest.NewRecorder()
			next.ServeHTTP(rec, r)
			select {
			case reached <- struct{}{}:
			default:
			}
			select {
			case <-time.After(slowAnswer):
			case <-r.Context().Done():
				return
			}
			for k, v := range rec.Header() {
				w.Header()[k] = v
			}
			w.WriteHeader(rec.Code)
			_, _ = w.Write(rec.Body.Bytes())
		})
	}
}

// expireSession ends the lease of one session on the server side.
func (f *fakeServer) expireSession(session string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for k, s := range f.valid {
		if s == session {
			delete(f.valid, k)
		}
	}
}

// leasedSessions gives the session of each live lease on the server.
func (f *fakeServer) leasedSessions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []string{}
	for _, s := range f.valid {
		out = append(out, s)
	}
	return out
}

// awaitReached waits until the server made the lease of the slow lease request.
func awaitReached(t *testing.T, reached <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		t.Fatalf("the bridge sent no %s", what)
	}
}

// A heartbeat of lane-a gets a new lease for lane-a while a chat logs out of lane-a. The
// logout must end each lease of lane-a that the bridge asked for. A login to lane-a in the
// same bridge then does not get SESSION_BUSY.
func TestALogoutDuringALeaseRequestOfTheHeartbeatLeavesNoLeaseThatMakesTheLoginBusy(t *testing.T) {
	var hold atomic.Bool
	reached := make(chan struct{}, 1)
	h := newWrappedHarness(t, slowNamedAnswer(&hold, reached))
	h.start(context.Background())
	handle := h.login(t, 1, "lane-a")["session"].(string)
	h.firstBeats(t, "cursor.s1", "cursor.lane-a")

	hold.Store(true)
	h.srv.expireSession("cursor.lane-a")
	awaitReached(t, reached, "lease request for lane-a in the heartbeat")
	if r := result(t, h.call(t, 2, "logout", map[string]any{"session": handle})); r["isError"] == true {
		t.Fatalf("logout = %v", r)
	}
	if code := errorCode(t, h.call(t, 3, "login", map[string]any{"session": "lane-a"})); code != "" {
		t.Fatalf("the login to lane-a after the logout gave %s; the server has leases of the sessions %q",
			code, h.srv.leasedSessions())
	}
	h.stop(t)
}

// The bridge stops while a heartbeat of lane-a gets a new lease for lane-a. The bridge must
// end each lease that it asked for. Then no lease stays on the server.
func TestTheStopOfTheBridgeDuringALeaseRequestOfTheHeartbeatLeavesNoLease(t *testing.T) {
	var hold atomic.Bool
	reached := make(chan struct{}, 1)
	h := newWrappedHarness(t, slowNamedAnswer(&hold, reached))
	h.start(context.Background())
	h.login(t, 1, "lane-a")
	h.firstBeats(t, "cursor.s1", "cursor.lane-a")

	hold.Store(true)
	h.srv.expireSession("cursor.lane-a")
	awaitReached(t, reached, "lease request for lane-a in the heartbeat")
	h.stop(t)
	if left := h.srv.leasedSessions(); len(left) != 0 {
		t.Fatalf("after the stop of the bridge, the server has leases of the sessions %q", left)
	}
}

// The context of the bridge ends (SIGINT, SIGTERM, or SIGHUP) while a login waits for the
// answer of its lease request. The bridge must end the lease of the login. Then no lease
// stays on the server.
func TestTheStopOfTheBridgeDuringALoginLeavesNoLease(t *testing.T) {
	var hold atomic.Bool
	reached := make(chan struct{}, 1)
	h := newWrappedHarness(t, slowNamedAnswer(&hold, reached))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.start(ctx)
	h.firstBeats(t, "cursor.s1")

	hold.Store(true)
	h.send(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"login","arguments":{"session":"lane-a"}}}`)
	awaitReached(t, reached, "lease request for the login to lane-a")
	cancel()
	go func() { _, _ = io.Copy(io.Discard, h.out) }()
	if code := h.wait(t); code != ExitOK {
		t.Fatalf("exit = %d, stderr: %s", code, h.stderr.String())
	}
	if left := h.srv.leasedSessions(); len(left) != 0 {
		t.Fatalf("after the stop of the bridge, the server has leases of the sessions %q", left)
	}
}
