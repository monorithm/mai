package bridge

import (
	"context"
	"strings"
	"testing"
	"time"
)

// The server ends the pool lease, and no pool session is free. A tool call with no session
// then gets a new pool lease with no session. The notice file must then have no pool entry,
// also before the next heartbeat.
func TestANewPoolLeaseWithNoSessionFromACallRemovesThePoolEntryOfTheNotice(t *testing.T) {
	h := newHarness(t, memoryWithKey())
	h.srv.hbSeconds = 60
	h.start(context.Background())
	h.login(t, 1, "lane-a")
	h.firstBeats(t, "cursor.s1", "cursor.lane-a")
	waitFor(t, "notice with the pool session cursor.s1 and lane-a", func() bool {
		n := readNotice(t, h)
		return n != nil && n["session"] == "cursor.s1" && len(namedInNotice(n)) == 1
	})

	h.srv.mu.Lock()
	h.srv.poolFull = true
	h.srv.mu.Unlock()
	h.srv.expireSession("cursor.s1")
	if e := toolErr(t, h.call(t, 2, "whoami", nil)); e["code"] != "NO_FREE_SESSION" {
		t.Fatalf("whoami with no session = %v", e)
	}
	if !strings.Contains(h.stderr.String(), "no free pool session") {
		t.Fatalf("the bridge did not get the pool lease with no session; stderr: %s", h.stderr.String())
	}
	deadline := time.Now().Add(5 * time.Second)
	var n map[string]any
	for time.Now().Before(deadline) {
		n = readNotice(t, h)
		_, pool := n["session"]
		names := namedInNotice(n)
		if n != nil && !pool && len(names) == 1 && names[0] == "cursor.lane-a" {
			h.stop(t)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("5 s after the pool lease with no session, the notice still has a pool entry: %v", n)
}

// The server ends the pool lease. A tool call with no session then gets a new pool lease on
// a different pool session. The notice file must have the new pool session, also before the
// next heartbeat.
func TestANewPoolLeaseFromACallPutsItsSessionInTheNotice(t *testing.T) {
	h := newHarness(t, memoryWithKey())
	h.srv.hbSeconds = 60
	h.start(context.Background())
	h.login(t, 1, "lane-a")
	h.firstBeats(t, "cursor.s1", "cursor.lane-a")
	waitFor(t, "notice with the pool session cursor.s1", func() bool {
		n := readNotice(t, h)
		return n != nil && n["session"] == "cursor.s1"
	})

	h.srv.expireSession("cursor.s1")
	r := result(t, h.call(t, 2, "whoami", nil))
	renewed, _ := r["session"].(string)
	if !strings.HasPrefix(renewed, "cursor.s") || renewed == "cursor.s1" {
		t.Fatalf("whoami after the end of the pool lease = %v", r)
	}
	waitFor(t, "notice with the pool session "+renewed, func() bool {
		n := readNotice(t, h)
		names := namedInNotice(n)
		return n != nil && n["session"] == renewed && len(names) == 1 && names[0] == "cursor.lane-a"
	})
	h.stop(t)
}
