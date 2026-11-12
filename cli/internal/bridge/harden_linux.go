//go:build linux

package bridge

import "golang.org/x/sys/unix"

// harden sets PR_SET_DUMPABLE to 0. Then a program of the same user cannot use ptrace on the
// bridge or read /proc/<pid>/mem.
func harden() error { return unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0) }
