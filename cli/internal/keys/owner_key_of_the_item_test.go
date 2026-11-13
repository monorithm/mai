package keys

import (
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/keychain"
)

// The key file and the legacy item hold two different owner keys, and the server accepts only
// the key of the legacy item. The server thus proves that key. MoveAll must write it to the key
// file and delete the legacy item, with no step for the user.
func TestMoveAllWritesTheKeyOfTheItemWhenTheServerAcceptsOnlyThatKey(t *testing.T) {
	dir := t.TempDir()
	legacy := keychain.NewMemory()
	account := OwnerAccount("mai")
	legacy.Put(account, ownerKey)
	f := Open(dir, legacy)
	if err := f.Set(account, "mai1:owner:refused"); err != nil {
		t.Fatal(err)
	}
	r, err := f.MoveAll([]string{account}, func(_, key string) (bool, error) { return key == ownerKey, nil })
	if err != nil || len(r.Conflicts) != 0 || len(r.Stays) != 0 {
		t.Fatalf("MoveAll = %+v, %v", r, err)
	}
	if got, _ := f.inFile(account); got != ownerKey {
		t.Fatal("the key file does not have the key that the server accepts")
	}
	if len(legacy.Accounts()) != 0 {
		t.Fatalf("the legacy items %v stay", legacy.Accounts())
	}
}

// When a different process changes the key of the key file after the check of the server,
// nothing proves which key the server accepts. MoveAll then changes nothing.
func TestMoveAllKeepsAKeyThatADifferentProcessWroteAfterTheCheck(t *testing.T) {
	dir := t.TempDir()
	legacy := keychain.NewMemory()
	account := OwnerAccount("mai")
	legacy.Put(account, ownerKey)
	f := Open(dir, legacy)
	if err := f.Set(account, "mai1:owner:refused"); err != nil {
		t.Fatal(err)
	}
	r, err := f.MoveAll([]string{account}, func(_, key string) (bool, error) {
		if key == ownerKey {
			// A different process writes a new key during the check.
			if err := Open(dir, nil).Set(account, "mai1:owner:new"); err != nil {
				t.Fatal(err)
			}
		}
		return key == ownerKey, nil
	})
	if err != nil || len(r.Conflicts) != 1 || r.Conflicts[0].Checked {
		t.Fatalf("MoveAll = %+v, %v", r, err)
	}
	if got, _ := f.inFile(account); got != "mai1:owner:new" {
		t.Fatal("MoveAll replaced the key that a different process wrote")
	}
	if len(legacy.Accounts()) != 1 {
		t.Fatal("the legacy item is gone")
	}
}
