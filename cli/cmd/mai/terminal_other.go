//go:build !darwin && !freebsd && !netbsd && !openbsd && !dragonfly && !linux && !windows

package main

import "os"

// terminal tells if f is a character device. On this OS, the CLI has no better test.
func terminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
