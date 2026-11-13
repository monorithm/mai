package uninstall

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/manifest"
)

// dataFolder makes a data folder with a file of PostgreSQL in it.
func dataFolder(t *testing.T) string {
	t.Helper()
	data := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(filepath.Join(data, "pg"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "pg", "PG_VERSION"), []byte("16\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return data
}

// jsonText gives s as a JSON string, with its quotes. A path of Windows has backslashes,
// which a JSON string must escape.
func jsonText(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// writeManifest writes the manifest of the state folder as it is.
func writeManifest(t *testing.T, e *env, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(e.dirs.State, manifest.FileName), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// In a manifest of mai 3.0 or 3.1, the record of a data folder has no instance. The container
// that mounts the folder at /data tells its instance, also when config.json does not.
func TestTheContainerThatMountsADataFolderOf31TellsItsInstance(t *testing.T) {
	e := setup(t)
	addInstance(t, e, "mai-dev")
	data := dataFolder(t)
	e.eng.containers[e.eng.findContainer("mai-dev")].Data = data
	writeManifest(t, e, `{"instance": "mai", "items": [
  {"type": "container", "name": "mai", "id": "c1"},
  {"type": "volume", "name": "mai-dev3-spool"},
  {"type": "folder", "name": "data", "path": `+jsonText(data)+`},
  {"type": "container", "name": "mai-dev", "id": "c-mai-dev"}]}`)

	var out bytes.Buffer
	u := e.uninstaller(&out)
	u.Instance = "mai"
	_ = u.Run(context.Background())
	if _, err := os.Stat(filepath.Join(data, "pg", "PG_VERSION")); err != nil || strings.Contains(out.String(), "folder     "+data) {
		t.Fatalf("mai uninstall --instance mai named or removed the data folder of mai-dev: %v\n%s", err, out.String())
	}

	out.Reset()
	u = e.uninstaller(&out)
	u.Instance = "mai-dev"
	_ = u.Run(context.Background())
	if _, err := os.Stat(data); err == nil || !strings.Contains(out.String(), "gone     folder "+data+"\n") {
		t.Fatalf("mai uninstall --instance mai-dev did not remove its data folder:\n%s", out.String())
	}
	m, err := manifest.Load(filepath.Join(e.dirs.State, manifest.FileName), "mai")
	if err == nil {
		for _, it := range m.Of(manifest.Folder) {
			if it.Path == data {
				t.Errorf("the manifest keeps the record of the data folder that the uninstall removed: %+v", it)
			}
		}
	}
}

// When nothing tells the instance of a data folder of mai 3.0 or 3.1, the folder stays, and
// the plan and the report tell this.
func TestADataFolderOf31WithNoKnownInstanceStays(t *testing.T) {
	e := setup(t)
	data := dataFolder(t)
	writeManifest(t, e, `{"instance": "mai", "items": [
  {"type": "container", "name": "mai", "id": "c1"},
  {"type": "folder", "name": "data", "path": `+jsonText(data)+`}]}`)
	var out bytes.Buffer
	_ = e.uninstaller(&out).Run(context.Background())
	if _, err := os.Stat(filepath.Join(data, "pg", "PG_VERSION")); err != nil {
		t.Fatalf("the uninstall removed a data folder with no known instance: %v\n%s", err, out.String())
	}
	for _, want := range []string{"The data folder " + data + " stays.", "kept     folder " + data + ": "} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the output has no %q:\n%s", want, out.String())
		}
	}
}

// A record of the data folder can name the wrong instance, for example after a start of mai
// 3.1 with the folder of a different instance. The uninstall never removes a folder that a
// container of a different instance mounts.
func TestUninstallKeepsAFolderThatAContainerOfADifferentInstanceMounts(t *testing.T) {
	e := setup(t)
	addInstance(t, e, "mai-dev")
	data := dataFolder(t)
	e.eng.containers[e.eng.findContainer("mai-dev")].Data = data
	m, err := manifest.Load(filepath.Join(e.dirs.State, manifest.FileName), "mai")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Add(manifest.Item{Type: manifest.Folder, Name: manifest.DataFolder, Path: data, Instance: "mai"}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	u := e.uninstaller(&out)
	u.Instance = "mai"
	_ = u.Run(context.Background())
	if _, err := os.Stat(filepath.Join(data, "pg", "PG_VERSION")); err != nil {
		t.Fatalf("mai uninstall --instance mai removed the folder that the container of mai-dev mounts: %v\n%s", err, out.String())
	}
	for _, want := range []string{
		"The folder " + data + " stays, because the container mai-dev of the instance mai-dev mounts the folder",
		"kept     folder " + data + ": the container mai-dev of the instance mai-dev mounts the folder",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the output has no %q:\n%s", want, out.String())
		}
	}
	// The plan does not list the folder, and the size does not count it.
	if strings.Contains(out.String(), "  folder     "+data+"\n") {
		t.Errorf("the plan lists the folder that the container of mai-dev mounts:\n%s", out.String())
	}
}
