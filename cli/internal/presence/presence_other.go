//go:build !presencefake

package presence

func check(reason string) error { return ErrUnavailable }
