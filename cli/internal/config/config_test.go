package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/version"
)

func TestLoadMissingFileGivesDefaults(t *testing.T) {
	c, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if c != Defaults() {
		t.Fatalf("got %+v, want %+v", c, Defaults())
	}
	if c.Instance != "mai" || c.Port != 8765 || c.DataDir != "" {
		t.Fatalf("defaults are wrong: %+v", c)
	}
	if c.Image != "ghcr.io/monorithm/mai:"+version.Version {
		t.Fatalf("image is %q", c.Image)
	}
}

func TestLoadMissingFieldGivesDefault(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(`{"port":18765}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Port != 18765 || c.Instance != "mai" || c.Image != DefaultImage() {
		t.Fatalf("got %+v", c)
	}
}

func TestSaveThenLoad(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "config")
	want := Config{Instance: "x", Port: 1, Image: "img:1", DataDir: "/d"}
	if err := Save(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	st, err := os.Stat(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
		t.Fatalf("mode is %v", st.Mode().Perm())
	}
}

func TestApplyOverrides(t *testing.T) {
	c := Defaults().Apply(Overrides{Port: 9000, DataDir: "/x"})
	if c.Port != 9000 || c.DataDir != "/x" || c.Instance != "mai" {
		t.Fatalf("got %+v", c)
	}
	c = c.Apply(Overrides{})
	if c.Port != 9000 {
		t.Fatalf("an empty override changed the port: %+v", c)
	}
}

func TestUpdateKeepsDefaultsOutOfTheFile(t *testing.T) {
	dir := t.TempDir()
	if err := Update(dir, Overrides{Port: 18765}); err != nil {
		t.Fatal(err)
	}
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Port != 18765 || c.Image != DefaultImage() || c.Instance != "mai" {
		t.Fatalf("got %+v", c)
	}
	if err := PinImage(dir, "img:2"); err != nil {
		t.Fatal(err)
	}
	if c, _ = Load(dir); c.Image != "img:2" || c.Port != 18765 {
		t.Fatalf("after the pin: %+v", c)
	}
	if err := PinImage(dir, ""); err != nil {
		t.Fatal(err)
	}
	if c, _ = Load(dir); c.Image != DefaultImage() {
		t.Fatalf("after the unpin: %+v", c)
	}
}

func TestLoadBadJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(`{`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("no error for bad JSON")
	}
}

// The port, the image, and the data folder of config.json are of the instance that the file
// names. An update to a different instance must not give these values to that instance.
func TestUpdateToADifferentInstanceKeepsNoValueOfTheInstanceBefore(t *testing.T) {
	dir := t.TempDir()
	if err := Update(dir, Overrides{Instance: "b", Port: 18993, Image: "img:b", DataDir: "/data-of-b"}); err != nil {
		t.Fatal(err)
	}
	if err := Update(dir, Overrides{Instance: "a", Port: 18992}); err != nil {
		t.Fatal(err)
	}
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c != (Config{Instance: "a", Port: 18992, Image: DefaultImage()}) {
		t.Fatalf("after the update to the instance a, config.json gives %+v", c)
	}
	// An update of the same instance keeps the other fields.
	if err := Update(dir, Overrides{Instance: "a", DataDir: "/data-of-a"}); err != nil {
		t.Fatal(err)
	}
	if c, _ = Load(dir); c.Port != 18992 || c.DataDir != "/data-of-a" {
		t.Fatalf("after an update of the same instance, config.json gives %+v", c)
	}
}
