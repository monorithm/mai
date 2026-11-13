package bridge

import (
	"fmt"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/keys"
)

// A different process can hold the lock of the key file for a short time. A subsequent try
// can succeed, thus the tool error is BUSY with retry_safe true.
func TestALockTimeoutOfTheKeyFileGivesBusy(t *testing.T) {
	h := startNamed(t)
	h.b.Keys = failingKeys{err: fmt.Errorf("%w: a different process holds the lock", keys.ErrBusy)}
	for i, name := range []string{"login", "whoami"} {
		e := toolErr(t, h.call(t, i+1, name, map[string]any{"session": "lane-a"}))
		if e["code"] != "BUSY" || e["retry_safe"] != true || e["path"] != "session" {
			t.Errorf("%s: tool error = %v, want BUSY with the path session and retry_safe true", name, e)
		}
	}
	h.stop(t)
}
