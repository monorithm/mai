package bridge

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
)

// When the app exits, it closes its read end of the standard output of the bridge. Each
// subsequent write of an answer fails, also when the standard input stays open. The bridge
// must then stop, end each of its leases, and remove its notice file.
func TestTheBridgeStopsAndEndsEachLeaseWhenAWriteToTheAppFails(t *testing.T) {
	h := newHarness(t, memoryWithKey())
	h.srv.hbSeconds = 60
	h.start(context.Background())
	if r := result(t, h.call(t, 1, "whoami", map[string]any{"session": "lane-a"})); r["session"] != "cursor.lane-a" {
		t.Fatalf("whoami with the label lane-a = %v", r)
	}
	h.firstBeats(t, "cursor.s1", "cursor.lane-a")
	waitForFile(t, noticePath(h))

	_ = h.b.Stdout.(*io.PipeWriter).CloseWithError(errors.New("test: the app closed its read end"))
	h.send(t, `{"jsonrpc":"2.0","id":2,"method":"ping"}`)
	if code := h.wait(t); code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	h.srv.mu.Lock()
	left := len(h.srv.valid)
	h.srv.mu.Unlock()
	if left != 0 {
		t.Errorf("after the stop of the bridge, the server has %d lease(s) of the bridge", left)
	}
	if _, err := os.Stat(noticePath(h)); err == nil {
		t.Error("the notice file stays after the stop of the bridge")
	}
}
