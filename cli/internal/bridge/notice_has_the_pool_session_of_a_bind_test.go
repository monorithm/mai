package bridge

import (
	"context"
	"strings"
	"testing"
	"time"
)

// The guide of the bridge tells that the notice file always has the pool session of the
// current pool lease. A tools/call with no session binds a free pool session to a pool lease
// with no session. The notice file must then have that pool session, also before the next
// heartbeat. Else mai hook does not show the new mail of that session until the next
// heartbeat.
func TestTheNoticeHasThePoolSessionOfABindBeforeTheNextHeartbeat(t *testing.T) {
	h := newHarness(t, memoryWithKey())
	h.srv.hbSeconds = 60
	h.srv.poolFull = true
	h.start(context.Background())
	h.login(t, 1, "lane-a")
	h.firstBeats(t, "", "cursor.lane-a")
	h.srv.mu.Lock()
	h.srv.poolFull = false
	h.srv.mu.Unlock()
	r := result(t, h.call(t, 2, "whoami", nil))
	bound, _ := r["session"].(string)
	if !strings.HasPrefix(bound, "cursor.s") {
		t.Fatalf("whoami after the bind = %v", r)
	}
	deadline := time.Now().Add(5 * time.Second)
	var n map[string]any
	for time.Now().Before(deadline) {
		n = readNotice(t, h)
		if n != nil && n["session"] == bound {
			h.stop(t)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("5 s after the bind of %s, the notice has no pool entry for it: %v", bound, n)
}
