package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

var handleForm = regexp.MustCompile(`^ses-[a-z2-7]{16}$`)

// call sends one tools/call and returns the answer.
func (h *harness) call(t *testing.T, id int, name string, args map[string]any) map[string]any {
	t.Helper()
	params := map[string]any{"name": name}
	if args != nil {
		params["arguments"] = args
	}
	line, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": params})
	h.send(t, string(line))
	a := h.recv(t)
	if a["id"] != float64(id) {
		t.Fatalf("answer for id %d = %v", id, a)
	}
	return a
}

// result gives the result of an answer.
func result(t *testing.T, a map[string]any) map[string]any {
	t.Helper()
	r, ok := a["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result in %v", a)
	}
	return r
}

// login calls the tool login and returns the structured content of a good result.
func (h *harness) login(t *testing.T, id int, session string) map[string]any {
	t.Helper()
	r := result(t, h.call(t, id, "login", map[string]any{"session": session}))
	if r["isError"] == true {
		t.Fatalf("login %s failed: %v", session, r)
	}
	sc, _ := r["structuredContent"].(map[string]any)
	content, _ := r["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("login %s: content = %v", session, r["content"])
	}
	var text map[string]any
	if err := json.Unmarshal([]byte(content[0].(map[string]any)["text"].(string)), &text); err != nil || fmt.Sprint(text) != fmt.Sprint(sc) {
		t.Fatalf("login %s: the text %v is not the structured content %v", session, content[0], sc)
	}
	return sc
}

// errorCode gives the code of a tool result with isError true, or "".
func errorCode(t *testing.T, a map[string]any) string {
	t.Helper()
	r := result(t, a)
	if r["isError"] != true {
		return ""
	}
	sc, _ := r["structuredContent"].(map[string]any)
	e, _ := sc["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}

// startNamed starts a bridge and waits for the answer of initialize.
func startNamed(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t, memoryWithKey())
	h.start(context.Background())
	h.send(t, `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{}}`)
	h.recv(t)
	return h
}

func (h *harness) stop(t *testing.T) {
	t.Helper()
	_ = h.in.Close()
	if code := h.wait(t); code != ExitOK {
		t.Fatalf("exit = %d, stderr: %s", code, h.stderr.String())
	}
}

// waitFor runs check until it is true, for up to 5 seconds.
func waitFor(t *testing.T, what string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no %s", what)
}

func TestTwoLoginsInOneBridgeGiveTwoHandles(t *testing.T) {
	h := startNamed(t)
	a := h.login(t, 1, "lane-a")
	b := h.login(t, 2, "cursor.lane-b")
	for _, sc := range []map[string]any{a, b} {
		if !handleForm.MatchString(fmt.Sprint(sc["session"])) || sc["agent"] != "cursor" {
			t.Fatalf("login result = %v", sc)
		}
	}
	if a["login"] != "cursor.lane-a" || b["login"] != "cursor.lane-b" || a["session"] == b["session"] {
		t.Fatalf("login results = %v and %v", a, b)
	}
	h.stop(t)
	h.srv.mu.Lock()
	defer h.srv.mu.Unlock()
	if n := h.keys.reads.Load(); n != 3 {
		t.Fatalf("reads of the agent key = %d, want 3: one for each lease request", n)
	}
	if len(h.srv.leaseReqs) != 3 || h.srv.leaseReqs[1]["session"] != "lane-a" || h.srv.leaseReqs[2]["session"] != "cursor.lane-b" {
		t.Fatalf("lease requests = %v", h.srv.leaseReqs)
	}
	if _, ok := h.srv.leaseReqs[0]["session"]; ok {
		t.Fatalf("the pool lease request names a session: %v", h.srv.leaseReqs[0])
	}
	for _, req := range h.srv.leaseReqs {
		if req["app"] != "Cursor" || req["folder"] != "/work/project" || req["pid"] != float64(os.Getpid()) {
			t.Fatalf("lease request = %v", req)
		}
	}
	for _, m := range h.srv.mcpCalls {
		if p, _ := m["params"].(map[string]any); p["name"] == "login" {
			t.Fatalf("the login reached the server: %v", m)
		}
	}
}

func TestWhoamiGoesWithTheLeaseOfTheHandle(t *testing.T) {
	h := startNamed(t)
	handle := h.login(t, 1, "lane-a")["session"].(string)

	r := result(t, h.call(t, 2, "whoami", map[string]any{"session": handle, "verbose": true}))
	if r["session"] != "cursor.lane-a" {
		t.Fatalf("whoami with the handle = %v", r)
	}
	args, _ := r["arguments"].(map[string]any)
	if _, ok := args["session"]; ok || args["verbose"] != true {
		t.Fatalf("the server got the arguments %v", args)
	}
	if r := result(t, h.call(t, 3, "whoami", nil)); r["session"] != "cursor.s1" {
		t.Fatalf("whoami with no session = %v", r)
	}
	// null is not a label, a login, or a handle, as on the server.
	wantSessionError(t, h.call(t, 4, "whoami", map[string]any{"session": nil}), "INVALID_ARGUMENT", false)
	h.stop(t)
}

func TestAnUnknownHandleGivesSessionUnknown(t *testing.T) {
	h := startNamed(t)
	h.login(t, 1, "lane-a")
	for i, s := range []any{"ses-aaaaaaaaaaaaaaaa", "ses-abcdefghijklmnop"} {
		a := h.call(t, 10+i, "whoami", map[string]any{"session": s})
		if code := errorCode(t, a); code != "SESSION_UNKNOWN" {
			t.Fatalf("whoami with %v = %v", s, a)
		}
		if text := fmt.Sprint(result(t, a)["content"]); !strings.Contains(text, "call login again") {
			t.Fatalf("whoami with %v: content = %s", s, text)
		}
	}
	h.stop(t)
	h.srv.mu.Lock()
	defer h.srv.mu.Unlock()
	for _, m := range h.srv.mcpCalls {
		if p, _ := m["params"].(map[string]any); p["name"] == "whoami" {
			t.Fatalf("a call with an unknown handle reached the server: %v", m)
		}
	}
}

func TestLoginGivesTheCodeOfTheServer(t *testing.T) {
	h := startNamed(t)
	h.srv.mu.Lock()
	h.srv.valid["lease_held-by-a-different-bridge"] = "cursor.lane-c"
	h.srv.mu.Unlock()
	cases := []struct {
		args map[string]any
		code string
	}{
		{map[string]any{"session": "lane-c"}, "SESSION_BUSY"},
		{map[string]any{"session": "gone"}, "SESSION_REVOKED"},
		{map[string]any{"session": "nope"}, "SESSION_UNKNOWN"},
		{map[string]any{"session": "codex.lane-a"}, "SESSION_UNKNOWN"},
		{map[string]any{"session": "Bad_Label"}, "BAD_LABEL"},
		{map[string]any{}, "BAD_LABEL"},
		{map[string]any{"session": 7}, "BAD_LABEL"},
	}
	for i, c := range cases {
		a := h.call(t, 20+i, "login", c.args)
		if code := errorCode(t, a); code != c.code {
			t.Errorf("login %v = %v, want %s", c.args, a, c.code)
		}
	}
	if r := result(t, h.call(t, 40, "whoami", nil)); r["session"] != "cursor.s1" {
		t.Fatalf("the pool after the errors = %v", r)
	}
	h.stop(t)
}

func TestASecondLoginToTheSameSessionGivesTheSameHandle(t *testing.T) {
	h := startNamed(t)
	first := h.login(t, 1, "lane-a")
	second := h.login(t, 2, "lane-a")
	third := h.login(t, 3, "cursor.lane-a")
	if first["session"] != second["session"] || first["session"] != third["session"] {
		t.Fatalf("handles = %v, %v, %v", first, second, third)
	}
	h.stop(t)
	h.srv.mu.Lock()
	defer h.srv.mu.Unlock()
	if len(h.srv.leaseReqs) != 2 {
		t.Fatalf("lease requests = %v", h.srv.leaseReqs)
	}
}

func TestLogoutEndsTheLeaseOfTheHandle(t *testing.T) {
	h := startNamed(t)
	handle := h.login(t, 1, "lane-a")["session"].(string)
	keep := h.login(t, 2, "lane-b")["session"].(string)
	waitFor(t, "notice with two named sessions", func() bool {
		data, _ := os.ReadFile(noticePath(h))
		return strings.Contains(string(data), "cursor.lane-a") && strings.Contains(string(data), "cursor.lane-b")
	})

	r := result(t, h.call(t, 3, "logout", map[string]any{"session": handle}))
	sc, _ := r["structuredContent"].(map[string]any)
	if r["isError"] == true || sc["session"] != handle || sc["login"] != "cursor.lane-a" {
		t.Fatalf("logout = %v", r)
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
	if code := errorCode(t, h.call(t, 4, "whoami", map[string]any{"session": handle})); code != "SESSION_UNKNOWN" {
		t.Fatalf("whoami after logout = %s", code)
	}
	if code := errorCode(t, h.call(t, 5, "logout", map[string]any{"session": handle})); code != "SESSION_UNKNOWN" {
		t.Fatalf("second logout = %s", code)
	}
	if code := errorCode(t, h.call(t, 6, "logout", nil)); code != "INVALID_ARGUMENT" {
		t.Fatalf("logout with no handle = %s", code)
	}
	if r := result(t, h.call(t, 7, "whoami", map[string]any{"session": keep})); r["session"] != "cursor.lane-b" {
		t.Fatalf("whoami of the other handle = %v", r)
	}
	again := h.login(t, 8, "lane-a")["session"]
	if again == handle {
		t.Fatal("a login after logout gives the old handle")
	}
	h.stop(t)
}

func TestEachLeaseHasAHeartbeatAndANotice(t *testing.T) {
	h := startNamed(t)
	h.srv.mu.Lock()
	h.srv.mail["cursor.s1"] = 2
	h.srv.mail["cursor.lane-a"] = 1
	h.srv.mail["cursor.lane-b"] = 0
	h.srv.mu.Unlock()
	h.login(t, 1, "lane-b")
	h.login(t, 2, "lane-a")
	waitFor(t, "two heartbeats of each lease", func() bool {
		h.srv.mu.Lock()
		defer h.srv.mu.Unlock()
		return h.srv.beats["cursor.s1"] >= 2 && h.srv.beats["cursor.lane-a"] >= 2 && h.srv.beats["cursor.lane-b"] >= 2
	})
	var n struct {
		Session string `json:"session"`
		NewMail int    `json:"new_mail"`
		Named   []struct {
			Session   string `json:"session"`
			NewMail   int    `json:"new_mail"`
			UpdatedAt string `json:"updated_at"`
		} `json:"named"`
	}
	// The pool session is first, then the named sessions, sorted by login.
	waitFor(t, "notice of each lease", func() bool {
		data, _ := os.ReadFile(noticePath(h))
		n.Named = nil
		return json.Unmarshal(data, &n) == nil && len(n.Named) == 2 && n.Named[0].UpdatedAt != "" && n.Named[1].UpdatedAt != "" &&
			n.Session == "cursor.s1" && n.NewMail == 2 && n.Named[0].Session == "cursor.lane-a" && n.Named[0].NewMail == 1 &&
			n.Named[1].Session == "cursor.lane-b" && n.Named[1].NewMail == 0
	})
	data, _ := os.ReadFile(noticePath(h))
	if strings.Contains(string(data), "lease_") || strings.Contains(string(data), "ses-") || strings.Contains(string(data), "mai1:") {
		t.Fatalf("the notice file holds a secret or a handle: %s", data)
	}
	h.stop(t)
}

func TestTheExitEndsEachLease(t *testing.T) {
	h := startNamed(t)
	h.login(t, 1, "lane-a")
	h.login(t, 2, "lane-b")
	waitFor(t, "notice file", func() bool { _, err := os.Stat(noticePath(h)); return err == nil })
	h.stop(t)
	if _, err := os.Stat(noticePath(h)); !os.IsNotExist(err) {
		t.Fatalf("the notice file is still there: %v", err)
	}
	h.srv.mu.Lock()
	defer h.srv.mu.Unlock()
	if len(h.srv.ended) != 3 || len(h.srv.valid) != 0 {
		t.Fatalf("ended = %v, valid = %v", h.srv.ended, h.srv.valid)
	}
}

func TestTheContextEndEndsEachLease(t *testing.T) {
	h := newHarness(t, memoryWithKey())
	ctx, cancel := context.WithCancel(context.Background())
	h.start(ctx)
	h.send(t, `{"jsonrpc":"2.0","id":0,"method":"ping"}`)
	h.recv(t)
	h.login(t, 1, "lane-a")
	cancel()
	if code := h.wait(t); code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	h.srv.mu.Lock()
	defer h.srv.mu.Unlock()
	if len(h.srv.ended) != 2 || len(h.srv.valid) != 0 {
		t.Fatalf("ended = %v, valid = %v", h.srv.ended, h.srv.valid)
	}
}

// firstBeats waits for the first heartbeat of each session. The next heartbeat occurs only
// after heartbeat_seconds.
func (h *harness) firstBeats(t *testing.T, sessions ...string) {
	t.Helper()
	waitFor(t, "first heartbeats", func() bool {
		h.srv.mu.Lock()
		defer h.srv.mu.Unlock()
		for _, s := range sessions {
			if h.srv.beats[s] == 0 {
				return false
			}
		}
		return true
	})
}

func TestANamedLeaseRenewsWithTheAgentKey(t *testing.T) {
	h := newHarness(t, memoryWithKey())
	h.srv.hbSeconds = 60
	h.start(context.Background())
	handle := h.login(t, 1, "lane-a")["session"].(string)
	h.firstBeats(t, "cursor.s1", "cursor.lane-a")
	h.srv.expire()
	if r := result(t, h.call(t, 2, "whoami", map[string]any{"session": handle})); r["session"] != "cursor.lane-a" {
		t.Fatalf("whoami after the expiry = %v", r)
	}
	if r := result(t, h.call(t, 3, "whoami", nil)); r["session"] == nil || r["session"] == "" {
		t.Fatalf("the pool after the expiry = %v", r)
	}
	h.stop(t)
	if n := h.keys.reads.Load(); n != 4 {
		t.Fatalf("reads of the agent key = %d, want 4: one for each lease request", n)
	}
	h.srv.mu.Lock()
	defer h.srv.mu.Unlock()
	if len(h.srv.leaseReqs) != 4 || h.srv.leaseReqs[2]["session"] != "cursor.lane-a" {
		t.Fatalf("lease requests = %v", h.srv.leaseReqs)
	}
	if _, ok := h.srv.leaseReqs[3]["session"]; ok {
		t.Fatalf("the renew of the pool names a session: %v", h.srv.leaseReqs[3])
	}
}

func TestARevokedNamedSessionGivesTheCodeAndGoes(t *testing.T) {
	h := newHarness(t, memoryWithKey())
	h.srv.hbSeconds = 60
	h.start(context.Background())
	handle := h.login(t, 1, "lane-a")["session"].(string)
	h.firstBeats(t, "cursor.s1", "cursor.lane-a")
	h.srv.mu.Lock()
	h.srv.named["cursor.lane-a"] = "revoked"
	h.srv.mu.Unlock()
	h.srv.expire()
	if code := errorCode(t, h.call(t, 2, "whoami", map[string]any{"session": handle})); code != "SESSION_REVOKED" {
		t.Fatalf("whoami after the revoke = %s", code)
	}
	if code := errorCode(t, h.call(t, 3, "whoami", map[string]any{"session": handle})); code != "SESSION_UNKNOWN" {
		t.Fatalf("second whoami after the revoke = %s", code)
	}
	if code := errorCode(t, h.call(t, 4, "whoami", nil)); code != "" {
		t.Fatalf("the pool after the revoke = %s", code)
	}
	h.stop(t)
}

func TestTheHeartbeatDropsANamedSessionThatTheServerRefuses(t *testing.T) {
	h := startNamed(t)
	handle := h.login(t, 1, "lane-a")["session"].(string)
	waitFor(t, "notice of lane-a", func() bool {
		data, _ := os.ReadFile(noticePath(h))
		return strings.Contains(string(data), "cursor.lane-a")
	})
	h.srv.mu.Lock()
	h.srv.named["cursor.lane-a"] = "revoked"
	for k, s := range h.srv.valid {
		if s == "cursor.lane-a" {
			delete(h.srv.valid, k)
		}
	}
	h.srv.mu.Unlock()
	waitFor(t, "notice with no lane-a", func() bool {
		data, _ := os.ReadFile(noticePath(h))
		return len(data) > 0 && !strings.Contains(string(data), "cursor.lane-a")
	})
	if code := errorCode(t, h.call(t, 2, "whoami", map[string]any{"session": handle})); code != "SESSION_UNKNOWN" {
		t.Fatalf("whoami after the drop = %s", code)
	}
	if !strings.Contains(h.stderr.String(), "SESSION_REVOKED") {
		t.Fatalf("stderr = %q", h.stderr.String())
	}
	h.stop(t)
}

func TestALongWaitDoesNotStopTheCallsOfADifferentChat(t *testing.T) {
	h := startNamed(t)
	handle := h.login(t, 1, "lane-a")["session"].(string)
	h.srv.mu.Lock()
	h.srv.block = make(chan struct{})
	h.srv.mu.Unlock()
	h.send(t, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"wait_for_mail","arguments":{"session":"`+handle+`"}}}`)
	a := h.call(t, 3, "whoami", nil)
	if result(t, a)["session"] != "cursor.s1" {
		t.Fatalf("whoami during the wait = %v", a)
	}
	h.srv.mu.Lock()
	close(h.srv.block)
	h.srv.mu.Unlock()
	if a := h.recv(t); a["id"] != float64(2) || result(t, a)["session"] != "cursor.lane-a" {
		t.Fatalf("answer of the wait = %v", a)
	}
	h.stop(t)
}

func TestNewHandleHasTheForm(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		h, err := newHandle()
		if err != nil || !handleForm.MatchString(h) || seen[h] {
			t.Fatalf("handle %q, %v", h, err)
		}
		seen[h] = true
	}
}
