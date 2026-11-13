package uninstall

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/keychain"
	"github.com/monorithm/mai/cli/v3/internal/keys"
	"github.com/monorithm/mai/cli/v3/internal/manifest"
)

// downStore is a legacy keychain whose items exist, but that is not available while down is
// true, as the Secret Service of a Linux host in a shell with no session bus.
type downStore struct {
	*keychain.Memory
	down atomic.Bool
}

func (s *downStore) fail() error { return fmt.Errorf("%w: no session bus", keychain.ErrUnavailable) }

func (s *downStore) Get(a string) (string, error) {
	if s.down.Load() {
		return "", s.fail()
	}
	return s.Memory.Get(a)
}

func (s *downStore) Has(a string) (bool, error) {
	if s.down.Load() {
		return false, s.fail()
	}
	return s.Memory.Has(a)
}

func (s *downStore) Delete(a string) error {
	if s.down.Load() {
		return s.fail()
	}
	return s.Memory.Delete(a)
}

// The manifest of a 3.1 install records each keychain item that mai wrote. When the keychain
// is not available during the uninstall, these items can exist. The uninstall then must not
// tell success with no word about them: the key of cursor stays in the keychain, and the
// uninstall deletes the manifest, the only record of the item.
func TestAnUninstallWithAKeychainThatIsNotAvailableTellsAboutTheRecordedItems(t *testing.T) {
	e := setup(t)
	ds := &downStore{Memory: e.kc}
	ds.down.Store(true)
	e.keys = keys.Open(e.dirs.State, ds)
	var out bytes.Buffer
	code := e.uninstaller(&out).Run(context.Background())
	ds.down.Store(false)
	left := e.kc.Accounts()
	if len(left) == 0 {
		t.Fatal("the test needs a legacy item that stays")
	}
	text := out.String()
	if code == 0 && !strings.Contains(text, "keychain") {
		t.Errorf("exit 0 and no word about the keychain, but the items %v of the instance stay in the keychain:\n%s",
			left, text)
	}
}

// Each recorded item that stays is a failure with the cause and the step. The manifest stays
// with the records of these items, and the CLI stays. Thus a subsequent uninstall, with the
// keychain available, removes the items.
func TestAnUninstallKeepsTheRecordOfTheKeychainItemsThatStay(t *testing.T) {
	e := setup(t)
	ds := &downStore{Memory: e.kc}
	ds.down.Store(true)
	e.keys = keys.Open(e.dirs.State, ds)
	var out bytes.Buffer
	if code := e.uninstaller(&out).Run(context.Background()); code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, out.String())
	}
	text := out.String()
	for _, want := range []string{
		"keychain   dev.mai / agent:cursor@mai (the manifest records it, and the keychain is not available)",
		"failure  keychain dev.mai / agent:cursor@mai: the keychain is not available",
		"failure  keychain dev.mai / owner@mai: the keychain is not available",
		"Make the keychain available",
		"run mai uninstall again",
		"gone     key owner@mai",
		"kept     binary ",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the output has no %q:\n%s", want, text)
		}
	}
	m, err := manifest.Load(filepath.Join(e.dirs.State, manifest.FileName), "mai")
	if err != nil {
		t.Fatal(err)
	}
	var recorded []string
	for _, it := range m.Items {
		recorded = append(recorded, it.Type+" "+it.Name)
	}
	if strings.Join(recorded, ",") != "keychain owner@mai,keychain agent:cursor@mai" {
		t.Fatalf("the manifest after the uninstall records %v", recorded)
	}
	if _, err := os.Stat(e.keys.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Error("the key file exists")
	}
	if _, err := os.Stat(e.bin); err != nil {
		t.Errorf("the binary is gone: %v", err)
	}

	ds.down.Store(false)
	out.Reset()
	if code := e.uninstaller(&out).Run(context.Background()); code != 0 {
		t.Fatalf("the second uninstall: exit %d:\n%s", code, out.String())
	}
	if left := e.kc.Accounts(); len(left) != 0 {
		t.Fatalf("after the second uninstall, the keychain items %v stay", left)
	}
	for _, p := range []string{e.dirs.State, e.bin} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s exists after the second uninstall", p)
		}
	}
}

// For a different instance, the step of a recorded item gives the instance, because the
// configuration goes.
func TestTheStepOfAKeychainItemThatStaysGivesTheInstance(t *testing.T) {
	e := setup(t)
	ds := &downStore{Memory: e.kc}
	ds.down.Store(true)
	e.keys = keys.Open(e.dirs.State, ds)
	m, err := manifest.Load(filepath.Join(e.dirs.State, manifest.FileName), "mai")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Add(manifest.Item{Type: manifest.KeychainKey, Name: keys.OwnerAccount("mai-dev")}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	u := e.uninstaller(&out)
	u.Instance = "mai-dev"
	if code := u.Run(context.Background()); code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "failure  keychain dev.mai / owner@mai-dev: the keychain is not available") ||
		!strings.Contains(out.String(), "run mai uninstall --instance mai-dev again") {
		t.Fatalf("output:\n%s", out.String())
	}
}
