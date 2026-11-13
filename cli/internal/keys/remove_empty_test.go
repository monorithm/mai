package keys

import (
	"errors"
	"os"
	"testing"
)

// RemoveEmpty removes the key file and its lock file only when the file holds no key. A key
// file with a key stays with each of its keys.
func TestRemoveEmptyRemovesOnlyAKeyFileWithNoKey(t *testing.T) {
	f := Open(t.TempDir(), nil)
	if err := f.Set(AgentAccount("mai-dev", "cursor"), agentKey); err != nil {
		t.Fatal(err)
	}
	rest, err := f.RemoveEmpty()
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 1 || rest[0] != AgentAccount("mai-dev", "cursor") {
		t.Fatalf("RemoveEmpty gave %v", rest)
	}
	if key, err := f.Get(AgentAccount("mai-dev", "cursor")); err != nil || key != agentKey {
		t.Fatalf("the key of the key file that stays: %v", err)
	}
	if err := f.Delete(AgentAccount("mai-dev", "cursor")); err != nil {
		t.Fatal(err)
	}
	if rest, err := f.RemoveEmpty(); err != nil || len(rest) != 0 {
		t.Fatalf("RemoveEmpty of an empty key file gave %v, %v", rest, err)
	}
	for _, p := range []string{f.Path(), f.lockPath()} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s exists after RemoveEmpty: %v", p, err)
		}
	}
}
