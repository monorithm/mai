package bridge

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"
)

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

func TestHeartbeatStopsTheBridgeWhenNoNewLeaseIsAvailable(t *testing.T) {
	h := newHarness(t, memoryWithKey())
	h.srv.mu.Lock()
	h.srv.refuseNew = true
	h.srv.mu.Unlock()
	h.start(context.Background())
	h.send(t, `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	h.recv(t)
	h.srv.expire()
	a := h.recv(t)
	if a["id"] != nil || a["error"] == nil {
		t.Fatalf("answer = %v", a)
	}
	if code := h.wait(t); code != ExitError {
		t.Fatalf("exit = %d", code)
	}
}
