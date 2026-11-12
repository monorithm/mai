//go:build windows

package procchain

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

// lookup finds the parent in a snapshot of the processes (CreateToolhelp32Snapshot) and the
// executable path with QueryFullProcessImageName.
func lookup(pid int) (Proc, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return Proc{}, err
	}
	defer windows.CloseHandle(snap)
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		if int(e.ProcessID) != pid {
			continue
		}
		p := Proc{PID: pid, PPID: int(e.ParentProcessID), Exe: windows.UTF16ToString(e.ExeFile[:])}
		if full := imagePath(uint32(pid)); full != "" {
			p.Exe = full
		}
		return p, nil
	}
	return Proc{}, errors.New("procchain: no process with this PID")
}

func imagePath(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}
