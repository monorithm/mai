//go:build linux

package procchain

import (
	"errors"
	"os"
	"strconv"
	"strings"
)

// lookup reads the parent from /proc/<pid>/stat and the executable from /proc/<pid>/exe.
func lookup(pid int) (Proc, error) {
	dir := "/proc/" + strconv.Itoa(pid)
	raw, err := os.ReadFile(dir + "/stat")
	if err != nil {
		return Proc{}, err
	}
	s := string(raw)
	// The name is in parentheses and can contain spaces and parentheses. The fields after
	// the last ")" are the state and then the parent PID.
	open, end := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
	if open < 0 || end < open {
		return Proc{}, errors.New("procchain: bad stat of process " + strconv.Itoa(pid))
	}
	fields := strings.Fields(s[end+1:])
	if len(fields) < 2 {
		return Proc{}, errors.New("procchain: bad stat of process " + strconv.Itoa(pid))
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return Proc{}, err
	}
	p := Proc{PID: pid, PPID: ppid}
	if exe, err := os.Readlink(dir + "/exe"); err == nil {
		p.Exe = strings.TrimSuffix(exe, " (deleted)")
	} else {
		p.Exe = s[open+1 : end]
	}
	return p, nil
}
