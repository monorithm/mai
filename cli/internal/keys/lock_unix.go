//go:build darwin || linux || freebsd || netbsd || openbsd || dragonfly

package keys

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// tryLock takes the flock of the lock file and does not wait. It gives false when a
// different holder has the lock.
func tryLock(f *os.File) (bool, error) {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, unix.EWOULDBLOCK), errors.Is(err, unix.EINTR):
		return false, nil
	}
	return false, err
}

func unlock(f *os.File) error { return unix.Flock(int(f.Fd()), unix.LOCK_UN) }
