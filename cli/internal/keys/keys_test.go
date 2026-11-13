package keys

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/keychain"
	"github.com/monorithm/mai/cli/v3/internal/manifest"
)

const (
	ownerKey = "mai1:owner:testownerkeyvalue"
	agentKey = "mai1:cursor:testagentkeyvalue"
)

// The helper process of TestTwoProcessesLoseNoKey: it writes keys to the key file of the
// folder in MAI_KEYS_HELPER_DIR and exits.
func TestMain(m *testing.M) {
	if dir := os.Getenv("MAI_KEYS_HELPER_DIR"); dir != "" {
		os.Exit(helper(dir, os.Getenv("MAI_KEYS_HELPER_NAME")))
	}
	os.Exit(m.Run())
}

func helper(dir, name string) int {
	f := Open(dir, nil)
	for i := range 25 {
		if err := f.Set(AgentAccount("mai", fmt.Sprintf("%s-%d", name, i)), "mai1:x:"+name); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	return 0
}

// readDoc reads the key file in dir with no lock, for a check.
func readDoc(t *testing.T, dir string) doc {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	var d doc
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestSetGetDeleteWithTheModes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	f := Open(dir, nil)
	if _, err := f.Get(OwnerAccount("mai")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get with no file: %v", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a read made the folder")
	}
	if err := f.Set(OwnerAccount("mai"), ownerKey); err != nil {
		t.Fatal(err)
	}
	if err := f.Set(AgentAccount("mai", "cursor"), agentKey); err != nil {
		t.Fatal(err)
	}
	if got, err := f.Get(OwnerAccount("mai")); err != nil || got != ownerKey {
		t.Fatalf("get: equal = %v, %v", got == ownerKey, err)
	}
	d := readDoc(t, dir)
	if d.Version != 1 || len(d.Keys) != 2 || d.Keys["agent:cursor@mai"] != agentKey {
		t.Fatalf("the file has %d keys, version %d", len(d.Keys), d.Version)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(f.Path())
		di, _ := os.Stat(dir)
		if fi.Mode().Perm() != 0o600 || di.Mode().Perm() != 0o700 {
			t.Fatalf("modes = %v and %v", fi.Mode().Perm(), di.Mode().Perm())
		}
	}
	if err := f.Delete(OwnerAccount("mai")); err != nil {
		t.Fatal(err)
	}
	if err := f.Delete(OwnerAccount("mai")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	if ok, err := f.Has(AgentAccount("mai", "cursor")); err != nil || !ok {
		t.Fatalf("has = %v, %v", ok, err)
	}
	if err := f.Set("", "x"); err == nil {
		t.Fatal("set accepted an empty account")
	}
	if err := f.Set("owner@x", ""); err == nil {
		t.Fatal("set accepted an empty key")
	}
}

func TestAWriteLeavesNoTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	f := Open(dir, nil)
	for i := range 5 {
		if err := f.Set(OwnerAccount("mai"), ownerKey+strconv.Itoa(i)); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Join(names, ",") != "keys.json,keys.json.lock" {
		t.Fatalf("the folder holds %v", names)
	}
}

func TestAKeyFileThatOthersCanReadIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no Unix mode")
	}
	dir := t.TempDir()
	f := Open(dir, nil)
	if err := f.Set(OwnerAccount("mai"), ownerKey); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(f.Path(), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := f.Get(OwnerAccount("mai"))
	var me *ModeError
	if !errors.As(err, &me) || !strings.Contains(err.Error(), "chmod 600") || !strings.Contains(err.Error(), f.Path()) {
		t.Fatalf("get: %v", err)
	}
	if strings.Contains(err.Error(), ownerKey) {
		t.Fatal("the error shows the key")
	}
	if err := f.Set(AgentAccount("mai", "cursor"), agentKey); !errors.As(err, &me) {
		t.Fatalf("set: %v", err)
	}
	if fi, _ := os.Stat(f.Path()); fi.Mode().Perm() != 0o644 {
		t.Fatal("the refused write changed the file")
	}
	if err := f.Check(); !errors.As(err, &me) {
		t.Fatalf("check: %v", err)
	}
	_ = os.Chmod(f.Path(), 0o600)
	if err := f.Check(); err != nil {
		t.Fatalf("check after chmod 600: %v", err)
	}
}

func TestAWriteGivesTheFolderTheMode0700(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no Unix mode")
	}
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Open(dir, nil).Set(OwnerAccount("mai"), ownerKey); err != nil {
		t.Fatal(err)
	}
	if di, _ := os.Stat(dir); di.Mode().Perm() != 0o700 {
		t.Fatalf("the folder has the mode %v", di.Mode().Perm())
	}
}

func TestABadKeyFileGivesAnErrorWithNoKey(t *testing.T) {
	dir := t.TempDir()
	f := Open(dir, nil)
	if err := os.WriteFile(f.Path(), []byte(`{"keys":{"owner@mai":"`+ownerKey+`"`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := f.Get(OwnerAccount("mai"))
	if err == nil || strings.Contains(err.Error(), ownerKey) || !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("get: %v", err)
	}
	if err := f.Set(OwnerAccount("mai"), "mai1:owner:other"); err == nil {
		t.Fatal("set replaced a key file that is not valid")
	}
	if err := os.WriteFile(f.Path(), []byte(`{"version":2,"keys":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Get(OwnerAccount("mai")); err == nil || !strings.Contains(err.Error(), "version 2") {
		t.Fatalf("a newer version: %v", err)
	}
	if err := os.WriteFile(f.Path(), []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Get(OwnerAccount("mai")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an empty file: %v", err)
	}
}

func TestGoroutinesLoseNoKey(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	for i := range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := Open(dir, nil).Set(AgentAccount("mai", "a"+strconv.Itoa(i)), "mai1:x:v"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if n := len(readDoc(t, dir).Keys); n != 40 {
		t.Fatalf("the file has %d keys, want 40", n)
	}
}

func TestTwoProcessesLoseNoKey(t *testing.T) {
	dir := t.TempDir()
	var cmds []*exec.Cmd
	for _, name := range []string{"p1", "p2", "p3", "p4"} {
		cmd := exec.Command(os.Args[0], "-test.run=^$")
		cmd.Env = append(os.Environ(), "MAI_KEYS_HELPER_DIR="+dir, "MAI_KEYS_HELPER_NAME="+name)
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		cmds = append(cmds, cmd)
	}
	for _, cmd := range cmds {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("a helper process failed: %v", err)
		}
	}
	if n := len(readDoc(t, dir).Keys); n != 100 {
		t.Fatalf("the file has %d keys, want 100", n)
	}
}

func TestTheFileLockRefusesASecondHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.json.lock")
	a, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if ok, err := tryLock(a); !ok || err != nil {
		t.Fatalf("first lock = %v, %v", ok, err)
	}
	if ok, err := tryLock(b); ok || err != nil {
		t.Fatalf("second lock while the first holds = %v, %v", ok, err)
	}
	if err := unlock(a); err != nil {
		t.Fatal(err)
	}
	if ok, err := tryLock(b); !ok || err != nil {
		t.Fatalf("second lock after the unlock = %v, %v", ok, err)
	}
	_ = unlock(b)
}

func TestInstanceOfAndTheAccountsOfAnInstance(t *testing.T) {
	cases := map[string]string{
		"owner@mai":              "mai",
		"agent:cursor@mai-dev":   "mai-dev",
		"agent:cursor@x@y":       "y",
		"owner":                  "",
		AgentAccount("a.b", "c"): "a.b",
	}
	for account, want := range cases {
		if got := InstanceOf(account); got != want {
			t.Errorf("InstanceOf(%q) = %q, want %q", account, got, want)
		}
	}
	f := Open(t.TempDir(), nil)
	for _, a := range []string{OwnerAccount("mai"), AgentAccount("mai", "cursor"), OwnerAccount("mai-dev"), AgentAccount("mai-dev", "cursor")} {
		if err := f.Set(a, "mai1:x:"+a); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := f.OfInstance("mai"); strings.Join(got, ",") != "agent:cursor@mai,owner@mai" {
		t.Fatalf("OfInstance = %v", got)
	}
	gone, err := f.DeleteInstance("mai")
	if err != nil || strings.Join(gone, ",") != "agent:cursor@mai,owner@mai" {
		t.Fatalf("DeleteInstance = %v, %v", gone, err)
	}
	if all, _ := f.Accounts(); strings.Join(all, ",") != "agent:cursor@mai-dev,owner@mai-dev" {
		t.Fatalf("the keys of the other instance: %v", all)
	}
	if gone, err := f.DeleteInstance("mai"); err != nil || len(gone) != 0 {
		t.Fatalf("second DeleteInstance = %v, %v", gone, err)
	}
}

func TestDefaultWithMAIHomeHasNoLegacyKeychain(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MAI_HOME", home)
	f, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if f.HasLegacy() || f.Path() != filepath.Join(home, "state", FileName) {
		t.Fatalf("Default() = %s, legacy %v", f.Path(), f.HasLegacy())
	}
}

// failDelete is a legacy keychain whose delete fails.
type failDelete struct{ *keychain.Memory }

func (failDelete) Delete(string) error { return errors.New("the delete failed") }

func TestGetMovesTheLegacyItemToTheKeyFile(t *testing.T) {
	dir := t.TempDir()
	legacy := keychain.NewMemory()
	legacy.Put(OwnerAccount("mai"), ownerKey)
	legacy.Put(OwnerAccount("mai-dev"), "mai1:owner:other")
	f := Open(dir, legacy)
	got, err := f.Get(OwnerAccount("mai"))
	if err != nil || got != ownerKey {
		t.Fatalf("get: equal = %v, %v", got == ownerKey, err)
	}
	if readDoc(t, dir).Keys[OwnerAccount("mai")] != ownerKey {
		t.Fatal("the key file has no moved key")
	}
	if strings.Join(legacy.Accounts(), ",") != "owner@mai-dev" {
		t.Fatalf("legacy items after the move: %v", legacy.Accounts())
	}
	// The next Get reads the key file and does not read the keychain.
	legacy.Err = errors.New("no read")
	if got, err := f.Get(OwnerAccount("mai")); err != nil || got != ownerKey {
		t.Fatalf("second get: %v", err)
	}
}

func TestGetKeepsTheKeyOfTheKeyFile(t *testing.T) {
	dir := t.TempDir()
	legacy := keychain.NewMemory()
	legacy.Put(AgentAccount("mai", "cursor"), "mai1:cursor:old")
	f := Open(dir, legacy)
	if err := f.Set(AgentAccount("mai", "cursor"), agentKey); err != nil {
		t.Fatal(err)
	}
	if got, err := f.Get(AgentAccount("mai", "cursor")); err != nil || got != agentKey {
		t.Fatalf("get: equal = %v, %v", got == agentKey, err)
	}
}

func TestAFailedReadOfTheKeychainIsNotErrNotFound(t *testing.T) {
	legacy := keychain.NewMemory()
	legacy.Put(OwnerAccount("mai"), ownerKey)
	legacy.Err = errors.New("the user refused the prompt")
	f := Open(t.TempDir(), legacy)
	_, err := f.Get(OwnerAccount("mai"))
	var le *LegacyError
	if !errors.As(err, &le) || errors.Is(err, ErrNotFound) || le.Account != OwnerAccount("mai") {
		t.Fatalf("get: %v", err)
	}
	legacy.Err = nil
	if ok, _ := legacy.Has(OwnerAccount("mai")); !ok {
		t.Fatal("the failed read removed the legacy item")
	}
	legacy.Err = keychain.ErrUnavailable
	if _, err := f.Get(OwnerAccount("mai")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get with no keychain: %v", err)
	}
}

func TestAKeyFileWithABadModeMovesNoKey(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no Unix mode")
	}
	dir := t.TempDir()
	legacy := keychain.NewMemory()
	legacy.Put(OwnerAccount("mai"), ownerKey)
	f := Open(dir, legacy)
	if err := f.Set(AgentAccount("mai", "cursor"), agentKey); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(f.Path(), 0o644)
	var me *ModeError
	if _, err := f.Get(OwnerAccount("mai")); !errors.As(err, &me) {
		t.Fatalf("get: %v; want a ModeError", err)
	}
	if ok, _ := legacy.Has(OwnerAccount("mai")); !ok {
		t.Fatal("the legacy item is gone, and the key file has no key")
	}
}

// chmodOnGet is a legacy keychain whose read gives the key file the mode 0644, as a
// different program can do. Thus the read of the key file before the move succeeds, and the
// write of the move fails.
type chmodOnGet struct {
	*keychain.Memory
	path string
}

func (c chmodOnGet) Get(account string) (string, error) {
	if err := os.Chmod(c.path, 0o644); err != nil {
		return "", err
	}
	return c.Memory.Get(account)
}

func TestAFailedWriteKeepsTheLegacyItem(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no Unix mode")
	}
	dir := t.TempDir()
	legacy := keychain.NewMemory()
	legacy.Put(OwnerAccount("mai"), ownerKey)
	if err := Open(dir, nil).Set(AgentAccount("mai", "cursor"), agentKey); err != nil {
		t.Fatal(err)
	}
	f := Open(dir, chmodOnGet{Memory: legacy, path: filepath.Join(dir, FileName)})
	_, err := f.Get(OwnerAccount("mai"))
	var me *ModeError
	if !errors.As(err, &me) || !strings.Contains(err.Error(), "move "+OwnerAccount("mai")) {
		t.Fatalf("get: %v; want the error of the write of the move", err)
	}
	if ok, _ := legacy.Has(OwnerAccount("mai")); !ok {
		t.Fatal("the legacy item is gone, and the key file has no key")
	}
}

func TestAFailedDeleteOfTheLegacyItemKeepsTheMovedKey(t *testing.T) {
	dir := t.TempDir()
	m := keychain.NewMemory()
	m.Put(OwnerAccount("mai"), ownerKey)
	f := Open(dir, failDelete{m})
	if got, err := f.Get(OwnerAccount("mai")); err != nil || got != ownerKey {
		t.Fatalf("get: %v", err)
	}
	if readDoc(t, dir).Keys[OwnerAccount("mai")] != ownerKey {
		t.Fatal("the key file has no moved key")
	}
}

func TestTwoGetsAtTheSameTimeMoveOneKey(t *testing.T) {
	dir := t.TempDir()
	legacy := keychain.NewMemory()
	legacy.Put(OwnerAccount("mai"), ownerKey)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := Open(dir, legacy).Get(OwnerAccount("mai"))
			if err != nil || got != ownerKey {
				t.Errorf("get: equal = %v, %v", got == ownerKey, err)
			}
		}()
	}
	wg.Wait()
	if len(legacy.Accounts()) != 0 || len(readDoc(t, dir).Keys) != 1 {
		t.Fatalf("legacy %v, key file %d keys", legacy.Accounts(), len(readDoc(t, dir).Keys))
	}
}

func TestMoveAllMovesAndCleans(t *testing.T) {
	dir := t.TempDir()
	legacy := keychain.NewMemory()
	legacy.Put(OwnerAccount("mai"), ownerKey)
	legacy.Put(AgentAccount("mai", "cursor"), agentKey)
	legacy.Put(AgentAccount("mai-dev", "cursor"), "mai1:cursor:dev")
	f := Open(dir, legacy)
	if err := f.Set(AgentAccount("mai", "cursor"), agentKey); err != nil {
		t.Fatal(err)
	}
	accounts, err := f.Candidates("mai", []string{AgentAccount("mai", "codex"), AgentAccount("mai-dev", "cursor")})
	if err != nil || strings.Join(accounts, ",") != "agent:codex@mai,agent:cursor@mai,owner@mai" {
		t.Fatalf("candidates = %v, %v", accounts, err)
	}
	items, err := f.LegacyItems(accounts)
	if err != nil || strings.Join(items, ",") != "agent:cursor@mai,owner@mai" {
		t.Fatalf("legacy items = %v, %v", items, err)
	}
	r, err := f.MoveAll(accounts, nil)
	if err != nil || strings.Join(r.Moved, ",") != "owner@mai" || strings.Join(r.Cleaned, ",") != "agent:cursor@mai" ||
		len(r.Conflicts) != 0 || r.Unavailable {
		t.Fatalf("MoveAll = %+v, %v", r, err)
	}
	d := readDoc(t, dir)
	if d.Keys[OwnerAccount("mai")] != ownerKey || d.Keys[AgentAccount("mai", "cursor")] != agentKey {
		t.Fatal("the key file does not have the moved key and the key of the key file")
	}
	if strings.Join(legacy.Accounts(), ",") != "agent:cursor@mai-dev" {
		t.Fatalf("legacy items after MoveAll: %v", legacy.Accounts())
	}
	legacy.Err = fmt.Errorf("%w: no Secret Service", keychain.ErrUnavailable)
	if r, err := f.MoveAll(accounts, nil); err != nil || !r.Unavailable || len(r.Moved) != 0 {
		t.Fatalf("MoveAll with no keychain = %+v, %v", r, err)
	}
	if _, err := f.LegacyItems(accounts); !errors.Is(err, keychain.ErrUnavailable) {
		t.Fatalf("LegacyItems with no keychain: %v", err)
	}
}

func TestMoveAllGivesEachFailure(t *testing.T) {
	legacy := keychain.NewMemory()
	legacy.Put(OwnerAccount("mai"), ownerKey)
	f := Open(t.TempDir(), refuseGet{legacy})
	r, err := f.MoveAll([]string{OwnerAccount("mai")}, nil)
	if err == nil || len(r.Moved) != 0 || !strings.Contains(err.Error(), "owner@mai") {
		t.Fatalf("MoveAll = %+v, %v", r, err)
	}
	if ok, _ := legacy.Has(OwnerAccount("mai")); !ok {
		t.Fatal("the failed move removed the legacy item")
	}
}

func TestWithNoLegacyKeychainTheLegacyCallsDoNothing(t *testing.T) {
	f := Open(t.TempDir(), nil)
	if items, err := f.LegacyItems([]string{OwnerAccount("mai")}); err != nil || items != nil {
		t.Fatalf("LegacyItems = %v, %v", items, err)
	}
	if ok, err := f.HasLegacyItem(OwnerAccount("mai")); ok || err != nil {
		t.Fatalf("HasLegacyItem = %v, %v", ok, err)
	}
	if err := f.DeleteLegacy(OwnerAccount("mai")); err != nil {
		t.Fatal(err)
	}
	if r, err := f.MoveAll([]string{OwnerAccount("mai")}, nil); err != nil || len(r.Moved) != 0 {
		t.Fatalf("MoveAll = %+v, %v", r, err)
	}
}

// refuseGet is a legacy keychain whose read fails, as when the user refuses the prompt.
type refuseGet struct{ *keychain.Memory }

func (refuseGet) Get(string) (string, error) { return "", errors.New("the user refused the prompt") }

// recordItem writes a manifest in dir that records the legacy item of the account, as mai 3.0
// and 3.1 did.
func recordItem(t *testing.T, dir, account string) {
	t.Helper()
	m, err := manifest.Load(filepath.Join(dir, manifest.FileName), InstanceOf(account))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Add(manifest.Item{Type: manifest.KeychainKey, Name: account}); err != nil {
		t.Fatal(err)
	}
}

func TestAKeychainThatIsNotAvailableGivesALegacyErrorForARecordedAccount(t *testing.T) {
	dir := t.TempDir()
	legacy := keychain.NewMemory()
	legacy.Err = fmt.Errorf("%w: no session bus", keychain.ErrUnavailable)
	f := Open(dir, legacy)
	if _, err := f.Get(OwnerAccount("mai")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get with no record: %v, want ErrNotFound", err)
	}
	recordItem(t, dir, OwnerAccount("mai"))
	_, err := f.Get(OwnerAccount("mai"))
	var le *LegacyError
	if !errors.As(err, &le) || errors.Is(err, ErrNotFound) || le.Account != OwnerAccount("mai") ||
		!strings.Contains(err.Error(), "keychain is not available") || !strings.Contains(err.Error(), "do the command again") {
		t.Fatalf("get with a record: %v, want a LegacyError with a step", err)
	}
	if _, err := f.Get(OwnerAccount("mai-dev")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get of a different instance: %v, want ErrNotFound", err)
	}
	if err := os.WriteFile(filepath.Join(dir, manifest.FileName), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Get(AgentAccount("mai", "cursor")); errors.Is(err, ErrNotFound) {
		t.Fatal("get with a manifest that is not valid gives ErrNotFound")
	}
}

func TestMoveAllKeepsALegacyItemWithADifferentKey(t *testing.T) {
	dir := t.TempDir()
	legacy := keychain.NewMemory()
	f := Open(dir, legacy)
	account := OwnerAccount("mai")
	if err := f.Set(account, "mai1:owner:file"); err != nil {
		t.Fatal(err)
	}
	legacy.Put(account, "mai1:owner:item")
	var asked []string
	server := func(accepted string, err error) Accepts {
		return func(a, key string) (bool, error) {
			asked = append(asked, key)
			return key == accepted, err
		}
	}
	for _, tc := range []struct {
		name    string
		accepts Accepts
	}{
		{"no check", nil},
		{"the server accepts the key of the item", server("mai1:owner:item", nil)},
		{"the server refuses the two keys", server("", nil)},
		{"the check fails", server("", errors.New("connection refused"))},
	} {
		r, err := f.MoveAll([]string{account}, tc.accepts)
		if err != nil || len(r.Cleaned) != 0 || len(r.Moved) != 0 || len(r.Conflicts) != 1 || r.Conflicts[0].Account != account {
			t.Fatalf("%s: MoveAll = %+v, %v", tc.name, r, err)
		}
		if got, _ := legacy.Get(account); got != "mai1:owner:item" {
			t.Fatalf("%s: the legacy item is gone", tc.name)
		}
		if got := readDoc(t, dir).Keys[account]; got != "mai1:owner:file" {
			t.Fatalf("%s: the key of the key file changed", tc.name)
		}
	}
	asked = nil
	r, err := f.MoveAll([]string{account}, server("mai1:owner:file", nil))
	if err != nil || strings.Join(r.Cleaned, ",") != account || len(r.Conflicts) != 0 {
		t.Fatalf("the server accepts the key of the key file: MoveAll = %+v, %v", r, err)
	}
	if strings.Join(asked, ",") != "mai1:owner:file,mai1:owner:item" {
		t.Fatalf("the check asked about %d key(s), want each of the two keys", len(asked))
	}
	if ok, _ := legacy.Has(account); ok {
		t.Fatal("the legacy item with the key that the server refuses stays")
	}
}

// writeOnGet is a legacy keychain whose read writes a different key for the account to the
// key file, as a different process can do during a move.
type writeOnGet struct {
	*keychain.Memory
	dir string
}

func (w writeOnGet) Get(account string) (string, error) {
	if err := Open(w.dir, nil).Set(account, "mai1:owner:other"); err != nil {
		return "", err
	}
	return w.Memory.Get(account)
}

func TestAMoveKeepsTheLegacyItemWhenADifferentProcessWritesADifferentKey(t *testing.T) {
	dir := t.TempDir()
	legacy := keychain.NewMemory()
	legacy.Put(OwnerAccount("mai"), ownerKey)
	f := Open(dir, writeOnGet{Memory: legacy, dir: dir})
	if got, err := f.Get(OwnerAccount("mai")); err != nil || got != "mai1:owner:other" {
		t.Fatalf("get: %v; want the key of the key file", err)
	}
	if got, _ := legacy.Get(OwnerAccount("mai")); got != ownerKey {
		t.Fatal("the legacy item with a different key is gone")
	}
	if err := f.Delete(OwnerAccount("mai")); err != nil {
		t.Fatal(err)
	}
	r, err := f.MoveAll([]string{OwnerAccount("mai")}, nil)
	if err != nil || len(r.Moved) != 0 || len(r.Conflicts) != 1 {
		t.Fatalf("MoveAll = %+v, %v", r, err)
	}
	if ok, _ := legacy.Has(OwnerAccount("mai")); !ok {
		t.Fatal("the legacy item is gone")
	}
}
