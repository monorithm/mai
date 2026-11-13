package uninstall

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/keys"
)

// config.json names the instance mai-dev, which has keys, and the uninstall of the instance mai
// keeps a recorded keychain item, because no keychain is available. config.json stays, thus
// "mai uninstall" with no --instance acts on mai-dev. The step for the item that stays must
// name the instance mai.
func TestTheStepOfAKeychainItemThatStaysNamesTheInstanceWhenConfigNamesADifferentInstance(t *testing.T) {
	e := setup(t)
	addInstance(t, e, "mai-dev")
	if err := os.WriteFile(filepath.Join(e.dirs.Config, ConfigFile), []byte(`{"instance":"mai-dev","port":8766}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ds := &downStore{Memory: e.kc}
	ds.down.Store(true)
	e.keys = keys.Open(e.dirs.State, ds)
	var out bytes.Buffer
	u := e.uninstaller(&out)
	u.Instance = "mai"
	if code := u.Run(context.Background()); code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, out.String())
	}
	text := out.String()
	if _, err := os.Stat(filepath.Join(e.dirs.Config, ConfigFile)); err != nil {
		t.Fatalf("config.json of mai-dev is gone: %v", err)
	}
	// The step that the output gives, with no --instance, plans the remove of mai-dev.
	next := &Uninstaller{Engine: e.eng, Keys: e.keys, Dirs: e.dirs}
	p, err := next.MakePlan()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "run mai uninstall again") && p.Instance != "mai" {
		t.Errorf("the step is \"run mai uninstall again\", but that command removes the instance %s:\n%s", p.Instance, text)
	}
	if !strings.Contains(text, "mai uninstall --instance mai") {
		t.Errorf("no step names --instance mai:\n%s", text)
	}
}
