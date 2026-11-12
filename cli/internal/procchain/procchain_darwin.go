//go:build darwin

package procchain

import (
	"bytes"

	"golang.org/x/sys/unix"
)

// lookup reads the parent with sysctl kern.proc.pid and the executable path with
// kern.procargs2. No cgo is necessary.
func lookup(pid int) (Proc, error) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return Proc{}, err
	}
	if int(kp.Proc.P_pid) != pid {
		return Proc{}, unix.ESRCH
	}
	p := Proc{PID: pid, PPID: int(kp.Eproc.Ppid), Exe: exePath(pid)}
	if p.Exe == "" {
		p.Exe = unix.ByteSliceToString(kp.Proc.P_comm[:])
	}
	return p, nil
}

// exePath reads kern.procargs2: an int32 argc, then the executable path that ends with NUL.
// For a process of a different user the call fails, and then the path is empty.
func exePath(pid int) string {
	buf, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(buf) < 5 {
		return ""
	}
	rest := buf[4:]
	if i := bytes.IndexByte(rest, 0); i >= 0 {
		rest = rest[:i]
	}
	return string(rest)
}
