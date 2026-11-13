package uninstall

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/manifest"
)

// The app entry that mai connect wrote for mai stays while mai-dev has keys or records, because
// the entry names no instance. Its record then is of mai-dev, the instance of config.json. Thus
// the uninstall of mai-dev, the last instance, removes the entry.
func TestTheUninstallOfTheLastInstanceRemovesTheAppEntryThatStayed(t *testing.T) {
	e := setup(t)
	addInstance(t, e, "mai-dev")
	if err := os.WriteFile(filepath.Join(e.dirs.Config, ConfigFile), []byte(`{"instance":"mai-dev","port":18766}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var removed []manifest.Item
	RemoveAppEntry = func(it manifest.Item) error { removed = append(removed, it); return nil }
	t.Cleanup(func() { RemoveAppEntry = nil })

	var out bytes.Buffer
	u := e.uninstaller(&out)
	u.Instance = "mai"
	if code := u.Run(context.Background()); code != 0 {
		t.Fatalf("mai uninstall --instance mai: exit %d\n%s", code, out.String())
	}
	if len(removed) != 0 {
		t.Fatalf("the uninstall of mai removed the app entry:\n%s", out.String())
	}
	for _, want := range []string{"the app entries of mai connect stay", "kept     app entry cursor in " + e.app + ": it stays, because the instances mai-dev have keys or records"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the output has no %q:\n%s", want, out.String())
		}
	}
	m, err := manifest.Load(filepath.Join(e.dirs.State, manifest.FileName), "mai-dev")
	if err != nil {
		t.Fatal(err)
	}
	if got := m.OfInstance("mai-dev", manifest.AppEntry); len(got) != 1 {
		t.Fatalf("after the uninstall of mai, the records of the app entries are %+v", m.Of(manifest.AppEntry))
	}

	out.Reset()
	u = e.uninstaller(&out)
	u.Instance = "mai-dev"
	if code := u.Run(context.Background()); code != 0 {
		t.Fatalf("mai uninstall --instance mai-dev: exit %d\n%s", code, out.String())
	}
	if len(removed) != 1 || removed[0].Name != "cursor" || !strings.Contains(out.String(), "gone     app entry cursor in "+e.app) {
		t.Fatalf("the uninstall of the last instance did not remove the app entry: %+v\n%s", removed, out.String())
	}
	if _, err := os.Stat(e.dirs.State); !os.IsNotExist(err) {
		t.Errorf("the state folder stays after the uninstall of the last instance: %v", err)
	}
}
