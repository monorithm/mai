//go:build !presencefake && !(darwin && cgo) && !linux && !windows

package presence

// This system has no presence check in this build.
func check(reason string) error { return ErrUnavailable }

func available() bool { return false }
