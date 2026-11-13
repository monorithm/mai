package uninstall

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/keychain"
	"github.com/monorithm/mai/cli/v3/internal/keys"
	"github.com/monorithm/mai/cli/v3/internal/manifest"
)

func names(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

func TestUninstallKeepsTheKeysOfADifferentInstance(t *testing.T) {
	e := setup(t)
	if err := e.keys.Set(keys.OwnerAccount("mai-dev"), "mai1:owner:dev"); err != nil {
		t.Fatal(err)
	}
	e.kc.Put(keys.OwnerAccount("mai-dev"), "mai1:owner:devold")
	var out bytes.Buffer
	if code := e.uninstaller(&out).Run(context.Background()); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
	all, err := e.keys.Accounts()
	if err != nil || strings.Join(all, ",") != "owner@mai-dev" {
		t.Fatalf("the key file holds %v, %v", all, err)
	}
	if got := names(t, e.dirs.State); got != "keys.json,keys.json.lock" {
		t.Fatalf("the state folder holds %s", got)
	}
	for _, d := range []string{e.dirs.Config, e.dirs.Cache} {
		if _, err := os.Stat(d); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s exists", d)
		}
	}
	if strings.Join(e.kc.Accounts(), ",") != "owner@mai-dev" {
		t.Fatalf("the legacy items are %v", e.kc.Accounts())
	}
	text := out.String()
	for _, want := range []string{"kept     folder " + e.dirs.State, "it holds the keys of the instances: mai-dev", "gone     key owner@mai"} {
		if !strings.Contains(text, want) {
			t.Errorf("the output has no %q:\n%s", want, text)
		}
	}
}

// mai 3.0 and 3.1 did not operate with no keychain. Thus with no record in the manifest, a
// keychain that is not available holds no item of mai.
func TestUninstallWithNoKeychainRemovesTheKeysOfTheKeyFile(t *testing.T) {
	e := setup(t)
	m, err := manifest.Load(filepath.Join(e.dirs.State, manifest.FileName), "mai")
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range m.Of(manifest.KeychainKey) {
		if err := m.Remove(it); err != nil {
			t.Fatal(err)
		}
	}
	e.kc.Err = keychain.ErrUnavailable
	var out bytes.Buffer
	if code := e.uninstaller(&out).Run(context.Background()); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
	if strings.Contains(out.String(), "keychain") {
		t.Errorf("the output tells about a keychain that is not available:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "gone     key owner@mai") {
		t.Errorf("output:\n%s", out.String())
	}
	if _, err := os.Stat(e.keys.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Error("the key file exists")
	}
}

func TestUninstallTellsAboutAKeychainError(t *testing.T) {
	e := setup(t)
	e.kc.Err = errors.New("the keychain is locked")
	var out bytes.Buffer
	if code := e.uninstaller(&out).Run(context.Background()); code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, out.String())
	}
	for _, want := range []string{
		"keychain   dev.mai: not examined: the keychain is locked",
		"keychain   dev.mai / owner@mai (if the item exists)",
		"failure  keychain dev.mai / owner@mai: check failed: the keychain is locked",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the output has no %q:\n%s", want, out.String())
		}
	}
}

// hasFails is a legacy keychain whose examination is not successful. Its read and its delete
// operate.
type hasFails struct{ *keychain.Memory }

func (hasFails) Has(string) (bool, error) { return false, errors.New("the keychain is locked") }

// When the examination of the keychain is not successful, each account of the instance can
// have a legacy item. The uninstall deletes the item of each account. It cannot make sure that the items
// are gone, thus it gives a failure and the exit code 1.
func TestAFailedExaminationOfTheKeychainRemovesEachLegacyItem(t *testing.T) {
	e := setup(t)
	e.kc.Put(keys.OwnerAccount("mai-dev"), "mai1:owner:dev")
	e.keys = keys.Open(e.dirs.State, hasFails{e.kc})
	var out bytes.Buffer
	if code := e.uninstaller(&out).Run(context.Background()); code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, out.String())
	}
	if got := strings.Join(e.kc.Accounts(), ","); got != "owner@mai-dev" {
		t.Fatalf("the legacy items after the uninstall are %s, want only the item of mai-dev", got)
	}
	text := out.String()
	for _, want := range []string{
		"failure  keychain dev.mai / agent:cursor@mai: check failed: the keychain is locked",
		"failure  keychain dev.mai / owner@mai: check failed: the keychain is locked",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the output has no %q:\n%s", want, text)
		}
	}
}

func TestRemoveExceptKeepsOnlyThePathsAndTheirFolders(t *testing.T) {
	// The folders of macOS: the state folder is in the configuration folder.
	root := filepath.Join(t.TempDir(), "mai")
	state := filepath.Join(root, "state")
	for _, d := range []string{filepath.Join(state, "notices"), filepath.Join(root, "other")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{filepath.Join(root, "config.json"), filepath.Join(state, "keys.json"),
		filepath.Join(state, "keys.json.lock"), filepath.Join(state, "manifest.json"), filepath.Join(state, "notices", "1-a.json")} {
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	keep := []string{filepath.Join(state, "keys.json"), filepath.Join(state, "keys.json.lock")}
	held, err := removeExcept(root, keep)
	if err != nil || !held {
		t.Fatalf("removeExcept = %v, %v", held, err)
	}
	if got := names(t, root); got != "state" {
		t.Fatalf("the folder holds %s", got)
	}
	if got := names(t, state); got != "keys.json,keys.json.lock" {
		t.Fatalf("the state folder holds %s", got)
	}
	other := filepath.Join(t.TempDir(), "cache")
	_ = os.MkdirAll(other, 0o700)
	if held, err := removeExcept(other, keep); err != nil || held {
		t.Fatalf("removeExcept of a different folder = %v, %v", held, err)
	}
	if _, err := os.Stat(other); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the different folder exists")
	}
}
