package bridge

import (
	"testing"
)

// login and a tool call take the same values of session.
func TestLoginRefusesEachSessionValueThatACallRefuses(t *testing.T) {
	h := startNamed(t)
	for i, v := range []string{"lane-a\n", " lane-b", "lane-c "} {
		call := h.call(t, 10+i, "whoami", map[string]any{"session": v})
		login := h.call(t, 20+i, "login", map[string]any{"session": v})
		t.Logf("%q: call %v, login %v", v, result(t, call)["structuredContent"], result(t, login)["structuredContent"])
		if (errorCode(t, call) == "") != (errorCode(t, login) == "") {
			t.Errorf("session %q: a call gives %q and login gives %q", v, errorCode(t, call), errorCode(t, login))
		}
	}
	h.stop(t)
}

// logout takes the same values of session as a call: with no value, the result is
// INVALID_ARGUMENT, not SESSION_UNKNOWN for a handle that the call did not give.
func TestLogoutWithNoSessionGivesInvalidArgument(t *testing.T) {
	h := startNamed(t)
	for i, args := range []map[string]any{{}, {"session": ""}, {"session": nil}} {
		e := toolErr(t, h.call(t, 30+i, "logout", args))
		if e["code"] != "INVALID_ARGUMENT" || e["path"] != "session" {
			t.Errorf("logout %v = %v, want INVALID_ARGUMENT with the path session", args, e)
		}
	}
	h.stop(t)
}

// login refuses a value that is not a label or a login, before a lease request. The tool
// error has the path session and retry_safe false.
func TestLoginOfAValueThatIsNotALabelOrALoginSendsNoLeaseRequest(t *testing.T) {
	h := startNamed(t)
	for i, v := range []any{"lane-a\n", " lane-b", "lane-c ", "Lane-A", "ses-abcdefghijklmnop", "", 7, nil} {
		wantSessionError(t, h.call(t, 10+i, "login", map[string]any{"session": v}), "BAD_LABEL", false)
	}
	wantSessionError(t, h.call(t, 20, "login", map[string]any{}), "BAD_LABEL", false)
	if got := h.srv.namedRequests(); len(got) != 0 {
		t.Fatalf("the named lease requests are %q, want none", got)
	}
	if r := h.login(t, 21, "cursor.lane-a"); r["login"] != "cursor.lane-a" {
		t.Fatalf("login cursor.lane-a = %v", r)
	}
	h.stop(t)
}
