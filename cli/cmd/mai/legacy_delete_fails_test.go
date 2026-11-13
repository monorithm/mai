package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/keychain"
	"github.com/monorithm/mai/cli/v3/internal/keys"
	"github.com/monorithm/mai/cli/v3/internal/manifest"
	"github.com/monorithm/mai/cli/v3/internal/paths"
)

// refuseDelete is a legacy keychain whose read is successful and whose delete fails, as when
// the keychain refuses the delete of an item.
type refuseDelete struct{ *keychain.Memory }

func (refuseDelete) Delete(string) error { return errors.New("the keychain refused the delete") }

func TestUpgradeFailsWhenTheItemOfAMovedKeyStays(t *testing.T) {
	t.Setenv("MAI_HOME", t.TempDir())
	var out, errs bytes.Buffer
	oldOut, oldErr := lcOut, lcErr
	lcOut, lcErr = &out, &errs
	t.Cleanup(func() { lcOut, lcErr = oldOut, oldErr })
	legacy := keychain.NewMemory()
	legacy.Put(keys.OwnerAccount("mai"), "mai1:owner:legacy")
	legacy.Put(keys.AgentAccount("mai", "cursor"), "mai1:cursor:legacy")
	dirs, _ := paths.Default()
	old := openKeys
	openKeys = func() (*keys.File, error) { return keys.Open(dirs.State, refuseDelete{legacy}), nil }
	t.Cleanup(func() { openKeys = old })
	m := testManifest(t,
		manifest.Item{Type: manifest.KeychainKey, Name: keys.OwnerAccount("mai")},
		manifest.Item{Type: manifest.KeychainKey, Name: keys.AgentAccount("mai", "cursor")},
	)
	ok := lcMoveKeys("mai", m)
	stay := legacy.Accounts()
	if len(stay) != 2 {
		t.Fatalf("the keychain items are %v", stay)
	}
	if ok {
		t.Fatalf("the move of mai upgrade gave true, but the keychain items %v stay.\nstdout: %s\nstderr: %s",
			stay, out.String(), errs.String())
	}
	for _, a := range stay {
		if !strings.Contains(errs.String(), a) {
			t.Fatalf("stderr does not tell about the keychain item %s that stays: %q", a, errs.String())
		}
	}
}

func TestUpgradeFailsWhenAKeychainItemStaysAfterTheMove(t *testing.T) {
	t.Setenv("MAI_HOME", t.TempDir())
	var out, errs bytes.Buffer
	oldOut, oldErr := lcOut, lcErr
	lcOut, lcErr = &out, &errs
	t.Cleanup(func() { lcOut, lcErr = oldOut, oldErr })
	// An item with no data holds no key, thus no key moves, and the item stays.
	legacy := keychain.NewMemory()
	legacy.Put(keys.OwnerAccount("mai"), "")
	dirs, _ := paths.Default()
	old := openKeys
	openKeys = func() (*keys.File, error) { return keys.Open(dirs.State, legacy), nil }
	t.Cleanup(func() { openKeys = old })
	m := testManifest(t, manifest.Item{Type: manifest.KeychainKey, Name: keys.OwnerAccount("mai")})
	if lcMoveKeys("mai", m) {
		t.Fatalf("the move gave true, but the keychain items %v stay", legacy.Accounts())
	}
	if !strings.Contains(errs.String(), "owner@mai stays") || !strings.Contains(errs.String(), "Step:") {
		t.Fatalf("stderr does not tell about the keychain item that stays: %q", errs.String())
	}
	if len(m.Of(manifest.KeychainKey)) != 1 {
		t.Fatalf("the manifest does not keep the record of the item: %+v", m.Of(manifest.KeychainKey))
	}
}
