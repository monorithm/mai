package bridge

import (
	"context"
	"testing"
	"time"
)

// startWait starts a bridge with the examination gone of the app. A call with the label
// lane-a gets the named lease, and then a wait_for_mail on that lease stays on the server.
// startWait then closes the standard input of the bridge, and reads each answer of the bridge.
func startWait(t *testing.T, gone func() bool) *harness {
	t.Helper()
	h := newHarness(t, memoryWithKey())
	h.b.AppGone = gone
	h.start(context.Background())
	h.send(t, `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{}}`)
	h.recv(t)
	if a := h.call(t, 1, "whoami", map[string]any{"session": "lane-a"}); result(t, a)["session"] != "cursor.lane-a" {
		t.Fatalf("whoami with lane-a = %v", a)
	}
	h.srv.mu.Lock()
	h.srv.block = make(chan struct{})
	h.srv.mu.Unlock()
	t.Cleanup(func() {
		h.srv.mu.Lock()
		close(h.srv.block)
		h.srv.mu.Unlock()
	})
	h.send(t, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"wait_for_mail","arguments":{"session":"lane-a"}}}`)
	waitFor(t, "the wait on the server", func() bool {
		h.srv.mu.Lock()
		defer h.srv.mu.Unlock()
		return len(h.srv.mcpCalls) >= 3
	})
	_ = h.in.Close()
	go func() {
		for {
			if _, err := h.out.ReadString('\n'); err != nil {
				return
			}
		}
	}()
	return h
}

// leasesLeft gives the count of the leases that the fake server holds.
func leasesLeft(h *harness) int {
	h.srv.mu.Lock()
	defer h.srv.mu.Unlock()
	return len(h.srv.valid)
}

// After the end of the standard input, a call can be in flight, for example wait_for_mail.
// When the app is gone, nothing can read the answer of the call. The bridge then stops the
// call at once and ends each lease. Else a new bridge of the app gets SESSION_BUSY for the
// same session until the call ends.
func TestTheBridgeStopsACallInFlightWhenTheAppIsGone(t *testing.T) {
	h := startWait(t, func() bool { return true })
	start := time.Now()
	if code := h.wait(t); code != ExitOK {
		t.Fatalf("exit %d, stderr: %s", code, h.stderr.String())
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("the bridge stopped %s after the end of the standard input", d)
	}
	if n := leasesLeft(h); n != 0 {
		t.Errorf("after the stop of the bridge, the server holds %d leases", n)
	}
}

// When the bridge cannot tell if the app is gone, it waits for the answer of a call in
// flight for a maximum of drainTimeout. Then it stops the call and ends each lease.
func TestTheBridgeWaitsForACallInFlightOnlyForALimitedTime(t *testing.T) {
	old := drainTimeout
	drainTimeout = 300 * time.Millisecond
	t.Cleanup(func() { drainTimeout = old })
	h := startWait(t, nil)
	start := time.Now()
	if code := h.wait(t); code != ExitOK {
		t.Fatalf("exit %d, stderr: %s", code, h.stderr.String())
	}
	if d := time.Since(start); d < drainTimeout || d > 5*time.Second {
		t.Errorf("the bridge stopped %s after the end of the standard input, want %s or more", d, drainTimeout)
	}
	if n := leasesLeft(h); n != 0 {
		t.Errorf("after the stop of the bridge, the server holds %d leases", n)
	}
}
