package keys

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/keychain"
)

// Peek is for a command that changes nothing. It makes no folder, no key file, and no lock
// file, and it does not read or move a legacy keychain item.
func TestPeekMakesNoFileAndReadsNoLegacyItem(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	legacy := keychain.NewMemory()
	legacy.Put(OwnerAccount("mai"), ownerKey)
	f := Open(dir, legacy)
	if _, err := f.Peek(OwnerAccount("mai")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("peek with no folder: %v, want ErrNotFound", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("peek made the folder %s: %v", dir, err)
	}
	if len(legacy.Accounts()) != 1 {
		t.Fatalf("peek changed the legacy keychain: %v", legacy.Accounts())
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Peek(OwnerAccount("mai")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("peek with an empty folder: %v, want ErrNotFound", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("peek made files in %s: %v %v", dir, entries, err)
	}
}

func TestPeekGivesTheKeyOfTheKeyFile(t *testing.T) {
	dir := t.TempDir()
	f := Open(dir, nil)
	if err := f.Set(AgentAccount("mai", "cursor"), agentKey); err != nil {
		t.Fatal(err)
	}
	got, err := f.Peek(AgentAccount("mai", "cursor"))
	if err != nil || got != agentKey {
		t.Fatalf("peek: equal = %v, %v", got == agentKey, err)
	}
	if _, err := f.Peek(AgentAccount("mai", "codex")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("peek of a different agent: %v, want ErrNotFound", err)
	}
	// With no lock file, Peek reads the key file with no lock and makes no lock file.
	if err := os.Remove(filepath.Join(dir, FileName+".lock")); err != nil {
		t.Fatal(err)
	}
	if got, err := f.Peek(AgentAccount("mai", "cursor")); err != nil || got != agentKey {
		t.Fatalf("peek with no lock file: equal = %v, %v", got == agentKey, err)
	}
	if _, err := os.Stat(filepath.Join(dir, FileName+".lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("peek made the lock file: %v", err)
	}
}
