//go:build presencefake

package presence

import "os"

// The test build agrees unless MAI_TEST_PRESENCE is "refuse". A release build never has
// this file, because the tag presencefake is only for tests. The value "unavailable"
// makes the test build act as a system with no presence check.
func check(reason string) error {
	switch os.Getenv("MAI_TEST_PRESENCE") {
	case "refuse":
		return ErrRefused
	case "unavailable":
		return ErrUnavailable
	}
	return nil
}

func available() bool { return os.Getenv("MAI_TEST_PRESENCE") != "unavailable" }
