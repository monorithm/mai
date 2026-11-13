package main

import (
	"bytes"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/keys"
	"github.com/monorithm/mai/cli/v3/internal/manifest"
)

// The keychain item of cursor stays from an install of mai 3.1 whose server data is gone. The
// server then has no agent cursor. "mai agent add cursor" makes the agent with a new key, thus
// the server accepts only the new key, and the keychain item holds a key that is not valid.
// As "mai agent rotate", the command must remove the item. Then "mai upgrade" has no item to
// keep and no conflict.
func TestAgentAddRemovesTheKeychainItemOfTheAgent(t *testing.T) {
	_, k, home := testEnv(t)
	cursor := keys.AgentAccount("mai", "cursor")
	k.legacy.Put(cursor, "mai1:cursor:oldsecret")
	if err := loadTestManifest(t, home).Add(manifest.Item{Type: manifest.KeychainKey, Name: cursor}); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := runAgent([]string{"add", "cursor"}, &out, &errOut); code != 0 {
		t.Fatalf("add: exit %d, stderr %q", code, errOut.String())
	}
	if got := fileKeys(t, k)[cursor]; got != "mai1:cursor:agentsecret" {
		t.Fatal("the key file does not have the new key of cursor")
	}
	if ok, _ := k.legacy.Has(cursor); ok {
		t.Error("the keychain item of cursor stays after mai agent add, with a key that the server refuses")
	}
	_, errs := captureLifecycle(t)
	if !lcMoveKeys("mai", 0, loadTestManifest(t, home)) {
		t.Errorf("mai upgrade after mai agent add fails:\n%s", errs.String())
	}
}
