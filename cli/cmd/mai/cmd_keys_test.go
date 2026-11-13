package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/config"
	"github.com/monorithm/mai/cli/v3/internal/keychain"
	"github.com/monorithm/mai/cli/v3/internal/keys"
	"github.com/monorithm/mai/cli/v3/internal/manifest"
	"github.com/monorithm/mai/cli/v3/internal/paths"
)

// TestMain gives the tests a scratch MAI_HOME. Thus no test reads or writes the key file of
// the user, and the key file of a test has no legacy keychain unless the test gives one. A
// test can set its own MAI_HOME.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "mai-cmd-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_ = os.Setenv("MAI_HOME", home)
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}

// doctorRun runs the key checks of doctor and gives the lines of the result.
func doctorRun(t *testing.T) (ok, bad []string) {
	t.Helper()
	dirs, err := paths.Default()
	if err != nil {
		t.Fatal(err)
	}
	s := &lcSession{dirs: dirs, cfg: config.Defaults()}
	lcDoctorKeys(s,
		func(k, v string) { ok = append(ok, k+": "+v) },
		func(k, v, step string) { bad = append(bad, k+": "+v+" | "+step) })
	return ok, bad
}

func testManifest(t *testing.T, items ...manifest.Item) *manifest.Manifest {
	t.Helper()
	dirs, _ := paths.Default()
	m, err := manifest.Load(filepath.Join(dirs.State, manifest.FileName), "mai")
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if err := m.Add(it); err != nil {
			t.Fatal(err)
		}
	}
	return m
}

func TestTheCommandsUseTheKeyFileOfMAIHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MAI_HOME", home)
	kf, err := openKeys()
	if err != nil {
		t.Fatal(err)
	}
	if kf.HasLegacy() || kf.Path() != filepath.Join(home, "state", "keys.json") {
		t.Fatalf("the key file is %s, legacy %v", kf.Path(), kf.HasLegacy())
	}
	ok, bad := doctorRun(t)
	if len(bad) != 0 || !strings.Contains(strings.Join(ok, "\n"), "legacy: MAI_HOME is set") {
		t.Fatalf("doctor: ok %v, bad %v", ok, bad)
	}
}

func TestDoctorTellsAboutTheLegacyItemsOfTheInstance(t *testing.T) {
	t.Setenv("MAI_HOME", t.TempDir())
	k := useTestKeys(t)
	k.legacy.Put(keys.OwnerAccount("mai"), "mai1:owner:legacy")
	k.legacy.Put(keys.AgentAccount("mai", "cursor"), "mai1:cursor:legacy")
	k.legacy.Put(keys.OwnerAccount("mai-dev"), "mai1:owner:dev")
	testManifest(t, manifest.Item{Type: manifest.KeychainKey, Name: keys.AgentAccount("mai", "cursor")})
	ok, bad := doctorRun(t)
	if len(bad) != 1 || !strings.Contains(bad[0], "2 key(s) of the instance mai") ||
		!strings.Contains(bad[0], "agent:cursor@mai, owner@mai") || strings.Contains(bad[0], "mai-dev") ||
		!strings.Contains(bad[0], "mai upgrade") {
		t.Fatalf("doctor: bad %v", bad)
	}
	if !strings.Contains(strings.Join(ok, "\n"), "keys: "+k.file.Path()+" holds 0 key(s) of the instance mai") {
		t.Fatalf("doctor: ok %v", ok)
	}
	if strings.Contains(strings.Join(append(ok, bad...), "\n"), "mai1:") {
		t.Fatal("doctor shows a key")
	}
	if len(k.legacy.Accounts()) != 3 {
		t.Fatal("doctor changed the keychain")
	}
}

func TestDoctorWithNoKeychainIsOK(t *testing.T) {
	t.Setenv("MAI_HOME", t.TempDir())
	k := useTestKeys(t)
	k.legacy.Err = keychain.ErrUnavailable
	ok, bad := doctorRun(t)
	if len(bad) != 0 || !strings.Contains(strings.Join(ok, "\n"), "legacy: no keychain is available") {
		t.Fatalf("doctor: ok %v, bad %v", ok, bad)
	}
	k.legacy.Err = errors.New("the keychain is locked")
	if _, bad := doctorRun(t); len(bad) != 1 || !strings.Contains(bad[0], "the keychain is locked") {
		t.Fatalf("doctor with a locked keychain: bad %v", bad)
	}
}

func TestDoctorRefusesAKeyFileThatOthersCanRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no Unix mode")
	}
	t.Setenv("MAI_HOME", t.TempDir())
	k := useTestKeys(t)
	if err := k.file.Set(keys.OwnerAccount("mai"), "mai1:owner:x"); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(k.file.Path(), 0o640)
	_, bad := doctorRun(t)
	if len(bad) != 1 || !strings.Contains(bad[0], "keys: ") || !strings.Contains(bad[0], "run: chmod 600") {
		t.Fatalf("doctor: bad %v", bad)
	}
}

func TestUpgradeMovesEachKeyOfTheInstance(t *testing.T) {
	t.Setenv("MAI_HOME", t.TempDir())
	var out, errs bytes.Buffer
	oldOut, oldErr := lcOut, lcErr
	lcOut, lcErr = &out, &errs
	t.Cleanup(func() { lcOut, lcErr = oldOut, oldErr })
	k := useTestKeys(t)
	k.legacy.Put(keys.OwnerAccount("mai"), "mai1:owner:legacy")
	k.legacy.Put(keys.AgentAccount("mai", "cursor"), "mai1:cursor:legacy")
	k.legacy.Put(keys.AgentAccount("mai", "codex"), "mai1:codex:same")
	k.legacy.Put(keys.OwnerAccount("mai-dev"), "mai1:owner:dev")
	if err := k.file.Set(keys.AgentAccount("mai", "codex"), "mai1:codex:same"); err != nil {
		t.Fatal(err)
	}
	m := testManifest(t,
		manifest.Item{Type: manifest.KeychainKey, Name: keys.OwnerAccount("mai")},
		manifest.Item{Type: manifest.KeychainKey, Name: keys.AgentAccount("mai", "cursor")},
		manifest.Item{Type: manifest.KeychainKey, Name: keys.AgentAccount("mai", "codex")},
		manifest.Item{Type: manifest.KeychainKey, Name: keys.OwnerAccount("mai-dev")},
	)
	if !lcMoveKeys("mai", m) {
		t.Fatalf("the move failed: %s", errs.String())
	}
	for account, want := range map[string]string{
		keys.OwnerAccount("mai"):           "mai1:owner:legacy",
		keys.AgentAccount("mai", "cursor"): "mai1:cursor:legacy",
		keys.AgentAccount("mai", "codex"):  "mai1:codex:same",
	} {
		k.file = keys.Open(filepath.Dir(k.file.Path()), nil)
		if got, err := k.file.Get(account); err != nil || got != want {
			t.Errorf("the key file has a different key for %s: %v", account, err)
		}
	}
	if strings.Join(k.legacy.Accounts(), ",") != "owner@mai-dev" {
		t.Fatalf("the legacy items are %v", k.legacy.Accounts())
	}
	if items := m.Of(manifest.KeychainKey); len(items) != 1 || items[0].Name != "owner@mai-dev" {
		t.Fatalf("the manifest records %+v", items)
	}
	text := out.String()
	if !strings.Contains(text, "moved 2 key(s)") || !strings.Contains(text, "removed 1 old keychain item(s)") || strings.Contains(text, "mai1:") {
		t.Fatalf("output:\n%s", text)
	}
}

// refuseRead is a legacy keychain whose read fails, as when the user refuses the prompt.
type refuseRead struct{ *keychain.Memory }

func (refuseRead) Get(string) (string, error) { return "", errors.New("the user refused the prompt") }

func TestUpgradeTellsWhenAKeyDoesNotMove(t *testing.T) {
	t.Setenv("MAI_HOME", t.TempDir())
	var out, errs bytes.Buffer
	oldOut, oldErr := lcOut, lcErr
	lcOut, lcErr = &out, &errs
	t.Cleanup(func() { lcOut, lcErr = oldOut, oldErr })
	legacy := keychain.NewMemory()
	legacy.Put(keys.OwnerAccount("mai"), "mai1:owner:legacy")
	dirs, _ := paths.Default()
	old := openKeys
	openKeys = func() (*keys.File, error) { return keys.Open(dirs.State, refuseRead{legacy}), nil }
	t.Cleanup(func() { openKeys = old })
	m := testManifest(t, manifest.Item{Type: manifest.KeychainKey, Name: keys.OwnerAccount("mai")})
	if lcMoveKeys("mai", m) {
		t.Fatal("the move gave true")
	}
	if !strings.Contains(errs.String(), "Run mai upgrade again") || len(m.Of(manifest.KeychainKey)) != 1 {
		t.Fatalf("stderr %q, records %+v", errs.String(), m.Of(manifest.KeychainKey))
	}
	if ok, _ := legacy.Has(keys.OwnerAccount("mai")); !ok {
		t.Fatal("the legacy item is gone")
	}
}

func TestTheOwnerKeyMovesAtTheFirstUse(t *testing.T) {
	t.Setenv("MAI_HOME", t.TempDir())
	k := useTestKeys(t)
	k.legacy.Put(keys.OwnerAccount("mai"), "mai1:owner:legacy")
	if got := lcOwnerKey("mai"); got != "mai1:owner:legacy" {
		t.Fatal("lcOwnerKey gave a different key")
	}
	if len(k.legacy.Accounts()) != 0 {
		t.Fatal("the legacy item stays after the move")
	}
	if got := lcOwnerKey("other"); got != "" {
		t.Fatal("lcOwnerKey gave a key for an instance with no key")
	}
}
