//go:build windows && !presencefake

package presence

// Windows Hello is not in this build. Thus Windows has no presence check.
func check(reason string) error { return ErrUnavailable }

func available() bool { return false }
