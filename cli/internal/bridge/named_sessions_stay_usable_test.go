package bridge

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// toolErr gives the error object of a tool result with isError true.
func toolErr(t *testing.T, a map[string]any) map[string]any {
	t.Helper()
	r := result(t, a)
	if r["isError"] != true {
		t.Fatalf("the answer is not a tool error: %v", a)
	}
	sc, _ := r["structuredContent"].(map[string]any)
	e, _ := sc["error"].(map[string]any)
	if e == nil {
		t.Fatalf("the tool error has no error object: %v", a)
	}
	return e
}

// wantRetrySafe stops the test when the answer is not a tool error with the code, the path
// session, and retry_safe true.
func wantRetrySafe(t *testing.T, a map[string]any, code string) {
	t.Helper()
	e := toolErr(t, a)
	if e["code"] != code || e["path"] != "session" || e["retry_safe"] != true {
		t.Fatalf("tool error = %v, want the code %s, the path session, and retry_safe true", e, code)
	}
}

// renewEnds waits until the named lease of the handle has no renew that runs.
func (h *harness) renewEnds(t *testing.T, handle string) {
	t.Helper()
	l := h.b.namedLease(handle)
	if l == nil {
		t.Fatalf("the bridge has no lease for the handle %s", handle)
	}
	waitFor(t, "the end of the renew of the heartbeat", func() bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		return l.renewing == nil
	})
}

// A server error of the lease request (HTTP 500 with no error code, as the server gives when
// the mail server does not reply) is not a refusal of the session. The named session must
// stay: the next call gets a new lease for it.
func TestAServerErrorOfTheLeaseRequestDoesNotEndANamedSession(t *testing.T) {
	var failNamed atomic.Bool
	h := newWrappedHarness(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if failNamed.Load() && isNamedRequest(r) {
				writeProblem(w)
				return
			}
			next.ServeHTTP(w, r)
		})
	})
	h.srv.hbSeconds = 60
	h.start(context.Background())
	handle := h.login(t, 1, "lane-a")["session"].(string)
	h.firstBeats(t, "cursor.s1", "cursor.lane-a")

	// A restart of the container ends each lease. The mail server does not operate at this time.
	failNamed.Store(true)
	h.srv.expire()
	first := h.call(t, 2, "whoami", map[string]any{"session": handle})
	failNamed.Store(false)
	wantRetrySafe(t, first, "SERVER_UNAVAILABLE")

	// The mail server operates again.
	second := h.call(t, 3, "whoami", map[string]any{"session": handle})
	if code := errorCode(t, second); code != "" {
		t.Fatalf("the handle of lane-a ended after one server error: first answer %v, second answer code %s %v",
			first, code, second)
	}
	if r := result(t, second); r["session"] != "cursor.lane-a" {
		t.Fatalf("whoami after the server error = %v", r)
	}
	h.stop(t)
}

// A coded server error (HTTP 503 MAIL_SERVER_DOWN) gives its code to the call, and the named
// session stays.
func TestACodedServerErrorOfTheLeaseRequestGivesItsCodeAndKeepsTheSession(t *testing.T) {
	var failNamed atomic.Bool
	h := newWrappedHarness(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if failNamed.Load() && isNamedRequest(r) {
				writeErr(w, 503, "MAIL_SERVER_DOWN")
				return
			}
			next.ServeHTTP(w, r)
		})
	})
	h.srv.hbSeconds = 60
	h.start(context.Background())
	handle := h.login(t, 1, "lane-a")["session"].(string)
	h.firstBeats(t, "cursor.s1", "cursor.lane-a")
	failNamed.Store(true)
	h.srv.expire()
	wantRetrySafe(t, h.call(t, 2, "whoami", map[string]any{"session": handle}), "MAIL_SERVER_DOWN")
	failNamed.Store(false)
	if r := result(t, h.call(t, 3, "whoami", map[string]any{"session": handle})); r["session"] != "cursor.lane-a" {
		t.Fatalf("whoami after the server error = %v", r)
	}
	h.stop(t)
}

// A network error of the lease request does not end a named session.
func TestANetworkErrorOfTheLeaseRequestKeepsTheSession(t *testing.T) {
	var failNamed atomic.Bool
	h := newWrappedHarness(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if failNamed.Load() && isNamedRequest(r) {
				conn, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					_ = conn.Close()
				}
				return
			}
			next.ServeHTTP(w, r)
		})
	})
	h.srv.hbSeconds = 60
	h.start(context.Background())
	handle := h.login(t, 1, "lane-a")["session"].(string)
	h.firstBeats(t, "cursor.s1", "cursor.lane-a")
	failNamed.Store(true)
	h.srv.expire()
	wantRetrySafe(t, h.call(t, 2, "whoami", map[string]any{"session": handle}), "SERVER_UNAVAILABLE")
	failNamed.Store(false)
	if r := result(t, h.call(t, 3, "whoami", map[string]any{"session": handle})); r["session"] != "cursor.lane-a" {
		t.Fatalf("whoami after the network error = %v", r)
	}
	h.stop(t)
}

// The heartbeat also keeps a named session when the lease request gives a server error. The
// next heartbeat tries again.
func TestTheHeartbeatKeepsANamedSessionWhenTheLeaseRequestGivesAServerError(t *testing.T) {
	var failNamed atomic.Bool
	var failures atomic.Int32
	h := newWrappedHarness(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if failNamed.Load() && isNamedRequest(r) {
				failures.Add(1)
				writeErr(w, 503, "STORE_UNAVAILABLE")
				return
			}
			next.ServeHTTP(w, r)
		})
	})
	h.start(context.Background())
	handle := h.login(t, 1, "lane-a")["session"].(string)
	h.firstBeats(t, "cursor.s1", "cursor.lane-a")
	failNamed.Store(true)
	h.srv.mu.Lock()
	for k, s := range h.srv.valid {
		if s == "cursor.lane-a" {
			delete(h.srv.valid, k)
		}
	}
	h.srv.mu.Unlock()
	waitFor(t, "two failed lease requests of the heartbeat", func() bool { return failures.Load() >= 2 })
	failNamed.Store(false)
	// The handler counts a failed lease request before it sends the answer. Thus the renew of
	// the heartbeat can still wait for that answer. A call at that time shares the renew and
	// gets its error. Wait until the renew ends. Each subsequent renew gets a lease.
	h.renewEnds(t, handle)
	if r := result(t, h.call(t, 2, "whoami", map[string]any{"session": handle})); r["session"] != "cursor.lane-a" {
		t.Fatalf("whoami after the failed heartbeats = %v", r)
	}
	h.stop(t)
}

// A login that gets a server error with no code gives a tool error that the chat can try
// again, not a JSON-RPC error.
func TestALoginWithAServerErrorGivesARetrySafeToolError(t *testing.T) {
	var failNamed atomic.Bool
	failNamed.Store(true)
	h := newWrappedHarness(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if failNamed.Load() && isNamedRequest(r) {
				writeProblem(w)
				return
			}
			next.ServeHTTP(w, r)
		})
	})
	h.start(context.Background())
	wantRetrySafe(t, h.call(t, 1, "login", map[string]any{"session": "lane-a"}), "SERVER_UNAVAILABLE")
	failNamed.Store(false)
	if sc := h.login(t, 2, "lane-a"); sc["login"] != "cursor.lane-a" {
		t.Fatalf("login after the server error = %v", sc)
	}
	h.stop(t)
}

// coded gives a handler that answers each named lease request with the HTTP status and the
// error body.
func coded(status int, body string) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isNamedRequest(r) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(body))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// A login that gets an HTTP 5xx error with a code gives that code with retry_safe true, also
// when the server gives retry_safe false. The server gives HTTP 502 LOGIN_FAILED with
// retry_safe false when its mail login of a lease request fails.
func TestALoginThatGetsAnHTTP5xxErrorGivesRetrySafeTrue(t *testing.T) {
	h := newWrappedHarness(t, coded(http.StatusBadGateway,
		`{"error":{"code":"LOGIN_FAILED","message":"The login or the secret is not correct.","retry_safe":false}}`))
	h.start(context.Background())
	wantRetrySafe(t, h.call(t, 1, "login", map[string]any{"session": "lane-a"}), "LOGIN_FAILED")
	h.stop(t)
}

// A login that gets an HTTP 4xx error with a code gives the retry_safe of the server, or
// false when the answer has no retry_safe.
func TestALoginThatGetsAnHTTP4xxErrorGivesTheRetrySafeOfTheServer(t *testing.T) {
	for _, tc := range []struct {
		body string
		want bool
	}{
		{`{"error":{"code":"SESSION_BUSY","message":"busy","retry_safe":true}}`, true},
		{`{"error":{"code":"SESSION_BUSY","message":"busy"}}`, false},
	} {
		h := newWrappedHarness(t, coded(http.StatusConflict, tc.body))
		h.start(context.Background())
		e := toolErr(t, h.call(t, 1, "login", map[string]any{"session": "lane-a"}))
		if e["code"] != "SESSION_BUSY" || e["retry_safe"] != tc.want {
			t.Fatalf("login with the answer %s = %v, want SESSION_BUSY and retry_safe %v", tc.body, e, tc.want)
		}
		h.stop(t)
	}
}

// While one chat logs out of lane-a, a second chat of the same bridge logs in to lane-a. The
// bridge holds the only lease of lane-a, thus the login must not get SESSION_BUSY.
func TestALoginDuringALogoutOfTheSameSessionIsNotBusy(t *testing.T) {
	var hold atomic.Bool
	reached := make(chan struct{}, 1)
	release := make(chan struct{})
	var once sync.Once
	free := func() { once.Do(func() { close(release) }) }
	h := newWrappedHarness(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if hold.Load() && r.Method == http.MethodDelete && r.URL.Path == "/api/leases/current" {
				select {
				case reached <- struct{}{}:
				default:
				}
				<-release
			}
			if hold.Load() && r.Method == http.MethodPost && r.URL.Path == "/api/leases" {
				// The login request gets to the server while the end of the first lease waits.
				// The end of the lease continues after the answer to the login.
				next.ServeHTTP(w, r)
				free()
				return
			}
			next.ServeHTTP(w, r)
		})
	})
	h.start(context.Background())
	h.send(t, `{"jsonrpc":"2.0","id":0,"method":"ping"}`)
	h.recv(t)
	handle := h.login(t, 1, "lane-a")["session"].(string)

	hold.Store(true)
	h.send(t, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"logout","arguments":{"session":"`+handle+`"}}}`)
	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		t.Fatal("the logout sent no end of the lease")
	}
	h.send(t, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"login","arguments":{"session":"lane-a"}}}`)
	// A bridge that lets the login wait for the logout sends no lease request at this time.
	stop := time.AfterFunc(500*time.Millisecond, free)
	defer stop.Stop()
	a := h.recv(t)
	b := h.recv(t)
	hold.Store(false)
	login := a
	if b["id"] == float64(3) {
		login = b
	}
	if code := errorCode(t, login); code != "" {
		t.Fatalf("the login during the logout = %s: %v", code, login)
	}
	h.stop(t)
}
