package bridge

import "testing"

// The reference of the MCP tools tells that a value of session that is not a label, a login,
// or a handle gives INVALID_ARGUMENT with the path session. An empty string is such a value.
// A call with that value must not act as the pool session.
func TestACallWithAnEmptySessionDoesNotActAsThePoolSession(t *testing.T) {
	h := startNamed(t)
	a := h.call(t, 10, "whoami", map[string]any{"session": ""})
	if r := result(t, a); r["isError"] != true {
		t.Fatalf("whoami with an empty session acted as the session %v", r["session"])
	}
	wantSessionError(t, a, "INVALID_ARGUMENT", false)
	h.stop(t)
}

// Some clients send null for each optional argument that the model did not fill. A call with
// null as session is the same as a call with no argument session: it uses the pool lease,
// and the server gets no argument session.
func TestACallWithANullSessionUsesThePoolSession(t *testing.T) {
	h := startNamed(t)
	r := result(t, h.call(t, 11, "whoami", map[string]any{"session": nil}))
	if args, _ := r["arguments"].(map[string]any); r["session"] != "cursor.s1" || len(args) != 0 {
		t.Fatalf("whoami with a null session = %v", r)
	}
	h.stop(t)
}
