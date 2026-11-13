package main

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/keychain"
	"github.com/monorithm/mai/cli/v3/internal/keys"
	"github.com/monorithm/mai/cli/v3/internal/manifest"
)

// downKeychain is a legacy keychain in memory that is not available while down is true, as
// the Secret Service of a Linux host in a shell with no session bus, for example an SSH
// session. Then it is available again, with the same items.
type downKeychain struct {
	*keychain.Memory
	down atomic.Bool
}

func (k *downKeychain) err() error {
	return fmt.Errorf("%w: no session bus", keychain.ErrUnavailable)
}

func (k *downKeychain) Get(a string) (string, error) {
	if k.down.Load() {
		return "", k.err()
	}
	return k.Memory.Get(a)
}

func (k *downKeychain) Has(a string) (bool, error) {
	if k.down.Load() {
		return false, k.err()
	}
	return k.Memory.Has(a)
}

func (k *downKeychain) Delete(a string) error {
	if k.down.Load() {
		return k.err()
	}
	return k.Memory.Delete(a)
}

// captureLifecycle sends the output of the lifecycle commands to two buffers.
func captureLifecycle(t *testing.T) (out, errs *bytes.Buffer) {
	t.Helper()
	out, errs = &bytes.Buffer{}, &bytes.Buffer{}
	oldOut, oldErr := lcOut, lcErr
	lcOut, lcErr = out, errs
	t.Cleanup(func() { lcOut, lcErr = oldOut, oldErr })
	return out, errs
}

// useLegacy makes the commands use the key file in the state folder of MAI_HOME, with the
// legacy keychain legacy.
func useLegacy(t *testing.T, legacy keychain.Store) string {
	t.Helper()
	state := filepath.Join(os.Getenv("MAI_HOME"), "state")
	old := openKeys
	openKeys = func() (*keys.File, error) { return keys.Open(state, legacy), nil }
	t.Cleanup(func() { openKeys = old })
	return state
}

// A 3.1 install keeps the owner key in the keychain, and the manifest records the item.
// When the keychain is not available at the first use of the owner key, the CLI must not
// take the key as missing: "mai start" then makes a new owner key that the server refuses
// (setOwner), and a subsequent "mai upgrade" deletes the keychain item that holds the
// only owner key that the server accepts.
func TestAKeychainThatIsNotAvailableOnceDoesNotLoseTheOwnerKey(t *testing.T) {
	t.Setenv("MAI_HOME", t.TempDir())
	out, _ := captureLifecycle(t)
	const live = "mai1:owner:theliveownerkeyofthe31install"
	account := keys.OwnerAccount("mai")
	legacy := &downKeychain{Memory: keychain.NewMemory()}
	legacy.Put(account, live)
	m := testManifest(t, manifest.Item{Type: manifest.KeychainKey, Name: account})
	state := useLegacy(t, legacy)

	// "mai start" in a shell with no session bus: setOwner reads the owner key. On
	// ErrNotFound it makes a new owner key and keeps it in the key file.
	legacy.down.Store(true)
	kf, _ := openKeys()
	_, err := kf.Get(account)
	if errors.Is(err, keys.ErrNotFound) {
		made, kerr := lcNewOwnerKey()
		if kerr != nil {
			t.Fatal(kerr)
		}
		if err := kf.Set(account, made); err != nil {
			t.Fatal(err)
		}
	}

	// "mai upgrade" later, with the keychain available again.
	legacy.down.Store(false)
	lcMoveKeys("mai", m)

	plain := keys.Open(state, nil)
	inFile, _ := plain.Get(account)
	inKeychain, _ := legacy.Memory.Get(account)
	if inFile != live && inKeychain != live {
		t.Errorf("the owner key that the server accepts is gone: the key file has a new key, and the keychain item is deleted; stdout: %q",
			strings.TrimSpace(out.String()))
	}
}

// With no record of a legacy item, a keychain that is not available holds no key of mai:
// mai 3.0 and 3.1 did not operate with no keychain. With a record, the read gives the step
// for the user, and it is not "no key".
func TestAKeychainThatIsNotAvailableIsNoKeyOnlyWithNoRecord(t *testing.T) {
	t.Setenv("MAI_HOME", t.TempDir())
	legacy := &downKeychain{Memory: keychain.NewMemory()}
	legacy.Put(keys.OwnerAccount("mai"), "mai1:owner:legacy")
	legacy.down.Store(true)
	useLegacy(t, legacy)
	kf, _ := openKeys()
	if _, err := kf.Get(keys.AgentAccount("mai", "cursor")); !errors.Is(err, keys.ErrNotFound) {
		t.Fatalf("an account with no record: %v, want ErrNotFound", err)
	}
	testManifest(t, manifest.Item{Type: manifest.KeychainKey, Name: keys.OwnerAccount("mai")})
	_, err := kf.Get(keys.OwnerAccount("mai"))
	var le *keys.LegacyError
	if !errors.As(err, &le) || errors.Is(err, keys.ErrNotFound) || !strings.Contains(err.Error(), "Make the keychain available") {
		t.Fatalf("an account with a record: %v, want a LegacyError with a step", err)
	}
	if strings.Contains(err.Error(), "mai1:") {
		t.Fatal("the error shows a key")
	}
	if got := lcOwnerKey("mai"); got != "" {
		t.Fatal("lcOwnerKey gave a key")
	}
	if ok, _ := legacy.Memory.Has(keys.OwnerAccount("mai")); !ok {
		t.Fatal("the legacy item is gone")
	}
}

// When the keychain is not available, "mai upgrade" cannot move the recorded items. It tells
// about them, gives the step, and fails. The records stay.
func TestUpgradeWithAKeychainThatIsNotAvailableTellsAboutTheRecordedItems(t *testing.T) {
	t.Setenv("MAI_HOME", t.TempDir())
	_, errs := captureLifecycle(t)
	legacy := &downKeychain{Memory: keychain.NewMemory()}
	legacy.Put(keys.OwnerAccount("mai"), "mai1:owner:legacy")
	legacy.down.Store(true)
	useLegacy(t, legacy)
	m := testManifest(t, manifest.Item{Type: manifest.KeychainKey, Name: keys.OwnerAccount("mai")})
	if lcMoveKeys("mai", m) {
		t.Fatal("the move gave true, but the recorded item did not move")
	}
	text := errs.String()
	if !strings.Contains(text, "owner@mai") || !strings.Contains(text, "run mai upgrade again") {
		t.Fatalf("stderr: %q", text)
	}
	if len(m.Of(manifest.KeychainKey)) != 1 {
		t.Fatal("the record of the item is gone")
	}
}

// The key file and the keychain item of an agent have different keys. Nothing tells which key
// the server accepts, thus "mai upgrade" keeps both, and gives the step "mai agent rotate".
func TestUpgradeKeepsAKeychainItemWithADifferentAgentKey(t *testing.T) {
	t.Setenv("MAI_HOME", t.TempDir())
	out, errs := captureLifecycle(t)
	k := useTestKeys(t)
	k.legacy.Put(keys.AgentAccount("mai", "codex"), "mai1:codex:old")
	k.legacy.Put(keys.AgentAccount("mai", "cursor"), "mai1:cursor:same")
	if err := k.file.Set(keys.AgentAccount("mai", "codex"), "mai1:codex:new"); err != nil {
		t.Fatal(err)
	}
	if err := k.file.Set(keys.AgentAccount("mai", "cursor"), "mai1:cursor:same"); err != nil {
		t.Fatal(err)
	}
	m := testManifest(t,
		manifest.Item{Type: manifest.KeychainKey, Name: keys.AgentAccount("mai", "codex")},
		manifest.Item{Type: manifest.KeychainKey, Name: keys.AgentAccount("mai", "cursor")},
	)
	if lcMoveKeys("mai", m) {
		t.Fatal("the move gave true, but a keychain item with a different key stays")
	}
	if got, _ := k.legacy.Get(keys.AgentAccount("mai", "codex")); got != "mai1:codex:old" {
		t.Fatal("the keychain item with a different key is gone")
	}
	if ok, _ := k.legacy.Has(keys.AgentAccount("mai", "cursor")); ok {
		t.Fatal("the keychain item with the same key stays")
	}
	if got, _ := k.file.Get(keys.AgentAccount("mai", "codex")); got != "mai1:codex:new" {
		t.Fatal("the key of the key file changed")
	}
	if items := m.Of(manifest.KeychainKey); len(items) != 1 || items[0].Name != "agent:codex@mai" {
		t.Fatalf("the manifest records %+v", items)
	}
	text := errs.String()
	if !strings.Contains(text, "the keychain item agent:codex@mai stays") || !strings.Contains(text, "run mai agent rotate codex") {
		t.Fatalf("stderr: %q", text)
	}
	if !strings.Contains(out.String(), "removed 1 old keychain item(s)") {
		t.Fatalf("stdout: %q", out.String())
	}
	if strings.Contains(out.String()+text, "mai1:") {
		t.Fatal("the output shows a key")
	}
}

// For the owner key, the server tells which key it accepts. "mai upgrade" removes the
// keychain item only when the server accepts the key of the key file and refuses the key of
// the item.
func TestUpgradeRemovesTheOwnerItemOnlyWhenTheServerAcceptsTheKeyOfTheKeyFile(t *testing.T) {
	_, _, k, home := legacyInstall(t, map[string]string{keys.OwnerAccount("mai"): "mai1:owner:old"})
	out, errs := captureLifecycle(t)
	if err := k.file.Set(keys.OwnerAccount("mai"), testOwnerKey); err != nil {
		t.Fatal(err)
	}
	if !lcMoveKeys("mai", loadTestManifest(t, home)) {
		t.Fatalf("the move failed: %s", errs.String())
	}
	if ok, _ := k.legacy.Has(keys.OwnerAccount("mai")); ok {
		t.Fatal("the keychain item with the key that the server refuses stays")
	}
	if !strings.Contains(out.String(), "removed 1 old keychain item(s)") {
		t.Fatalf("stdout: %q", out.String())
	}

	// The server accepts the key of the keychain item, and refuses the key of the key file.
	k.legacy.Put(keys.OwnerAccount("mai"), testOwnerKey)
	if err := k.file.Set(keys.OwnerAccount("mai"), "mai1:owner:new"); err != nil {
		t.Fatal(err)
	}
	m := loadTestManifest(t, home)
	if err := m.Add(manifest.Item{Type: manifest.KeychainKey, Name: keys.OwnerAccount("mai")}); err != nil {
		t.Fatal(err)
	}
	errs.Reset()
	if lcMoveKeys("mai", m) {
		t.Fatal("the move gave true, but the keychain item with the accepted key stays")
	}
	if got, _ := k.legacy.Get(keys.OwnerAccount("mai")); got != testOwnerKey {
		t.Fatal("the keychain item with the key that the server accepts is gone")
	}
	text := errs.String()
	if !strings.Contains(text, "the server accepts the key of the keychain item") ||
		!strings.Contains(text, "remove the account owner@mai from the key file") {
		t.Fatalf("stderr: %q", text)
	}
	if strings.Contains(text, "mai1:") {
		t.Fatal("the output shows a key")
	}
}

// When the server does not reply, nothing proves which owner key it accepts. The keychain
// item stays.
func TestUpgradeKeepsTheOwnerItemWhenTheServerDoesNotReply(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MAI_HOME", home)
	_, errs := captureLifecycle(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(l.Addr().String())
	_ = l.Close()
	_ = os.MkdirAll(filepath.Join(home, "config"), 0o700)
	if err := os.WriteFile(filepath.Join(home, "config", "config.json"), []byte(`{"instance":"mai","port":`+port+`}`), 0o600); err != nil {
		t.Fatal(err)
	}
	k := useTestKeys(t)
	k.legacy.Put(keys.OwnerAccount("mai"), "mai1:owner:old")
	if err := k.file.Set(keys.OwnerAccount("mai"), "mai1:owner:new"); err != nil {
		t.Fatal(err)
	}
	if lcMoveKeys("mai", testManifest(t)) {
		t.Fatal("the move gave true")
	}
	if ok, _ := k.legacy.Has(keys.OwnerAccount("mai")); !ok {
		t.Fatal("the keychain item is gone")
	}
	if text := errs.String(); !strings.Contains(text, "the check of the server failed") || !strings.Contains(text, "mai start") {
		t.Fatalf("stderr: %q", text)
	}
}

// After "mai agent rotate", the server accepts only the new key. Thus the keychain item of
// the agent holds a key that is not valid, and the command removes it with its record.
func TestAgentRotateRemovesTheKeychainItemOfTheAgent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("MAI_HOME", filepath.Join(home, "maihome"))
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testOwnerKey || r.Method != http.MethodPost || r.URL.Path != "/api/agents/codex/key" {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"codex","agent_key":"mai1:codex:rotated"}`))
	}))
	t.Cleanup(s.Close)
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(s.URL, "http://"))
	cfgDir := filepath.Join(home, "maihome", "config")
	_ = os.MkdirAll(cfgDir, 0o700)
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(`{"instance":"mai","port":`+port+`}`), 0o600); err != nil {
		t.Fatal(err)
	}
	k := useTestKeys(t)
	if err := k.file.Set(keys.OwnerAccount("mai"), testOwnerKey); err != nil {
		t.Fatal(err)
	}
	if err := k.file.Set(keys.AgentAccount("mai", "codex"), "mai1:codex:new"); err != nil {
		t.Fatal(err)
	}
	k.legacy.Put(keys.AgentAccount("mai", "codex"), "mai1:codex:old")
	if err := loadTestManifest(t, home).Add(manifest.Item{Type: manifest.KeychainKey, Name: keys.AgentAccount("mai", "codex")}); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := runAgent([]string{"rotate", "codex"}, &out, &errOut); code != 0 {
		t.Fatalf("rotate: exit %d, stderr %q", code, errOut.String())
	}
	if got, _ := k.file.Get(keys.AgentAccount("mai", "codex")); got != "mai1:codex:rotated" {
		t.Fatal("the key file does not have the new key")
	}
	if ok, _ := k.legacy.Has(keys.AgentAccount("mai", "codex")); ok {
		t.Fatal("the keychain item of the agent stays after the rotate")
	}
	if items := loadTestManifest(t, home).Of(manifest.KeychainKey); len(items) != 0 {
		t.Fatalf("the manifest records %+v", items)
	}
}
