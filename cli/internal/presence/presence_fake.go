//go:build presencefake

package presence

import "os"

// The test build agrees unless MAI_TEST_PRESENCE is "refuse". A release build never has
// this file, because the tag presencefake is only for tests.
func check(reason string) error {
	if os.Getenv("MAI_TEST_PRESENCE") == "refuse" {
		return ErrRefused
	}
	return nil
}
