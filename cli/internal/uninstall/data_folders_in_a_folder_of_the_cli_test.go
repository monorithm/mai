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

// The record of a data folder of mai 3.0 or 3.1 has no instance, and no container and no
// config.json tell its instance. The uninstall tells that the folder stays. The folder is in
// the configuration folder of the CLI, for example ~/Library/Application Support/mai/data on
// macOS. The remove of the configuration folder must then keep it.
func TestADataFolderOf31WithNoKnownInstanceStaysInAFolderOfTheCLI(t *testing.T) {
	e := setup(t)
	data := filepath.Join(e.dirs.Config, "data-of-mai-dev")
	if err := os.MkdirAll(filepath.Join(data, "pg"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "pg", "PG_VERSION"), []byte("16\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeManifest(t, e, `{"instance": "mai", "items": [
  {"type": "container", "name": "mai", "id": "c1"},
  {"type": "folder", "name": "data", "path": "`+data+`"}]}`)
	var out bytes.Buffer
	_ = e.uninstaller(&out).Run(context.Background())
	if !strings.Contains(out.String(), "kept     folder "+data) {
		t.Fatalf("the test needs the report that the folder stays:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(data, "pg", "PG_VERSION")); err != nil {
		t.Errorf("the uninstall tells that the data folder %s stays, but the folder is gone: %v\n%s", data, err, out.String())
	}
}

// The manifest records the data folder of mai-dev, with the instance mai-dev, in the
// configuration folder of the CLI. No container mounts it at this time, for example after a
// "mai upgrade --instance mai-dev" that removed the old container and then stopped with an
// error. The uninstall of mai must keep the data folder of mai-dev.
func TestTheUninstallOfMaiKeepsTheRecordedDataFolderOfADifferentInstanceInAFolderOfTheCLI(t *testing.T) {
	e := setup(t)
	data := filepath.Join(e.dirs.Config, "data-of-mai-dev")
	if err := os.MkdirAll(filepath.Join(data, "pg"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "pg", "PG_VERSION"), []byte("16\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := e.keys.Set(keys.OwnerAccount("mai-dev"), "mai1:owner:dev"); err != nil {
		t.Fatal(err)
	}
	writeManifest(t, e, `{"instance": "mai", "items": [
  {"type": "container", "name": "mai", "id": "c1", "instance": "mai"},
  {"type": "volume", "name": "mai-dev3-spool", "instance": "mai-dev"},
  {"type": "folder", "name": "data", "path": "`+data+`", "instance": "mai-dev"}]}`)
	var out bytes.Buffer
	u := e.uninstaller(&out)
	u.Instance = "mai"
	_ = u.Run(context.Background())
	if _, err := os.Stat(filepath.Join(data, "pg", "PG_VERSION")); err != nil {
		t.Errorf("mai uninstall --instance mai removed the data folder %s of mai-dev: %v\n%s", data, err, out.String())
	}
}
