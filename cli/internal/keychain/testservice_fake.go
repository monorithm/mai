//go:build presencefake

package keychain

import (
	"os"
	"strings"
)

// TestServiceVar is the variable that gives the test service of a test build.
const TestServiceVar = "MAI_TEST_KEYCHAIN_SERVICE"

// testServicePrefix is the start of each permitted test service. A different value has no
// effect, thus a test cannot move the items to a service of the user.
const testServicePrefix = Service + ".test."

// movedService gives the value of MAI_TEST_KEYCHAIN_SERVICE when it starts with
// "dev.mai.test." and has more text. Else it gives "". A release build never has this file,
// because the tag presencefake is only for tests.
func movedService() string {
	s := os.Getenv(TestServiceVar)
	if len(s) > len(testServicePrefix) && strings.HasPrefix(s, testServicePrefix) {
		return s
	}
	return ""
}
