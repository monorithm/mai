// Package procchain reads the chain of parent processes of a process: the process, its
// parent, the parent of the parent, and so on to the first process of the OS.
//
// The chain is information only. A program can start a process with the same chain as an
// app, thus the chain is not a proof of the caller.
package procchain

import (
	"os"
	"path/filepath"
	"strings"
)

// Proc is one process of a chain.
type Proc struct {
	PID  int
	PPID int
	Exe  string
}

// Name is the base name of the executable, with no ".exe" suffix.
func (p Proc) Name() string {
	n := filepath.Base(strings.ReplaceAll(p.Exe, `\`, "/"))
	if n == "." || n == "/" {
		return ""
	}
	return strings.TrimSuffix(n, ".exe")
}

// maxDepth stops a walk through a bad table with a loop.
const maxDepth = 64

// Of returns the chain of pid, from pid to the first process. The first item is pid.
func Of(pid int) ([]Proc, error) {
	var chain []Proc
	seen := map[int]bool{}
	for pid > 0 && !seen[pid] && len(chain) < maxDepth {
		seen[pid] = true
		p, err := lookup(pid)
		if err != nil {
			if len(chain) == 0 {
				return nil, err
			}
			break
		}
		chain = append(chain, p)
		pid = p.PPID
	}
	return chain, nil
}

// Self returns the chain of this process.
func Self() ([]Proc, error) { return Of(os.Getpid()) }

// Top returns the app at the top of the chain: the last process that is not a root process
// of the OS (PID 0, PID 1, launchd, init, or the System process of Windows).
func Top(chain []Proc) (Proc, bool) {
	for i := len(chain) - 1; i >= 0; i-- {
		if chain[i].PID > 1 && !isRoot(chain[i]) {
			return chain[i], true
		}
	}
	return Proc{}, false
}

func isRoot(p Proc) bool {
	switch strings.ToLower(p.Name()) {
	case "system", "launchd", "init", "systemd", "wininit", "services", "smss", "kernel_task":
		return true
	}
	return false
}
