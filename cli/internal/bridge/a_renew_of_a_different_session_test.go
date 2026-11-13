package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// The server ends the lease of the named session lane-a, and then a server that does not know
// named sessions gives a pool session for the new lease request. A call with the handle of
// lane-a must not act as that pool session. The bridge ends the new lease, keeps no handle,
// and does not send the call.
func TestARenewOfANamedLeaseNeverActsAsADifferentSession(t *testing.T) {
	var old atomic.Bool
	var given atomic.Value // the lease that the server gave for the request of lane-a
	h := newWrappedHarness(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !old.Load() || !isNamedRequest(r) {
				next.ServeHTTP(w, r)
				return
			}
			body := leaseBody(r)
			delete(body, "session")
			raw, _ := json.Marshal(body)
			r.Body = io.NopCloser(bytes.NewReader(raw))
			r.ContentLength = int64(len(raw))
			rec := httptest.NewRecorder()
			next.ServeHTTP(rec, r)
			var a struct {
				Lease string `json:"lease"`
			}
			_ = json.Unmarshal(rec.Body.Bytes(), &a)
			given.Store(a.Lease)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(rec.Code)
			_, _ = w.Write(rec.Body.Bytes())
		})
	})
	// With a heartbeat of 60 s, no second heartbeat comes during the test. Thus the call with
	// the handle does the renew. A heartbeat that does the renew first drops the handle, and
	// then the call gets SESSION_UNKNOWN.
	h.srv.hbSeconds = 60
	h.start(context.Background())
	h.send(t, `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{}}`)
	h.recv(t)
	handle := h.login(t, 1, "lane-a")["session"].(string)
	// A new lease sends its first heartbeat immediately, also with a heartbeat of 60 s. When
	// that heartbeat comes to the server after the expiry, it does the renew. Thus wait for the
	// first heartbeat of each lease before the expiry.
	h.firstBeats(t, "cursor.s1", "cursor.lane-a")
	old.Store(true)
	h.srv.expire()
	h.srv.mu.Lock()
	calls := len(h.srv.mcpCalls)
	h.srv.mu.Unlock()
	a := h.call(t, 2, "whoami", map[string]any{"session": handle})
	wantSessionError(t, a, "SERVER_UNAVAILABLE", false)
	lease, _ := given.Load().(string)
	if lease == "" {
		t.Fatal("the bridge sent no lease request for lane-a")
	}
	h.srv.mu.Lock()
	if s, ok := h.srv.valid[lease]; ok {
		t.Errorf("the server holds the lease of %s that it gave for lane-a", s)
	}
	// The call with the handle came to the server one time, and got LEASE_UNKNOWN. The
	// bridge did not send it again with the lease of the pool session.
	if n := len(h.srv.mcpCalls) - calls; n != 1 {
		t.Errorf("the call came to the server %d times after the renew, want 1", n)
	}
	h.srv.mu.Unlock()
	if code := errorCode(t, h.call(t, 3, "whoami", map[string]any{"session": handle})); code != "SESSION_UNKNOWN" {
		t.Errorf("a call with the handle after the refused renew gave %q, want SESSION_UNKNOWN", code)
	}
	h.stop(t)
}
