package uninstall

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/keys"
	"github.com/monorithm/mai/cli/v3/internal/manifest311"
	"github.com/monorithm/mai/cli/v3/internal/paths"
)

// A manifest of mai 3.1.1 holds the instance of the first command that wrote it, here
// "mai start --instance mai-dev". Then "mai start" and "mai connect cursor" wrote the
// records of mai. A record of 3.1.1 has no instance, and the app entry has no instance in its
// name. The uninstall of mai must report the app entry that mai connect wrote for mai: it
// removes it, or it tells that it stays.
func TestTheUninstallOfMaiReportsItsAppEntryInAManifestOf311OfADifferentInstance(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MAI_HOME", home)
	dirs, err := paths.Default()
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{dirs.Config, dirs.State, dirs.Cache} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dirs.Config, ConfigFile), []byte(`{"instance":"mai"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	app := filepath.Join(home, "app.json")
	m, err := manifest311.Load(filepath.Join(dirs.State, manifest311.FileName), "mai-dev")
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range []manifest311.Item{
		{Type: manifest311.Folder, Name: "config", Path: dirs.Config},
		{Type: manifest311.Folder, Name: "state", Path: dirs.State},
		{Type: manifest311.Volume, Name: "mai-dev3-data"},
		{Type: manifest311.Volume, Name: "mai-dev3-spool"},
		{Type: manifest311.Container, Name: "mai-dev", ID: "c-mai-dev"},
		{Type: manifest311.KeychainKey, Name: "owner@mai-dev"},
		{Type: manifest311.Volume, Name: "mai3-data"},
		{Type: manifest311.Volume, Name: "mai3-spool"},
		{Type: manifest311.Container, Name: "mai", ID: "c1"},
		{Type: manifest311.KeychainKey, Name: "owner@mai"},
		{Type: manifest311.AppEntry, Name: "cursor", Path: app, Pointer: "/mcpServers/mai"},
	} {
		if err := m.Add(it); err != nil {
			t.Fatal(err)
		}
	}
	eng := newFakeEngine()
	eng.containers = []fakeContainer{
		{ID: "c1", Name: "mai", ImageID: "sha256:img3", Labels: managed("mai")},
		{ID: "c-mai-dev", Name: "mai-dev", ImageID: "sha256:img3", Labels: managed("mai-dev")},
	}
	for _, v := range []string{"mai3-data", "mai3-spool"} {
		eng.volumes[v] = managed("mai")
	}
	for _, v := range []string{"mai-dev3-data", "mai-dev3-spool"} {
		eng.volumes[v] = managed("mai-dev")
	}
	kf := keys.Open(dirs.State, nil)
	for _, a := range []string{"owner@mai", "owner@mai-dev"} {
		if err := kf.Set(a, "mai1:owner:"+a); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	u := &Uninstaller{Engine: eng, Keys: kf, Dirs: dirs, Yes: true, Out: &out, BaseURL: "http://127.0.0.1:1"}
	if code := u.Run(context.Background()); code != 0 {
		t.Fatalf("mai uninstall: exit %d:\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "app entry cursor in "+app) {
		t.Errorf("the uninstall of mai does not tell about the app entry cursor in %s that mai connect wrote for mai:\n%s", app, out.String())
	}
}
