package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/moby/moby/client"

	"github.com/monorithm/mai/cli/v3/internal/config"
	"github.com/monorithm/mai/cli/v3/internal/engine"
	"github.com/monorithm/mai/cli/v3/internal/keychain"
	"github.com/monorithm/mai/cli/v3/internal/keys"
	"github.com/monorithm/mai/cli/v3/internal/paths"
)

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
