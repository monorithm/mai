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

// The state folder has one instance, mai. A container of a different state folder, for
// example of a CLI with MAI_HOME, has the labels of the CLI and of the instance mai-wf-x,
// and it uses the image that mai pulled. The state folder has no key and no record of
// mai-wf-x. The uninstall of mai is the uninstall of the last instance of the state folder.
// Thus no record of mai-wf-x must stay, and the folders of the CLI must go.
func TestTheUninstallOfTheOnlyInstanceKeepsNoRecordOfAContainerOfADifferentStateFolder(t *testing.T) {
	e := setup(t)
	e.eng.containers = append(e.eng.containers,
		fakeContainer{ID: "c-x", Name: "mai-wf-x", ImageID: "sha256:img3", Labels: managed("mai-wf-x")})
	var out bytes.Buffer
	if code := e.uninstaller(&out).Run(context.Background()); code != 0 {
		t.Fatalf("mai uninstall: exit %d:\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "kept     image sha256:img3: a different container uses it: /mai-wf-x") {
		t.Errorf("the report does not tell that the image that the container mai-wf-x uses stays:\n%s", out.String())
	}
	path := filepath.Join(e.dirs.State, manifest.FileName)
	if _, err := os.Stat(path); err == nil {
		m, err := manifest.Load(path, "mai")
		if err != nil {
			t.Fatal(err)
		}
		if got := m.Instances(); len(got) > 0 {
			t.Errorf("after the uninstall of the only instance of the state folder, the manifest stays with the records of the instances %v:\n%s",
				got, out.String())
		}
	}
	if _, err := os.Stat(e.dirs.Config); err == nil {
		t.Errorf("the configuration folder %s stays after the uninstall of the only instance:\n%s", e.dirs.Config, out.String())
	}

	// A new install of mai in the same folders, and then its uninstall. The uninstall must
	// not keep the files of the CLI for mai-wf-x, and must not give a step that removes the
	// container and the volumes of mai-wf-x.
	for _, d := range []string{e.dirs.Config, e.dirs.State, e.dirs.Cache} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(e.dirs.Config, ConfigFile), []byte(`{"instance":"mai","port":8765}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Load(path, "mai")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Add(manifest.Item{Type: manifest.Container, Name: "mai", ID: "c1"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Add(manifest.Item{Type: manifest.AppEntry, Name: "cursor", Path: e.app, Pointer: "/mcpServers/mai"}); err != nil {
		t.Fatal(err)
	}
	e.eng.containers = append(e.eng.containers, fakeContainer{ID: "c1", Name: "mai", ImageID: "sha256:img3", Labels: managed("mai")})
	_ = os.WriteFile(e.bin, []byte("binary"), 0o700)
	out.Reset()
	RemoveAppEntry = func(manifest.Item) error { return nil }
	t.Cleanup(func() { RemoveAppEntry = nil })
	_ = e.uninstaller(&out).Run(context.Background())
	if strings.Contains(out.String(), "mai uninstall --instance mai-wf-x") || strings.Contains(out.String(), "The instances mai-wf-x have keys or records") {
		t.Errorf("the uninstall of a new install of mai keeps the files of the CLI for mai-wf-x, an instance of a different state folder:\n%s", out.String())
	}
	// When the report gives a step for mai-wf-x, the user can do the uninstall of mai-wf-x
	// from this state folder. That step removes the container and the volumes of mai-wf-x,
	// which are of a different state folder.
	if !strings.Contains(out.String(), "--instance mai-wf-x") {
		return
	}
	e.eng.volumes[DataVolume("mai-wf-x")] = managed("mai-wf-x")
	out.Reset()
	u := e.uninstaller(&out)
	u.Instance = "mai-wf-x"
	_ = u.Run(context.Background())
	if e.eng.findContainer("mai-wf-x") < 0 {
		t.Errorf("the step of the report removed the container of mai-wf-x, an instance of a different state folder:\n%s", out.String())
	}
	if _, ok := e.eng.volumes[DataVolume("mai-wf-x")]; !ok {
		t.Errorf("the step of the report removed the volume %s of a different state folder", DataVolume("mai-wf-x"))
	}
}
