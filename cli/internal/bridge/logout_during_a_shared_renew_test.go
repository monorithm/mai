package bridge

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// statusRecorder keeps the status of an answer.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// logoutDuringASharedRenew gets the lease of lane-a with first, ends each lease on the server
// as a restart does, and waits until the heartbeat of lane-a sends its lease request. Then it
// sends the call (id 2) with the argument session value, which waits for the same renew, and
// a logout of lane-a (id 3). The server gives the new lease only after the logout started,
// and the call after the renew comes to the server only after the logout ended the new lease.
// It gives the answer of the call and the answer of the logout.
func logoutDuringASharedRenew(t *testing.T, first string, value func(map[string]any) string) (map[string]any, map[string]any) {
	t.Helper()
	var blockNamed, holdCalls atomic.Bool
	blocked := make(chan struct{}, 1)
	release := make(chan struct{})
	ended := make(chan struct{})
	var endOnce sync.Once
	h := newWrappedHarness(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case isNamedRequest(r) && blockNamed.CompareAndSwap(true, false):
				blocked <- struct{}{}
				<-release
				holdCalls.Store(true)
			case r.Method == http.MethodPost && r.URL.Path == "/mcp" && holdCalls.Load():
				select {
				case <-ended:
				case <-time.After(5 * time.Second):
				}
			case r.Method == http.MethodDelete:
				rec := &statusRecorder{ResponseWriter: w}
				next.ServeHTTP(rec, r)
				if rec.status == http.StatusNoContent && holdCalls.Load() {
					endOnce.Do(func() { close(ended) })
				}
				return
			}
			next.ServeHTTP(w, r)
		})
	})
	h.start(context.Background())
	var sc map[string]any
	if first == "login" {
		sc = h.login(t, 1, "lane-a")
	} else if r := result(t, h.call(t, 1, "whoami", map[string]any{"session": "lane-a"})); r["session"] != "cursor.lane-a" {
		t.Fatalf("whoami lane-a = %v", r)
	}
	h.firstBeats(t, "cursor.s1", "cursor.lane-a")
	blockNamed.Store(true)
	h.srv.expire()
	select {
	case <-blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("the heartbeat sent no lease request for lane-a")
	}
	h.sendCall(t, 2, "whoami", map[string]any{"session": value(sc)})
	time.Sleep(300 * time.Millisecond)
	h.sendCall(t, 3, "logout", map[string]any{"session": "lane-a"})
	time.Sleep(300 * time.Millisecond)
	close(release)
	got := h.recvByID(t, 2)
	h.stop(t)
	return got[2], got[3]
}

// A logout of a session can end its lease while a call with the label waits for a renew
// that the heartbeat does after a restart of the server. The server then does not do the
// call. The call must get a new lease for the session and the result of the call, as for a
// logout during a call that does not wait for a renew. It must not get SERVER_UNAVAILABLE,
// because the server did not end the lease.
func TestALogoutDuringARenewOfTheHeartbeatGivesACallWithTheLabelANewLease(t *testing.T) {
	call, logout := logoutDuringASharedRenew(t, "call", func(map[string]any) string { return "lane-a" })
	if code := errorCode(t, logout); code != "" {
		t.Fatalf("logout lane-a = %v", logout)
	}
	if code := errorCode(t, call); code != "" {
		t.Fatalf("the call with the label lane-a during the logout got %v, want the result of a new lease for lane-a", toolErr(t, call))
	}
	if r := result(t, call); r["session"] != "cursor.lane-a" {
		t.Fatalf("whoami lane-a after the logout = %v", r)
	}
}

// The same logout during a call with the handle of the session gives SESSION_UNKNOWN with
// retry_safe false: the bridge ended the lease, and the handle is not valid after the
// logout. SERVER_UNAVAILABLE with retry_safe true tells the chat to try the handle again.
func TestALogoutDuringARenewOfTheHeartbeatGivesACallWithTheHandleSessionUnknown(t *testing.T) {
	call, logout := logoutDuringASharedRenew(t, "login", func(sc map[string]any) string { return sc["session"].(string) })
	if code := errorCode(t, logout); code != "" {
		t.Fatalf("logout lane-a = %v", logout)
	}
	e := toolErr(t, call)
	if e["code"] != "SESSION_UNKNOWN" || e["retry_safe"] != false {
		t.Fatalf("the call with the handle of lane-a during the logout got %v, want SESSION_UNKNOWN with retry_safe false", e)
	}
}
