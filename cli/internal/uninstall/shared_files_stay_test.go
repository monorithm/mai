package uninstall

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/keys"
	"github.com/monorithm/mai/cli/v3/internal/manifest"
)

// namesAnObjectOfMai finds a line of the plan or of the report that names an object of the
// instance mai: its container, a volume, an image, its app entry, or a key.
var namesAnObjectOfMai = regexp.MustCompile(`(?m)(container\s+mai( |$)|mai3-data|mai3-spool|sha256:img3|sha256:shared|app entry|@mai( |\)|$))`)

// recordsOf counts the records of the instance.
func recordsOf(m *manifest.Manifest, instance string) int {
	n := 0
	for _, it := range m.Items {
		if it.Instance == instance {
			n++
		}
	}
	return n
}

// While a different instance has keys or records, the uninstall of one instance keeps the
// files of the CLI that the other instance uses. The plan and the report name no object of
// the other instance. The uninstall of the last instance then removes all.
func TestUninstallOfOneInstanceKeepsTheSharedFilesForADifferentInstance(t *testing.T) {
	e := setup(t)
	addInstance(t, e, "mai-dev")
	var seen []manifest.Item
	RemoveAppEntry = func(it manifest.Item) error { seen = append(seen, it); return nil }
	t.Cleanup(func() { RemoveAppEntry = nil })
	path := filepath.Join(e.dirs.State, manifest.FileName)
	before, err := manifest.Load(path, "mai")
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	u := e.uninstaller(&out)
	u.Instance = "mai-dev"
	if code := u.Run(context.Background()); code != 0 {
		t.Fatalf("mai uninstall --instance mai-dev: exit %d:\n%s", code, out.String())
	}
	text := out.String()
	if m := namesAnObjectOfMai.FindString(text); m != "" {
		t.Errorf("the plan or the report of mai-dev names %q, an object of the instance mai:\n%s", m, text)
	}
	if len(seen) != 0 {
		t.Errorf("the uninstall of mai-dev removed the app entries %v of mai", seen)
	}
	if e.eng.findContainer("mai-dev") >= 0 {
		t.Error("the container of mai-dev exists")
	}
	if e.eng.findImage("sha256:img3") < 0 {
		t.Error("the uninstall of mai-dev removed the image that mai pulled")
	}
	if _, err := os.Stat(e.bin); err != nil {
		t.Errorf("the uninstall of mai-dev removed the binary: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.dirs.Config, ConfigFile)); err != nil {
		t.Errorf("the uninstall of mai-dev removed the configuration file of mai: %v", err)
	}
	for _, want := range []string{
		"gone     container mai-dev", "gone     volume mai-dev3-data", "gone     key owner@mai-dev",
		"kept     binary " + e.bin + ": it stays, because the instances mai have keys or records",
		"it holds the records of the instances: mai",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the output has no %q:\n%s", want, text)
		}
	}
	after, err := manifest.Load(path, "mai")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := after.Instances(), []string{"mai"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("the manifest after the uninstall of mai-dev holds the records of %v, want %v", got, want)
	}
	if got, want := recordsOf(after, "mai"), recordsOf(before, "mai"); got != want || len(after.Items) != want {
		t.Errorf("the manifest holds %d records, %d of mai, after the uninstall of mai-dev, want %d of mai", len(after.Items), got, want)
	}

	out.Reset()
	u = e.uninstaller(&out)
	if code := u.Run(context.Background()); code != 0 {
		t.Fatalf("mai uninstall of the last instance: exit %d:\n%s", code, out.String())
	}
	if e.eng.findContainer("mai") >= 0 {
		t.Error("the container of mai exists")
	}
	if left := e.kc.Accounts(); len(left) != 0 {
		t.Errorf("the keychain items %v stay", left)
	}
	if all, err := e.keys.Accounts(); err == nil && len(all) != 0 {
		t.Errorf("the key file holds %v", all)
	}
	for _, p := range []string{e.dirs.Config, e.dirs.State, e.dirs.Cache, e.bin} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s exists after the uninstall of the last instance", p)
		}
	}
}

// A container or a volume goes only when its labels tell that it is of the instance, also
// when a record of the manifest names it.
func TestUninstallKeepsAContainerAndAVolumeWithTheLabelOfADifferentInstance(t *testing.T) {
	e := setup(t)
	e.eng.containers = append(e.eng.containers, fakeContainer{ID: "c7", Name: "mai-old", Labels: managed("mai-dev")})
	e.eng.volumes["mai-old3-data"] = managed("mai-dev")
	m, err := manifest.Load(filepath.Join(e.dirs.State, manifest.FileName), "mai")
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range []manifest.Item{
		{Type: manifest.Container, Name: "mai-old", ID: "c7", Instance: "mai"},
		{Type: manifest.Volume, Name: "mai-old3-data", Instance: "mai"},
	} {
		if err := m.Add(it); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	_ = e.uninstaller(&out).Run(context.Background())
	if e.eng.findContainer("mai-old") < 0 {
		t.Errorf("the uninstall of mai removed a container with the label of mai-dev:\n%s", out.String())
	}
	if _, ok := e.eng.volumes["mai-old3-data"]; !ok {
		t.Errorf("the uninstall of mai removed a volume with the label of mai-dev:\n%s", out.String())
	}
	for _, want := range []string{
		"kept     container mai-old: it has no label of the instance mai",
		"kept     volume mai-old3-data: it has no label of the instance mai",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the output has no %q:\n%s", want, out.String())
		}
	}
	if ok, _ := e.keys.Has(keys.OwnerAccount("mai")); ok {
		t.Error("the owner key of mai stays")
	}
}
