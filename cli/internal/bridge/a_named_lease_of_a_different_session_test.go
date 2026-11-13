package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

// A server that does not know named sessions, for example a server of mai 3.0, does not read
// the field session of a lease request. It gives a lease on a pool session. A call with the
// label lane-a must not act as that pool session: the chat then reads and sends the mail of
// a different session.
func TestACallWithALabelNeverActsAsADifferentSession(t *testing.T) {
	h := newWrappedHarness(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isNamedRequest(r) {
				body := leaseBody(r)
				delete(body, "session")
				raw, _ := json.Marshal(body)
				r.Body = io.NopCloser(bytes.NewReader(raw))
				r.ContentLength = int64(len(raw))
			}
			next.ServeHTTP(w, r)
		})
	})
	h.start(context.Background())
	a := h.call(t, 1, "whoami", map[string]any{"session": "lane-a"})
	r := result(t, a)
	if r["isError"] != true {
		// The fake server gives the session of the lease of the call in the result.
		t.Errorf("whoami with the label lane-a acted as the session %v, not as cursor.lane-a", r["session"])
	}
	l := h.call(t, 2, "login", map[string]any{"session": "lane-a"})
	if lr := result(t, l); lr["isError"] != true {
		sc, _ := lr["structuredContent"].(map[string]any)
		t.Errorf("login lane-a gave the login %v, not cursor.lane-a", sc["login"])
	}
	h.stop(t)
}
