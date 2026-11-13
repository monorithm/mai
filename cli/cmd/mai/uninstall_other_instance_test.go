package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/keys"
	"github.com/monorithm/mai/cli/v3/internal/manifest"
	"github.com/monorithm/mai/cli/v3/internal/paths"
	"github.com/monorithm/mai/cli/v3/internal/uninstall"
)

// closedURL is the base URL of the server for the uninstall in these tests. No server listens
// on it. Thus the end of the leases never sends a key to a server of the user, also when the
// configuration file is gone and the port is the default port.
const closedURL = "http://127.0.0.1:1"

// A 3.1 install of the instance mai and a second instance mai-dev share the state folder.
// The keychain holds the items of mai, and the manifest records them. "mai uninstall
// --instance mai-dev" must not change what the commands of mai know about the items of mai.
func TestTheUninstallOfADifferentInstanceKeepsTheMoveOfTheKeychainItems(t *testing.T) {
	_, _, k, home := legacyInstall(t, map[string]string{
		keys.OwnerAccount("mai"):           testOwnerKey,
		keys.AgentAccount("mai", "cursor"): legacyCursorKey,
	})
	// The second instance, as "mai start --instance mai-dev" makes it.
	if err := loadTestManifest(t, home).Add(manifest.Item{Type: manifest.Container, Name: "mai-dev", ID: "c-dev"}); err != nil {
		t.Fatal(err)
	}
	if err := k.file.Set(keys.OwnerAccount("mai-dev"), "mai1:owner:devsecret"); err != nil {
		t.Fatal(err)
	}
	// A command of mai moves the owner key of mai to the key file.
	var out, errOut bytes.Buffer
	if code := runAgent([]string{"list"}, &out, &errOut); code != 0 {
		t.Fatalf("mai agent list: exit %d, stderr %q", code, errOut.String())
	}

	dirs, err := paths.Default()
	if err != nil {
		t.Fatal(err)
	}
	var uout bytes.Buffer
	u := &uninstall.Uninstaller{Engine: emptyEngine{}, Keys: k.file, Dirs: dirs, Instance: "mai-dev", Yes: true, Out: &uout, BaseURL: closedURL}
	if code := u.Run(context.Background()); code != 0 {
		t.Fatalf("mai uninstall --instance mai-dev: exit %d\n%s", code, uout.String())
	}
	if ok, _ := k.legacy.Has(keys.AgentAccount("mai", "cursor")); !ok {
		t.Fatal("the uninstall of mai-dev removed the keychain item of cursor of the instance mai")
	}

	// mai doctor and mai upgrade of the instance mai must still find the keychain item of cursor.
	_, bad := doctorRun(t)
	if !strings.Contains(strings.Join(bad, "\n"), "agent:cursor@mai") {
		t.Errorf("after the uninstall of mai-dev, mai doctor does not tell about the keychain item agent:cursor@mai: bad %v", bad)
	}
	var mout, merr bytes.Buffer
	oldOut, oldErr := lcOut, lcErr
	lcOut, lcErr = &mout, &merr
	t.Cleanup(func() { lcOut, lcErr = oldOut, oldErr })
	m, err := manifest.Load(dirs.State+"/"+manifest.FileName, "mai")
	if err != nil {
		t.Fatal(err)
	}
	moved := lcMoveKeys("mai", configPort(t), m)
	stays, _ := k.legacy.Has(keys.AgentAccount("mai", "cursor"))
	if moved && stays {
		t.Fatalf("mai upgrade of mai gives success, but the keychain item agent:cursor@mai stays:\n%s%s", mout.String(), merr.String())
	}
}

// After "mai uninstall --instance mai-dev", "mai uninstall" of mai must still remove the
// keychain item of cursor of mai, or report it and give the exit code 1.
func TestTheUninstallOfADifferentInstanceKeepsTheRemoveOfTheKeychainItems(t *testing.T) {
	_, _, k, home := legacyInstall(t, map[string]string{
		keys.OwnerAccount("mai"):           testOwnerKey,
		keys.AgentAccount("mai", "cursor"): legacyCursorKey,
	})
	if err := loadTestManifest(t, home).Add(manifest.Item{Type: manifest.Container, Name: "mai-dev", ID: "c-dev"}); err != nil {
		t.Fatal(err)
	}
	if err := k.file.Set(keys.OwnerAccount("mai-dev"), "mai1:owner:devsecret"); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := runAgent([]string{"list"}, &out, &errOut); code != 0 {
		t.Fatalf("mai agent list: exit %d, stderr %q", code, errOut.String())
	}
	dirs, err := paths.Default()
	if err != nil {
		t.Fatal(err)
	}
	var dev bytes.Buffer
	u := &uninstall.Uninstaller{Engine: emptyEngine{}, Keys: k.file, Dirs: dirs, Instance: "mai-dev", Yes: true, Out: &dev, BaseURL: closedURL}
	if code := u.Run(context.Background()); code != 0 {
		t.Fatalf("mai uninstall --instance mai-dev: exit %d\n%s", code, dev.String())
	}
	var mai bytes.Buffer
	u = &uninstall.Uninstaller{Engine: emptyEngine{}, Keys: k.file, Dirs: dirs, Instance: "mai", Yes: true, Out: &mai, BaseURL: closedURL}
	code := u.Run(context.Background())
	stays, _ := k.legacy.Has(keys.AgentAccount("mai", "cursor"))
	if stays && (code == 0 || !strings.Contains(mai.String(), "agent:cursor@mai")) {
		t.Fatalf("mai uninstall of mai gives exit %d and does not report the keychain item agent:cursor@mai that stays:\n%s", code, mai.String())
	}
}
