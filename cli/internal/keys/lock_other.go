//go:build !windows && !darwin && !linux && !freebsd && !netbsd && !openbsd && !dragonfly

package keys

import (
	"errors"
	"os"
)

// tryLock fails: this system has no file lock that the CLI knows.
func tryLock(*os.File) (bool, error) {
	return false, errors.New("this system has no file lock for the key file")
}

func unlock(*os.File) error { return nil }
