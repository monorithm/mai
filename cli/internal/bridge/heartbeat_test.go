package bridge

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

// When the server cannot count the new mail, the heartbeat gives "new_mail": null. The
// notice keeps the last count, and the bridge continues.
func TestAHeartbeatWithNoCountOfNewMailKeepsTheLastCount(t *testing.T) {
	var noCount atomic.Bool
	var nulls atomic.Int32
	h := newWrappedHarness(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if noCount.Load() && r.URL.Path == "/api/leases/current/heartbeat" {
				nulls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"session":"cursor.s1","new_mail":null}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	})
	h.start(context.Background())
	waitFor(t, "notice with 3 new mail messages", func() bool {
		n := readNotice(t, h)
		return n != nil && n["session"] == "cursor.s1" && n["new_mail"] == float64(3)
	})
	noCount.Store(true)
	waitFor(t, "two heartbeats with no count", func() bool { return nulls.Load() >= 2 })
	if n := readNotice(t, h); n == nil || n["session"] != "cursor.s1" || n["new_mail"] != float64(3) {
		t.Fatalf("notice after the heartbeats with no count = %v", n)
	}
	h.running(t, 100*time.Millisecond)
	h.stop(t)
}

func TestHeartbeatGetsANewLeaseAfterAnExpiry(t *testing.T) {
	h := newHarness(t, memoryWithKey())
	h.start(context.Background())
	h.send(t, `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	h.recv(t)
	waitForFile(t, noticePath(h))
	h.srv.expire()
	deadline := time.Now().Add(5 * time.Second)
	var data []byte
	for time.Now().Before(deadline) {
		data, _ = os.ReadFile(noticePath(h))
		if bytes.Contains(data, []byte(`"cursor.s2"`)) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !bytes.Contains(data, []byte(`"cursor.s2"`)) {
		t.Fatalf("notice after the new lease = %s", data)
	}
	_ = h.in.Close()
	if code := h.wait(t); code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
}

func TestHeartbeatStopsTheBridgeWhenThePoolRenewGivesBadKey(t *testing.T) {
	h := newHarness(t, memoryWithKey())
	h.start(context.Background())
	h.send(t, `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	h.recv(t)
	h.srv.mu.Lock()
	h.srv.refuseKey = true
	h.srv.mu.Unlock()
	h.srv.expire()
	a := h.recv(t)
	if a["id"] != nil || a["error"] == nil {
		t.Fatalf("answer = %v", a)
	}
	if code := h.wait(t); code != ExitError {
		t.Fatalf("exit = %d", code)
	}
}
