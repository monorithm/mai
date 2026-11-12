// Package presence asks the OS for a proof that the user is at the computer: Touch ID or the
// password on macOS, Windows Hello on Windows, polkit on Linux.
package presence

import "errors"

// ErrRefused means that the user refused or cancelled the prompt.
var ErrRefused = errors.New("presence: the user refused the prompt")

// ErrUnavailable means that this OS or this build has no presence check.
var ErrUnavailable = errors.New("presence: no presence check on this system")

// Check shows the prompt with the reason and returns nil only when the user agrees.
func Check(reason string) error { return check(reason) }
