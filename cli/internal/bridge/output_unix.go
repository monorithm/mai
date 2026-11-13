//go:build unix

package bridge

import (
	"os"

	"golang.org/x/sys/unix"
)

// OutputClosed tells if the read end of the pipe or of the socket of f is closed. Then no
// process can read what the bridge writes to f. For such a file, poll gives POLLHUP on macOS
// and POLLERR or POLLHUP on Linux. A file that is not open gives POLLNVAL. OutputClosed does
// not change the mode of f.
func OutputClosed(f *os.File) bool {
	rc, err := f.SyscallConn()
	if err != nil {
		return false
	}
	closed := false
	_ = rc.Control(func(fd uintptr) {
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLOUT}}
		if _, err := unix.Poll(fds, 0); err == nil {
			closed = fds[0].Revents&(unix.POLLHUP|unix.POLLERR|unix.POLLNVAL) != 0
		}
	})
	return closed
}
