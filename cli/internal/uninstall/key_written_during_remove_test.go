package uninstall

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/keychain"
	"github.com/monorithm/mai/cli/v3/internal/keys"
)

// moveBeforeDelete is a legacy keychain. A bridge read the item of one account before the
// remove, and it writes the key to the key file just before the remove deletes the item.
type moveBeforeDelete struct {
	*keychain.Memory
	file    *keys.File
	account string
}

func (m moveBeforeDelete) Delete(account string) error {
	if account == m.account {
		if secret, err := m.Memory.Get(account); err == nil {
			if err := m.file.Set(account, secret); err != nil {
				return err
			}
		}
	}
	return m.Memory.Delete(account)
}

func TestUninstallRemovesAKeyOfTheInstanceThatABridgeWroteDuringTheRemove(t *testing.T) {
	e := setup(t)
	cursor := keys.AgentAccount("mai", "cursor")
	plain := keys.Open(e.dirs.State, nil)
	e.keys = keys.Open(e.dirs.State, moveBeforeDelete{Memory: e.kc, file: plain, account: cursor})
	var out bytes.Buffer
	code := e.uninstaller(&out).Run(context.Background())
	left, err := plain.OfInstance("mai")
	if err != nil {
		t.Fatal(err)
	}
	if len(left) > 0 && code == 0 {
		t.Fatalf("mai uninstall gave exit 0, but the key file holds the keys %v of the instance mai:\n%s", left, out.String())
	}
	if len(left) > 0 && !strings.Contains(out.String(), "failure key "+cursor) {
		t.Fatalf("mai uninstall does not report the key %s that stays:\n%s", cursor, out.String())
	}
}

func TestAKeyOfTheInstanceWrittenDuringTheRemoveGoesFromAKeyFileThatStays(t *testing.T) {
	e := setup(t)
	if err := e.keys.Set(keys.OwnerAccount("mai-dev"), "mai1:owner:dev"); err != nil {
		t.Fatal(err)
	}
	cursor := keys.AgentAccount("mai", "cursor")
	plain := keys.Open(e.dirs.State, nil)
	e.keys = keys.Open(e.dirs.State, moveBeforeDelete{Memory: e.kc, file: plain, account: cursor})
	var out bytes.Buffer
	if code := e.uninstaller(&out).Run(context.Background()); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
	all, err := plain.Accounts()
	if err != nil || strings.Join(all, ",") != "owner@mai-dev" {
		t.Fatalf("the key file holds %v, %v", all, err)
	}
	text := out.String()
	if !strings.Contains(text, "it holds the keys of the instances: mai-dev\n") || strings.Contains(text, "instances: mai,") ||
		!strings.Contains(text, "gone     key "+cursor) {
		t.Fatalf("the output does not tell the key file and the key of the instance:\n%s", text)
	}
}
