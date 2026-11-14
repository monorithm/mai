package uninstall

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelectContainerNeedsTheComposeLabel(t *testing.T) {
	e := newFakeEngine()
	e.containers = []fakeContainer{{ID: "c1", Name: "mai", Labels: map[string]string{"other": "x"}}}
	id, err := SelectContainer(context.Background(), e)
	if err != nil || id != "" {
		t.Fatalf("selected %q (%v) with no compose label", id, err)
	}
}

func TestSelectContainerNeverSelectsAManagedContainer(t *testing.T) {
	e := newFakeEngine()
	labels := managed("mai")
	labels[LabelCompose] = LegacyProject
	e.containers = []fakeContainer{{ID: "c1", Name: "mai", Labels: labels}}
	id, err := SelectContainer(context.Background(), e)
	if err != nil || id != "" {
		t.Fatalf("selected the managed container %q (%v)", id, err)
	}
}

func TestSelectContainerNeedsTheNameMai(t *testing.T) {
	e := newFakeEngine()
	e.containers = []fakeContainer{{ID: "c2", Name: "mai-old", Labels: map[string]string{LabelCompose: LegacyProject}}}
	if id, _ := SelectContainer(context.Background(), e); id != "" {
		t.Fatalf("selected %q, a container with a different name", id)
	}
}

func TestSelectContainerOfCompose(t *testing.T) {
	e := newFakeEngine()
	e.containers = []fakeContainer{
		{ID: "c2", Name: "other", Labels: map[string]string{LabelCompose: LegacyProject}},
		{ID: "c1", Name: "mai", Labels: map[string]string{LabelCompose: LegacyProject}},
	}
	if id, _ := SelectContainer(context.Background(), e); id != "c1" {
		t.Fatalf("selected %q, want c1", id)
	}
}

func TestLegacyUninstall(t *testing.T) {
	home := t.TempDir()
	slashHome := filepath.ToSlash(home)
	if err := os.MkdirAll(filepath.Join(home, ".mai", "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(home, ".mai", "compose.yaml"), []byte("services: {}\n"), 0o600)
	cursor := filepath.Join(home, ".cursor", "mcp.json")
	_ = os.MkdirAll(filepath.Dir(cursor), 0o700)
	entry := `{"mcpServers":{"mai":{"command":"` + slashHome + `/.mai/bin/mai","args":["mcp"]},"x":{"command":"npx"}}}`
	_ = os.WriteFile(cursor, []byte(entry), 0o600)
	codex := filepath.Join(home, ".codex", "config.toml")
	_ = os.MkdirAll(filepath.Dir(codex), 0o700)
	_ = os.WriteFile(codex, []byte("[mcp_servers.mai]\ncommand = \""+slashHome+"/.mai/bin/mai\"\n"), 0o600)

	e := newFakeEngine()
	e.containers = []fakeContainer{
		{ID: "c1", Name: "mai", ImageID: "sha256:old", Labels: map[string]string{LabelCompose: LegacyProject}},
		{ID: "c3", Name: "mai3", Labels: managed("mai3")},
	}
	e.volumes["mai_mai-data"] = nil
	e.volumes["mai_mai-spool"] = nil
	e.volumes["mai3-data"] = managed("mai")
	e.images = []fakeImage{
		{ID: "sha256:old", Tags: []string{"ghcr.io/monorithm/mai:2.1.0", "ghcr.io/monorithm/mai:2.0.1"}},
		{ID: "sha256:new", Tags: []string{"ghcr.io/monorithm/mai:3.0.0"}},
		{ID: "sha256:dev", Tags: []string{"mai:dev"}},
	}
	before, _ := os.ReadFile(cursor)

	var out bytes.Buffer
	l := &Legacy{Engine: e, Home: home, AppFiles: LegacyAppFiles(home, "linux"), Yes: true, Out: &out}
	if code := l.Run(context.Background()); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
	if e.findContainer("mai") >= 0 {
		t.Error("the 2.x container exists")
	}
	if e.findContainer("mai3") < 0 {
		t.Error("the legacy uninstall removed a managed container")
	}
	for _, v := range []string{"mai_mai-data", "mai_mai-spool"} {
		if _, ok := e.volumes[v]; ok {
			t.Errorf("the volume %s exists", v)
		}
	}
	if _, ok := e.volumes["mai3-data"]; !ok {
		t.Error("the legacy uninstall removed a 3.0 volume")
	}
	if e.findImage("sha256:old") >= 0 {
		t.Error("the 2.x image exists")
	}
	if e.findImage("ghcr.io/monorithm/mai:3.0.0") < 0 || e.findImage("mai:dev") < 0 {
		t.Error("the legacy uninstall removed an image that is not 2.x")
	}
	if _, err := os.Stat(filepath.Join(home, ".mai")); !os.IsNotExist(err) {
		t.Error("the folder .mai exists")
	}
	after, _ := os.ReadFile(cursor)
	if !bytes.Equal(before, after) {
		t.Error("the legacy uninstall changed a file of an app")
	}
	text := out.String()
	for _, want := range []string{cursor + " /mcpServers/mai", codex + " line 2", "gone     container mai", "gone     image ghcr.io/monorithm/mai:2.1.0"} {
		if !strings.Contains(text, want) {
			t.Errorf("the output has no %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "/mcpServers/x") {
		t.Errorf("the output lists an entry that is not 2.x:\n%s", text)
	}
}

func TestLegacyWantsTheTypedWord(t *testing.T) {
	e := newFakeEngine()
	e.containers = []fakeContainer{{ID: "c1", Name: "mai", Labels: map[string]string{LabelCompose: LegacyProject}}}
	var out bytes.Buffer
	l := &Legacy{Engine: e, Home: t.TempDir(), In: strings.NewReader("no\n"), Out: &out}
	if code := l.Run(context.Background()); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if len(e.removed) != 0 {
		t.Fatalf("removed %v with no agreement", e.removed)
	}
}

func TestLegacyFindsA2xEntryInTheMCPFileOfAntigravity(t *testing.T) {
	home := t.TempDir()
	file := filepath.Join(home, ".gemini", "config", "mcp_config.json")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	entry := `{"mcpServers":{"mai":{"command":"` + filepath.ToSlash(home) + `/.mai/bin/mai","args":["mcp"]},"user-server":{"command":"/usr/bin/true"}}}`
	if err := os.WriteFile(file, []byte(entry), 0o600); err != nil {
		t.Fatal(err)
	}
	var found []string
	for _, f := range LegacyAppFiles(home, "linux") {
		found = append(found, FindLegacyEntries(f)...)
	}
	if len(found) != 1 || found[0] != file+" /mcpServers/mai" {
		t.Fatalf("the legacy scan found %q, want the entry in %s", found, file)
	}
}
