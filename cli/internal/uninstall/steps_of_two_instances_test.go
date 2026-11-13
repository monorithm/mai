package uninstall

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/keys"
	"github.com/monorithm/mai/cli/v3/internal/manifest"
)

// stepRun finds each "run mai uninstall ... again" step in the output.
var stepRun = regexp.MustCompile(`run (mai uninstall(?: --instance ([a-z0-9-]+))?) again`)

// config.json names mai-dev, and it stays, because mai-dev has keys. The uninstall of mai
// cannot remove the keychain items of mai, because no keychain is available. Each step must
// then name --instance mai. The step "mai uninstall" acts on mai-dev, the instance of
// config.json, and removes the container and the mail of mai-dev.
func TestEachStepOfAnUninstallOfMaiNamesTheInstanceWhenConfigNamesADifferentInstance(t *testing.T) {
	e := setup(t)
	addInstance(t, e, "mai-dev")
	if err := os.WriteFile(filepath.Join(e.dirs.Config, ConfigFile), []byte(`{"instance":"mai-dev","port":18766}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ds := &downStore{Memory: e.kc}
	ds.down.Store(true)
	e.keys = keys.Open(e.dirs.State, ds)
	var out bytes.Buffer
	u := e.uninstaller(&out)
	u.Instance = "mai"
	if code := u.Run(context.Background()); code != 1 {
		t.Fatalf("mai uninstall --instance mai: exit %d, want 1:\n%s", code, out.String())
	}
	cfg, err := LoadConfig(e.dirs.Config)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Instance != "mai-dev" {
		t.Fatalf("the test needs a config.json of mai-dev that stays, got %q", cfg.Instance)
	}
	steps := stepRun.FindAllStringSubmatch(out.String(), -1)
	if len(steps) == 0 {
		t.Fatalf("the output gives no step to run mai uninstall again:\n%s", out.String())
	}
	for _, s := range steps {
		if s[2] != "mai" {
			t.Errorf("the step %q does not name --instance mai, and config.json names mai-dev", s[0])
		}
	}

	// Do the first step as the output gives it, with the keychain available.
	ds.down.Store(false)
	first := steps[0]
	var again bytes.Buffer
	u = e.uninstaller(&again)
	u.Instance = first[2]
	_ = u.Run(context.Background())
	if e.eng.findContainer("mai-dev") < 0 {
		t.Errorf("the step %q removed the container of mai-dev:\n%s", first[1], again.String())
	}
	if _, ok := e.eng.volumes[DataVolume("mai-dev")]; !ok {
		t.Errorf("the step %q removed the data volume of mai-dev", first[1])
	}
	if left := e.kc.Accounts(); len(left) != 0 {
		t.Errorf("after the step %q, the keychain items %v of mai stay", first[1], left)
	}
}

// config.json names mai, and mai stays. The uninstall of mai-dev cannot remove the keychain
// item of mai-dev, because no keychain is available. The step of the binary that stays must
// then name --instance mai-dev, as the step of the keychain item does. The step
// "mai uninstall" acts on mai, the instance of config.json.
func TestTheStepOfTheBinaryThatStaysNamesTheInstanceOfTheUninstall(t *testing.T) {
	e := setup(t)
	addInstance(t, e, "mai-dev")
	e.kc.Put(keys.OwnerAccount("mai-dev"), "mai1:owner:legacy-of-dev")
	man, err := manifest.Load(filepath.Join(e.dirs.State, manifest.FileName), "mai")
	if err != nil {
		t.Fatal(err)
	}
	if err := man.Add(manifest.Item{Type: manifest.KeychainKey, Name: keys.OwnerAccount("mai-dev")}); err != nil {
		t.Fatal(err)
	}
	ds := &downStore{Memory: e.kc}
	ds.down.Store(true)
	e.keys = keys.Open(e.dirs.State, ds)
	m, err := LoadConfig(e.dirs.Config)
	if err != nil || m.Instance != "mai" {
		t.Fatalf("the test needs a config.json of mai: %+v %v", m, err)
	}
	var out bytes.Buffer
	u := e.uninstaller(&out)
	u.Instance = "mai-dev"
	if code := u.Run(context.Background()); code != 1 {
		t.Fatalf("mai uninstall --instance mai-dev: exit %d, want 1:\n%s", code, out.String())
	}
	steps := stepRun.FindAllStringSubmatch(out.String(), -1)
	if len(steps) == 0 {
		t.Fatalf("the output gives no step to run mai uninstall again:\n%s", out.String())
	}
	for _, s := range steps {
		if s[2] != "mai-dev" {
			t.Errorf("the step %q does not name --instance mai-dev, and config.json names mai", s[0])
		}
	}
}
