package bridge

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// The server gives MAIL_SERVER_DOWN with its constant message "The mail server does not
// reply." (HTTP 503, retry_safe true). The bridge adds its own text to the message of a
// renew that fails. The message must be one correct text, with no "..".
func TestTheMessageOfAFailedRenewOfANamedLeaseHasNoDoubleFullStop(t *testing.T) {
	var fail atomic.Bool
	yes := true
	h := newWrappedHarness(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if fail.Load() && isNamedRequest(r) {
				writeServerErr(w, 503, "MAIL_SERVER_DOWN", "The mail server does not reply.", &yes)
				return
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
	fail.Store(true)
	h.srv.expire()
	a := h.call(t, 2, "whoami", map[string]any{"session": "lane-a"})
	wantRetrySafe(t, a, "MAIL_SERVER_DOWN")
	msg, _ := toolErr(t, a)["message"].(string)
	if strings.Contains(msg, "..") {
		t.Errorf("the message has a double full stop: %q", msg)
	}
	h.stop(t)
}

func TestTheMessageOfAFailedLeaseRequestIsCorrectForEachForm(t *testing.T) {
	pool, named := &lease{}, &lease{handle: "ses-abcdefghijklmnop", login: "cursor.lane-a"}
	for _, tc := range []struct {
		l    *lease
		err  error
		want string
	}{
		{pool, &apiError{Status: 503, Code: "MAIL_SERVER_DOWN", Message: "The mail server does not reply."},
			"The bridge got no lease for the pool lease: The mail server does not reply. Try again later."},
		{named, &apiError{Status: 503, Code: "STORE_UNAVAILABLE", Message: "The store does not reply."},
			"The bridge got no lease for the session cursor.lane-a: The store does not reply. Try again later."},
		{named, &apiError{Status: 502, Code: "LOGIN_FAILED", Message: "the login failed"},
			"The bridge got no lease for the session cursor.lane-a: the login failed. Try again later."},
	} {
		if _, got := unavailable(tc.l, tc.err); got != tc.want {
			t.Errorf("message = %q, want %q", got, tc.want)
		}
	}
	e := loginError("cursor", errors.New("the server gave no lease."))
	msg := e["structuredContent"].(map[string]any)["error"].(map[string]any)["message"].(string)
	if strings.Contains(msg, "..") {
		t.Errorf("the message of a login has a double full stop: %q", msg)
	}
}
