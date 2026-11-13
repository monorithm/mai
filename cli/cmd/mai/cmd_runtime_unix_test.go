//go:build !windows

package main

import (
	"syscall"
	"testing"
	"time"
)

// A terminal that closes sends SIGHUP to the bridge. The bridge must then end each lease,
// the same as for SIGINT and SIGTERM. The context of the bridge ends on each of them.
func TestTheBridgeContextEndsOnSIGHUPSIGINTAndSIGTERM(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM} {
		ctx, stop := stopContext()
		if err := syscall.Kill(syscall.Getpid(), sig); err != nil {
			stop()
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
			t.Errorf("the context did not end on %v", sig)
		}
		stop()
	}
}
