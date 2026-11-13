package keys

import (
	"encoding/base64"
	"encoding/hex"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/keychain"
)

// mai 3.0 and 3.1 built with no cgo wrote the keychain items of macOS through go-keyring and
// /usr/bin/security. go-keyring keeps the secret as "go-keyring-base64:" and the base64 of the
// secret. The Security framework of the darwin build with cgo gives the data of the item as
// it is. The memory keychain of this test holds the same data.
func TestTheMoveOfAnItemThatGoKeyringWroteGivesTheKey(t *testing.T) {
	dir := t.TempDir()
	legacy := keychain.NewMemory()
	legacy.Put(OwnerAccount("mai"), "go-keyring-base64:"+base64.StdEncoding.EncodeToString([]byte(ownerKey)))
	f := Open(dir, legacy)
	got, err := f.Get(OwnerAccount("mai"))
	if err != nil {
		t.Fatal(err)
	}
	if got != ownerKey {
		t.Fatalf("the move wrote the encoded data of the item to the key file, not the key; the keychain items are %v",
			legacy.Accounts())
	}
}

func TestTheMoveDecodesEachFormOfGoKeyring(t *testing.T) {
	for name, data := range map[string]string{
		"base64": "go-keyring-base64:" + base64.StdEncoding.EncodeToString([]byte(agentKey)),
		"hex":    "go-keyring-encoded:" + hex.EncodeToString([]byte(agentKey)),
		"plain":  agentKey,
	} {
		dir := t.TempDir()
		legacy := keychain.NewMemory()
		account := AgentAccount("mai", "cursor")
		legacy.Put(account, data)
		f := Open(dir, legacy)
		r, err := f.MoveAll([]string{account}, nil)
		if err != nil || len(r.Moved) != 1 {
			t.Fatalf("%s: MoveAll = %+v, %v", name, r, err)
		}
		if got := readDoc(t, dir).Keys[account]; got != agentKey {
			t.Fatalf("%s: the key file does not have the decoded key", name)
		}
		if len(legacy.Accounts()) != 0 {
			t.Fatalf("%s: the keychain items are %v", name, legacy.Accounts())
		}
	}
}

func TestDataOfGoKeyringThatIsNotValidKeepsTheItem(t *testing.T) {
	dir := t.TempDir()
	legacy := keychain.NewMemory()
	legacy.Put(OwnerAccount("mai"), "go-keyring-base64:%%%")
	f := Open(dir, legacy)
	if _, err := f.Get(OwnerAccount("mai")); err == nil {
		t.Fatal("get of data that is not valid gave no error")
	}
	if len(legacy.Accounts()) != 1 {
		t.Fatalf("the keychain items are %v", legacy.Accounts())
	}
	if ok, err := f.Has(OwnerAccount("mai")); ok || err != nil {
		t.Fatalf("the key file has a key for the account: %v, %v", ok, err)
	}
}

func TestMoveAllComparesTheDecodedKeyWithTheKeyOfTheKeyFile(t *testing.T) {
	dir := t.TempDir()
	legacy := keychain.NewMemory()
	f := Open(dir, legacy)
	if err := f.Set(OwnerAccount("mai"), ownerKey); err != nil {
		t.Fatal(err)
	}
	legacy.Put(OwnerAccount("mai"), "go-keyring-base64:"+base64.StdEncoding.EncodeToString([]byte(ownerKey)))
	r, err := f.MoveAll([]string{OwnerAccount("mai")}, nil)
	if err != nil || len(r.Cleaned) != 1 || len(r.Conflicts) != 0 {
		t.Fatalf("MoveAll = %+v, %v; want the item with the same key cleaned", r, err)
	}
}
