//go:build linux && !presencefake

package presence

import (
	"os"
	"os/exec"
)

// systemPolkit returns the polkit check with the real OS calls.
func systemPolkit() polkit {
	return polkit{
		lookPath: exec.LookPath,
		run:      runExit,
		exists:   fileExists,
		subject:  selfSubject,
	}
}

// selfSubject identifies this process for pkcheck.
func selfSubject() string {
	stat, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		stat = nil
	}
	return processSubject(os.Getpid(), os.Getuid(), string(stat))
}

// check uses polkit with the action dev.mai.lease.
func check(reason string) error { return systemPolkit().check(reason) }

func available() bool { return systemPolkit().available() }
