//go:build presencefake

package bridge

import (
	"context"
	"strings"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/presence"
)

func TestBridgeAsksForNoLeaseWhenThePresenceCheckFails(t *testing.T) {
	t.Setenv("MAI_TEST_PRESENCE", "refuse")
	h := newHarness(t, memoryWithKey())
	h.b.Presence = presence.Check
	h.start(context.Background())
	if code := h.wait(t); code != ExitError {
		t.Fatalf("exit = %d", code)
	}
	if len(h.srv.leaseReqs) != 0 {
		t.Fatal("the bridge asked for a lease after a refused check")
	}
	if !strings.Contains(h.stderr.String(), "presence") {
		t.Fatalf("stderr = %q", h.stderr.String())
	}
}

func TestBridgeRunsWithTheFakePresenceCheck(t *testing.T) {
	t.Setenv("MAI_TEST_PRESENCE", "")
	h := newHarness(t, memoryWithKey())
	h.b.Presence = presence.Check
	h.start(context.Background())
	h.send(t, `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	if a := h.recv(t); a["id"] != float64(1) {
		t.Fatalf("answer = %v", a)
	}
	_ = h.in.Close()
	if code := h.wait(t); code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
}
