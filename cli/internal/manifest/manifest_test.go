package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mai 3.0 and 3.1 wrote one manifest for all instances of a state folder, with no instance in
// an item. The name of a container, a volume, or a keychain account tells its instance. An app
// entry stays with no instance, because it names no instance. Each other item is of the
// instance of the file.
func TestLoadGivesEachItemOfMai31AnInstance(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	old := `{"instance": "mai", "items": [
		{"type": "container", "name": "mai", "id": "c1"},
		{"type": "volume", "name": "mai3-data"},
		{"type": "container", "name": "mai-dev", "id": "c2"},
		{"type": "volume", "name": "mai-dev3-spool"},
		{"type": "keychain", "name": "agent:cursor@mai-dev"},
		{"type": "keychain", "name": "owner@mai"},
		{"type": "image", "name": "ghcr.io/monorithm/mai:3.1.1", "id": "sha256:a"},
		{"type": "app-entry", "name": "cursor", "path": "/x/mcp.json", "pointer": "/mcpServers/mai"}
	]}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := Load(path, "mai-dev")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"container mai": "mai", "volume mai3-data": "mai", "container mai-dev": "mai-dev",
		"volume mai-dev3-spool": "mai-dev", "keychain agent:cursor@mai-dev": "mai-dev", "keychain owner@mai": "mai",
		"image ghcr.io/monorithm/mai:3.1.1": "mai", "app-entry cursor": "",
	}
	for _, it := range m.Items {
		if got := it.Instance; got != want[it.Type+" "+it.Name] {
			t.Errorf("the instance of %s %s is %q, want %q", it.Type, it.Name, got, want[it.Type+" "+it.Name])
		}
	}
	if got := strings.Join(m.Instances(), ","); got != "mai,mai-dev" {
		t.Errorf("Instances = %s", got)
	}
	if got := m.OfInstance("mai-dev", Container); len(got) != 1 || got[0].Name != "mai-dev" {
		t.Errorf("the containers of mai-dev are %v", got)
	}
	if got := m.OfInstance("mai-dev", Image); len(got) != 0 {
		t.Errorf("the images of mai-dev are %v", got)
	}
}

// Add records an item for the instance of its name, else for the instance of the command. A
// new record of the same item replaces the record and its instance.
func TestAddRecordsTheInstanceOfEachItem(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	a, err := Load(path, "mai")
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range []Item{
		{Type: Container, Name: "mai-dev", ID: "c2"},
		{Type: Folder, Name: "state", Path: "/s"},
		{Type: Image, Name: "img", ID: "sha256:b"},
	} {
		if err := a.Add(it); err != nil {
			t.Fatal(err)
		}
	}
	b, err := Load(path, "mai-dev")
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Add(Item{Type: Folder, Name: "state", Path: "/s"}); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path, "mai")
	if err != nil {
		t.Fatal(err)
	}
	if got := c.OfInstance("mai-dev", Container); len(got) != 1 {
		t.Errorf("the containers of mai-dev are %v", got)
	}
	if got := c.OfInstance("mai-dev", Folder); len(got) != 1 || len(c.Of(Folder)) != 1 {
		t.Errorf("the folders of mai-dev are %v, all folders %v", got, c.Of(Folder))
	}
	if got := c.OfInstance("mai", Image); len(got) != 1 {
		t.Errorf("the images of mai are %v", got)
	}
	if c.Instance != "mai" {
		t.Errorf("the instance of the file is %q", c.Instance)
	}
}

// The instance of a manifest of mai 3.0 or 3.1 is the instance of the first command that wrote
// it. The record of a data folder can be of a different instance, thus Load gives it no
// instance. A new record of a data folder gets the instance of the command.
func TestTheDataFolderOfA31RecordHasNoInstance(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	raw := `{"instance":"mai","items":[{"type":"folder","name":"data","path":"/d"},{"type":"folder","name":"state","path":"/s"}]}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := Load(path, "mai")
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Unassigned(); len(got) != 1 || got[0].Path != "/d" {
		t.Fatalf("the records with no instance are %+v", got)
	}
	if got := m.OfInstance("mai", Folder); len(got) != 1 || got[0].Name != "state" {
		t.Fatalf("the folders of mai are %+v", got)
	}
	m.Assign(Item{Type: Folder, Name: DataFolder, Path: "/d"}, "mai-dev")
	if got := m.OfInstance("mai-dev", Folder); len(got) != 1 || got[0].Path != "/d" || len(m.Unassigned()) != 0 {
		t.Fatalf("after Assign, the folders of mai-dev are %+v", got)
	}
	n, err := Load(filepath.Join(t.TempDir(), FileName), "mai-dev")
	if err != nil {
		t.Fatal(err)
	}
	if err := n.Add(Item{Type: Folder, Name: DataFolder, Path: "/e"}); err != nil {
		t.Fatal(err)
	}
	if got := n.OfInstance("mai-dev", Folder); len(got) != 1 {
		t.Fatalf("a new data folder record of mai-dev has the instance %+v", n.Items)
	}
}
