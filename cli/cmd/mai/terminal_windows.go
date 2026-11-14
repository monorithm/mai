//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// terminal tells if f is a console: the call that reads the console mode succeeds.
func terminal(f *os.File) bool {
	var mode uint32
	return windows.GetConsoleMode(windows.Handle(f.Fd()), &mode) == nil
}
