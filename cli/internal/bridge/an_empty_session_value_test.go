package bridge

import "testing"

// The reference of the MCP tools tells that a value of session that is not a label, a login,
// or a handle gives INVALID_ARGUMENT with the path session. The pattern of session in
// tools/list refuses an empty string and null, and the server gives INVALID_ARGUMENT for
// them. A call with such a value must not act as the pool session.
func TestACallWithAnEmptyOrNullSessionDoesNotActAsThePoolSession(t *testing.T) {
	h := startNamed(t)
	for i, v := range []any{"", nil} {
		a := h.call(t, 10+i, "whoami", map[string]any{"session": v})
		r := result(t, a)
		if r["isError"] != true {
			t.Errorf("whoami with session %#v acted as the session %v", v, r["session"])
			continue
		}
		wantSessionError(t, a, "INVALID_ARGUMENT", false)
	}
	h.stop(t)
}
