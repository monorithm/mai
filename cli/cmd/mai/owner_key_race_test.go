package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moby/moby/client"

	"github.com/monorithm/mai/cli/v3/internal/config"
	"github.com/monorithm/mai/cli/v3/internal/engine"
	"github.com/monorithm/mai/cli/v3/internal/keychain"
	"github.com/monorithm/mai/cli/v3/internal/keys"
	"github.com/monorithm/mai/cli/v3/internal/paths"
)

// otherWriter is a legacy keychain. During the read of the item of one account, a different
// process writes a key for that account to the key file.
type otherWriter struct {
	*keychain.Memory
	dir, account, key string
}

func (o otherWriter) Get(account string) (string, error) {
	if account == o.account {
		if err := keys.Open(o.dir, nil).Set(account, o.key); err != nil {
			return "", err
		}
	}
	return o.Memory.Get(account)
}

func TestUpgradeDoesAServerCheckWhenADifferentProcessWritesTheOwnerKey(t *testing.T) {
	// The server accepts only testOwnerKey, the key of the keychain item.
	_, _, k, home := legacyInstall(t, map[string]string{keys.OwnerAccount("mai"): testOwnerKey})
	dir := filepath.Dir(k.file.Path())
	other := otherWriter{Memory: k.legacy, dir: dir, account: keys.OwnerAccount("mai"), key: "mai1:owner:otherprocess"}
	old := openKeys
	openKeys = func() (*keys.File, error) { return keys.Open(dir, other), nil }
	t.Cleanup(func() { openKeys = old })
	var out, errs bytes.Buffer
	oldOut, oldErr := lcOut, lcErr
	lcOut, lcErr = &out, &errs
	t.Cleanup(func() { lcOut, lcErr = oldOut, oldErr })

	if lcMoveKeys("mai", configPort(t), loadTestManifest(t, home)) {
		t.Fatal("the move gave true, but the keychain item stays")
	}
	text := errs.String()
	if strings.Contains(text, "the server refuses the two keys") || strings.Contains(text, "set-key --replace") {
		t.Fatalf("the server accepts the key of the keychain item, but mai upgrade tells that it refuses the two keys "+
			"and gives the step to replace the owner key of the server:\n%s", text)
	}
	if !strings.Contains(text, "the server accepts the key of the keychain item") {
		t.Fatalf("mai upgrade does not tell the result of the check of the server:\n%s", text)
	}
	if ok, _ := k.legacy.Has(keys.OwnerAccount("mai")); !ok {
		t.Fatal("the keychain item with the key that the server accepts is gone")
	}
}

func TestAConflictWithNoCheckOfTheServerTellsNothingAboutTheServer(t *testing.T) {
	text := lcConflictText("mai", "keys.json", keys.Conflict{Account: keys.OwnerAccount("mai")})
	if strings.Contains(text, "server refuses") || strings.Contains(text, "server accepts") || strings.Contains(text, "--replace") ||
		!strings.Contains(text, "mai upgrade again") {
		t.Fatalf("the text for a conflict with no check is %q", text)
	}
}

// movedByOther is a legacy keychain with the item of the owner key. During the read, a
// different process moves the item: it writes the key to the key file, and then it deletes
// the item. Thus the read finds no item.
type movedByOther struct {
	*keychain.Memory
	dir string
}

func (m movedByOther) Get(account string) (string, error) {
	secret, err := m.Memory.Get(account)
	if err != nil {
		return "", err
	}
	if err := keys.Open(m.dir, nil).Set(account, secret); err != nil {
		return "", err
	}
	if err := m.Memory.Delete(account); err != nil {
		return "", err
	}
	return "", keychain.ErrNotFound
}

func TestStartKeepsTheOwnerKeyThatADifferentProcessMoved(t *testing.T) {
	t.Setenv("MAI_HOME", t.TempDir())
	dirs, _ := paths.Default()
	legacy := keychain.NewMemory()
	legacy.Put(keys.OwnerAccount("mai"), "mai1:owner:legacy")
	other := movedByOther{Memory: legacy, dir: dirs.State}
	old := openKeys
	openKeys = func() (*keys.File, error) { return keys.Open(dirs.State, other), nil }
	t.Cleanup(func() { openKeys = old })
	var out, errs bytes.Buffer
	oldOut, oldErr := lcOut, lcErr
	lcOut, lcErr = &out, &errs
	t.Cleanup(func() { lcOut, lcErr = oldOut, oldErr })
	// An engine with no server: the exec of "mai owner set-key" fails after the write of the
	// key file.
	api, err := client.New(client.WithHost("tcp://127.0.0.1:1"))
	if err != nil {
		t.Fatal(err)
	}
	s := &lcSession{dirs: dirs, cfg: config.Defaults(), eng: &engine.Engine{API: api}}
	defer s.close()
	_ = s.setOwner(context.Background(), "mai")
	got, err := keys.Open(dirs.State, nil).Get(keys.OwnerAccount("mai"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "mai1:owner:legacy" {
		t.Fatalf("mai start replaced the owner key that a different process moved from the keychain item; "+
			"the keychain items are %v", legacy.Accounts())
	}
}
