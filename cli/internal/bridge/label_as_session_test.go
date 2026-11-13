package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// namedRequests gives the session name of each lease request that names a session.
func (f *fakeServer) namedRequests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []string{}
	for _, req := range f.leaseReqs {
		if name, ok := req["session"].(string); ok {
			out = append(out, name)
		}
	}
	return out
}

// toolCalls gives each tools/call that reached /mcp.
func (f *fakeServer) toolCalls() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []map[string]any{}
	for _, m := range f.mcpCalls {
		if m["method"] == "tools/call" {
			out = append(out, m)
		}
	}
	return out
}

// wantSessionError stops the test when the answer is not a tool error with the code, the
// path session, and the retry_safe value.
func wantSessionError(t *testing.T, a map[string]any, code string, retrySafe bool) {
	t.Helper()
	e := toolErr(t, a)
	if e["code"] != code || e["path"] != "session" || e["retry_safe"] != retrySafe {
		t.Fatalf("tool error = %v, want the code %s, the path session, and retry_safe %v", e, code, retrySafe)
	}
}

// sendCall writes one tools/call and does not wait for the answer.
func (h *harness) sendCall(t *testing.T, id int, name string, args map[string]any) {
	t.Helper()
	line, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": args}})
	h.send(t, string(line))
}

// recvByID reads n answers and gives them by id.
func (h *harness) recvByID(t *testing.T, n int) map[int]map[string]any {
	t.Helper()
	out := map[int]map[string]any{}
	for len(out) < n {
		a := h.recv(t)
		id, ok := a["id"].(float64)
		if !ok {
			t.Fatalf("an answer with no id: %v", a)
		}
		out[int(id)] = a
	}
	return out
}

func TestACallWithALabelActsAsTheNamedSessionWithNoLogin(t *testing.T) {
	h := startNamed(t)
	r := result(t, h.call(t, 1, "whoami", map[string]any{"session": "lane-a", "verbose": true}))
	if r["session"] != "cursor.lane-a" {
		t.Fatalf("whoami with the label lane-a = %v", r)
	}
	args, _ := r["arguments"].(map[string]any)
	if _, ok := args["session"]; ok || args["verbose"] != true {
		t.Fatalf("the server got the arguments %v", args)
	}
	if r := result(t, h.call(t, 2, "list_inbox", map[string]any{"session": "lane-a"})); r["session"] != "cursor.lane-a" {
		t.Fatalf("a second call with the label lane-a = %v", r)
	}
	if r := result(t, h.call(t, 3, "list_inbox", map[string]any{"session": "cursor.lane-a"})); r["session"] != "cursor.lane-a" {
		t.Fatalf("a call with the login cursor.lane-a = %v", r)
	}
	if r := result(t, h.call(t, 4, "whoami", map[string]any{"session": "cursor.lane-b"})); r["session"] != "cursor.lane-b" {
		t.Fatalf("a first call with the login cursor.lane-b = %v", r)
	}
	if r := result(t, h.call(t, 5, "whoami", nil)); r["session"] != "cursor.s1" {
		t.Fatalf("whoami with no session = %v", r)
	}
	if got := h.srv.namedRequests(); fmt.Sprint(got) != "[lane-a cursor.lane-b]" {
		t.Fatalf("the named lease requests are %q, want one for lane-a and one for cursor.lane-b", got)
	}
	for _, m := range h.srv.toolCalls() {
		p, _ := m["params"].(map[string]any)
		if a, _ := p["arguments"].(map[string]any); a != nil {
			if _, ok := a["session"]; ok {
				t.Fatalf("a call reached the server with the argument session: %v", m)
			}
		}
	}
	// The notice file and the hook show the sessions of the label calls, as for a login.
	waitFor(t, "notice with the sessions of the label calls", func() bool {
		data, _ := os.ReadFile(noticePath(h))
		return strings.Contains(string(data), "cursor.lane-a") && strings.Contains(string(data), "cursor.lane-b")
	})
	// The pool lease and the two named leases: the bridge reads the agent key from the key
	// file at each lease request.
	if n := h.keys.reads.Load(); n != 3 {
		t.Fatalf("reads of the agent key = %d, want 3: one for each lease request", n)
	}
	h.stop(t)
}

func TestALoginGivesTheHandleOfTheLeaseOfALabelCall(t *testing.T) {
	h := startNamed(t)
	if r := result(t, h.call(t, 1, "whoami", map[string]any{"session": "lane-a"})); r["session"] != "cursor.lane-a" {
		t.Fatalf("whoami with the label lane-a = %v", r)
	}
	a := h.login(t, 2, "lane-a")
	if !handleForm.MatchString(fmt.Sprint(a["session"])) || a["login"] != "cursor.lane-a" {
		t.Fatalf("login lane-a after the label call = %v", a)
	}
	if again := h.login(t, 3, "cursor.lane-a"); again["session"] != a["session"] {
		t.Fatalf("a second login gave the handle %v, not %v", again["session"], a["session"])
	}
	if r := result(t, h.call(t, 4, "whoami", map[string]any{"session": a["session"]})); r["session"] != "cursor.lane-a" {
		t.Fatalf("whoami with the handle = %v", r)
	}

	// A label call after a login uses the lease of the login.
	b := h.login(t, 5, "lane-b")
	if r := result(t, h.call(t, 6, "whoami", map[string]any{"session": "lane-b"})); r["session"] != "cursor.lane-b" {
		t.Fatalf("whoami with the label lane-b after its login = %v", r)
	}
	if got := h.srv.namedRequests(); fmt.Sprint(got) != "[lane-a lane-b]" {
		t.Fatalf("the named lease requests are %q, want one for each session", got)
	}
	if b["session"] == a["session"] {
		t.Fatal("two sessions have the same handle")
	}
	h.stop(t)
}

func TestFirstCallsAtTheSameTimeForTheSameSessionMakeOneLeaseRequest(t *testing.T) {
	var hold atomic.Bool
	reached := make(chan struct{}, 1)
	h := newWrappedHarness(t, slowNamedAnswer(&hold, reached))
	h.start(context.Background())
	h.firstBeats(t, "cursor.s1")
	hold.Store(true)
	names := []string{"lane-a", "lane-a", "cursor.lane-a", "lane-a", "cursor.lane-a", "lane-a"}
	h.sendCall(t, 1, "whoami", map[string]any{"session": names[0]})
	awaitReached(t, reached, "lease request for the first call with lane-a")
	for i, name := range names[1:] {
		h.sendCall(t, 2+i, "whoami", map[string]any{"session": name})
	}
	answers := h.recvByID(t, len(names))
	for id, a := range answers {
		if r := result(t, a); r["session"] != "cursor.lane-a" {
			t.Fatalf("answer %d = %v", id, a)
		}
	}
	if got := h.srv.namedRequests(); len(got) != 1 {
		t.Fatalf("the named lease requests are %q, want one", got)
	}
	h.stop(t)
	if left := h.srv.leasedSessions(); len(left) != 0 {
		t.Fatalf("after the stop, the server has leases of the sessions %q", left)
	}
}

func TestACallWithALabelGivesTheToolErrorsOfLogin(t *testing.T) {
	h := startNamed(t)
	h.srv.mu.Lock()
	h.srv.valid["lease_held-by-a-different-bridge"] = "cursor.lane-c"
	h.srv.mu.Unlock()
	cases := []struct {
		session string
		code    string
	}{
		{"lane-c", "SESSION_BUSY"},
		{"gone", "SESSION_REVOKED"},
		{"cursor.gone", "SESSION_REVOKED"},
		{"nope", "SESSION_UNKNOWN"},
		{"codex.lane-a", "SESSION_UNKNOWN"},
		{"s2", "BAD_LABEL"},
		{"cursor.s1", "BAD_LABEL"},
	}
	for i, c := range cases {
		wantSessionError(t, h.call(t, 10+i, "whoami", map[string]any{"session": c.session}), c.code, false)
		// The same call again gets the same error: a refusal keeps no lease.
		wantSessionError(t, h.call(t, 30+i, "list_inbox", map[string]any{"session": c.session}), c.code, false)
		if code := errorCode(t, h.call(t, 50+i, "login", map[string]any{"session": c.session})); code != c.code {
			t.Fatalf("login %s = %s, want %s as for the label call", c.session, code, c.code)
		}
	}
	if r := result(t, h.call(t, 70, "whoami", nil)); r["session"] != "cursor.s1" {
		t.Fatalf("the pool after the errors = %v", r)
	}
	for _, m := range h.srv.toolCalls() {
		if p, _ := m["params"].(map[string]any); p["name"] != "whoami" || p["arguments"] != nil {
			t.Fatalf("a refused call reached the server: %v", m)
		}
	}
	h.stop(t)
}

func TestASessionOfNoValidFormGivesInvalidArgumentAndNoLeaseRequest(t *testing.T) {
	h := startNamed(t)
	for i, s := range []any{"Lane-A", "lane a", "lane_a", "-lane", "lane-a\n", "a.b.c", "cursor.", ".lane-a",
		"cursor.lane-a@mai.internal", strings.Repeat("l", 33), 42, true, map[string]any{"label": "lane-a"}} {
		wantSessionError(t, h.call(t, 10+i, "whoami", map[string]any{"session": s}), "INVALID_ARGUMENT", false)
	}
	if got := h.srv.namedRequests(); len(got) != 0 {
		t.Fatalf("the named lease requests are %q, want none", got)
	}
	if calls := h.srv.toolCalls(); len(calls) != 0 {
		t.Fatalf("a call reached the server: %v", calls)
	}
	h.stop(t)
}

func TestASessionNameAgreesWithTheRulesOfTheServer(t *testing.T) {
	for _, s := range []string{"a", "lane-1", "s", "s1", "s1a", "0", strings.Repeat("l", 32), "cursor.lane-1",
		"cursor.s1", "owner.lane-1", strings.Repeat("a", 48) + "." + strings.Repeat("b", 32), "cursor.ses-abcdefghijklmnop"} {
		if !isSessionName(s) {
			t.Errorf("isSessionName(%q) = false", s)
		}
	}
	for _, s := range []string{"", "ses-abcdefghijklmnop", strings.Repeat("l", 33), strings.Repeat("a", 49) + ".b",
		"a." + strings.Repeat("b", 33), "-a", "a_b", "A", "lane-1\n", "a.b.c", "a..b", ".a", "a.", "a@mai.internal"} {
		if isSessionName(s) {
			t.Errorf("isSessionName(%q) = true", s)
		}
	}
}

// A server error or a network error of the first lease request of a label call gives a tool
// error with retry_safe true, as a login does. The next call with the label gets the lease.
func TestACallWithALabelGetsTheLeaseAfterAServerError(t *testing.T) {
	var fail atomic.Int32
	h := newWrappedHarness(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isNamedRequest(r) {
				switch fail.Load() {
				case 1:
					writeErr(w, 503, "MAIL_SERVER_DOWN")
					return
				case 2:
					writeProblem(w)
					return
				case 3:
					if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
						_ = conn.Close()
					}
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	})
	h.start(context.Background())
	h.firstBeats(t, "cursor.s1")
	for i, code := range []string{"MAIL_SERVER_DOWN", "SERVER_UNAVAILABLE", "SERVER_UNAVAILABLE"} {
		fail.Store(int32(i + 1))
		wantSessionError(t, h.call(t, 10+i, "whoami", map[string]any{"session": "lane-a"}), code, true)
	}
	fail.Store(0)
	if r := result(t, h.call(t, 20, "whoami", map[string]any{"session": "lane-a"})); r["session"] != "cursor.lane-a" {
		t.Fatalf("whoami with lane-a after the server errors = %v", r)
	}
	h.stop(t)
}

func TestALogoutWithALabelOrALoginEndsTheNamedLease(t *testing.T) {
	h := startNamed(t)
	if r := result(t, h.call(t, 1, "whoami", map[string]any{"session": "lane-a"})); r["session"] != "cursor.lane-a" {
		t.Fatalf("whoami with lane-a = %v", r)
	}
	b := h.login(t, 2, "lane-b")["session"]
	waitFor(t, "notice with lane-a and lane-b", func() bool {
		data, _ := os.ReadFile(noticePath(h))
		return strings.Contains(string(data), "cursor.lane-a") && strings.Contains(string(data), "cursor.lane-b")
	})

	r := result(t, h.call(t, 3, "logout", map[string]any{"session": "lane-a"}))
	sc, _ := r["structuredContent"].(map[string]any)
	if r["isError"] == true || !handleForm.MatchString(fmt.Sprint(sc["session"])) || sc["login"] != "cursor.lane-a" {
		t.Fatalf("logout lane-a = %v", r)
	}
	h.srv.mu.Lock()
	ended, busy := len(h.srv.ended), h.srv.leased("cursor.lane-a")
	h.srv.mu.Unlock()
	if ended != 1 || busy {
		t.Fatalf("after logout: ended = %d, cursor.lane-a leased = %v", ended, busy)
	}
	data, _ := os.ReadFile(noticePath(h))
	if strings.Contains(string(data), "cursor.lane-a") || !strings.Contains(string(data), "cursor.lane-b") {
		t.Fatalf("notice after logout = %s", data)
	}
	for i, s := range []string{"lane-a", "cursor.lane-a", "s1", "cursor.s1", "lane-z"} {
		a := h.call(t, 10+i, "logout", map[string]any{"session": s})
		wantSessionError(t, a, "SESSION_UNKNOWN", false)
		if text := fmt.Sprint(result(t, a)["content"]); !strings.Contains(text, "no lease for the session") {
			t.Fatalf("logout %s: content = %s", s, text)
		}
	}
	wantSessionError(t, h.call(t, 20, "logout", map[string]any{"session": "Lane-A"}), "INVALID_ARGUMENT", false)
	wantSessionError(t, h.call(t, 21, "logout", map[string]any{"session": 7}), "INVALID_ARGUMENT", false)
	if r := result(t, h.call(t, 22, "whoami", nil)); r["session"] != "cursor.s1" {
		t.Fatalf("the pool after a logout of s1 = %v", r)
	}

	// The next call with the label gets a new lease.
	if r := result(t, h.call(t, 30, "whoami", map[string]any{"session": "lane-a"})); r["session"] != "cursor.lane-a" {
		t.Fatalf("whoami with lane-a after the logout = %v", r)
	}
	if r := result(t, h.call(t, 31, "logout", map[string]any{"session": "cursor.lane-a"})); r["isError"] == true {
		t.Fatalf("logout cursor.lane-a = %v", r)
	}
	r = result(t, h.call(t, 32, "logout", map[string]any{"session": "lane-b"}))
	if sc, _ := r["structuredContent"].(map[string]any); r["isError"] == true || sc["session"] != b {
		t.Fatalf("logout lane-b = %v, want the handle %v of its login", r, b)
	}
	if code := errorCode(t, h.call(t, 33, "whoami", map[string]any{"session": b})); code != "SESSION_UNKNOWN" {
		t.Fatalf("whoami with the handle of lane-b after its logout = %s", code)
	}
	if left := h.srv.leasedSessions(); fmt.Sprint(left) != "[cursor.s1]" {
		t.Fatalf("after the logouts, the server has leases of the sessions %q", left)
	}
	h.stop(t)
}

// endsAfterRelease gives a handler that holds the next tools/call while hold is true. When
// release closes, the handler answers 401 LEASE_UNKNOWN, as the server does when the lease of
// the call ended before the call.
func endsAfterRelease(hold *atomic.Bool, reached chan<- struct{}, release <-chan struct{}) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/mcp" || !hold.CompareAndSwap(true, false) {
				next.ServeHTTP(w, r)
				return
			}
			reached <- struct{}{}
			<-release
			writeErr(w, 401, "LEASE_UNKNOWN")
		})
	}
}

// A logout of lane-a ends its lease while a call with the label lane-a goes to the server.
// The call must get a new lease for lane-a, as the first call with the label does. It must
// not get SESSION_UNKNOWN: the chat gave a label, not the handle of the ended lease.
func TestACallWithALabelGetsANewLeaseWhenALogoutEndsTheLeaseDuringTheCall(t *testing.T) {
	var hold atomic.Bool
	reached := make(chan struct{}, 1)
	release := make(chan struct{})
	h := newWrappedHarness(t, endsAfterRelease(&hold, reached, release))
	h.start(context.Background())
	if r := result(t, h.call(t, 1, "whoami", map[string]any{"session": "lane-a"})); r["session"] != "cursor.lane-a" {
		t.Fatalf("whoami with lane-a = %v", r)
	}
	hold.Store(true)
	h.sendCall(t, 2, "whoami", map[string]any{"session": "lane-a"})
	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		t.Fatal("the call did not reach the server")
	}
	if r := result(t, h.call(t, 3, "logout", map[string]any{"session": "lane-a"})); r["isError"] == true {
		t.Fatalf("logout lane-a = %v", r)
	}
	close(release)
	a := h.recv(t)
	if a["id"] != float64(2) || result(t, a)["session"] != "cursor.lane-a" {
		t.Fatalf("the call during the logout = %v", a)
	}
	if got := h.srv.namedRequests(); len(got) != 2 {
		t.Fatalf("the named lease requests are %q, want two", got)
	}
	h.stop(t)
	if left := h.srv.leasedSessions(); len(left) != 0 {
		t.Fatalf("after the stop, the server has leases of the sessions %q", left)
	}
}

func TestTheStopEndsTheLeaseOfALabelCall(t *testing.T) {
	h := startNamed(t)
	if r := result(t, h.call(t, 1, "whoami", map[string]any{"session": "lane-a"})); r["session"] != "cursor.lane-a" {
		t.Fatalf("whoami with lane-a = %v", r)
	}
	waitFor(t, "notice file", func() bool { _, err := os.Stat(noticePath(h)); return err == nil })
	h.stop(t)
	if _, err := os.Stat(noticePath(h)); !os.IsNotExist(err) {
		t.Fatalf("the notice file is still there: %v", err)
	}
	h.srv.mu.Lock()
	defer h.srv.mu.Unlock()
	if len(h.srv.ended) != 2 || len(h.srv.valid) != 0 {
		t.Fatalf("ended = %v, valid = %v", h.srv.ended, h.srv.valid)
	}
}

// The context of the bridge ends while the first call with a label waits for the answer of
// its lease request. The bridge must end that lease. Then no lease stays on the server.
func TestTheStopOfTheBridgeDuringTheFirstCallWithALabelLeavesNoLease(t *testing.T) {
	var hold atomic.Bool
	reached := make(chan struct{}, 1)
	h := newWrappedHarness(t, slowNamedAnswer(&hold, reached))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.start(ctx)
	h.firstBeats(t, "cursor.s1")

	hold.Store(true)
	h.sendCall(t, 1, "whoami", map[string]any{"session": "lane-a"})
	awaitReached(t, reached, "lease request for the first call with lane-a")
	cancel()
	go func() { _, _ = io.Copy(io.Discard, h.out) }()
	if code := h.wait(t); code != ExitOK {
		t.Fatalf("exit = %d, stderr: %s", code, h.stderr.String())
	}
	if left := h.srv.leasedSessions(); len(left) != 0 {
		t.Fatalf("after the stop of the bridge, the server has leases of the sessions %q", left)
	}
}
