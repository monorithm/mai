package connect

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/manifest"
)

// testBin is the path of mai that the entries of the tests name.
const testBin = "/opt/mai/bin/mai"

// useBinary makes Prepare write path as the mai binary until the end of the test.
func useBinary(t *testing.T, path string) {
	t.Helper()
	old := Binary
	Binary = func() (string, error) { return path, nil }
	t.Cleanup(func() { Binary = old })
}

// setup makes a temp HOME and MAI_HOME, so that no test touches a real file of an app.
func setup(t *testing.T) (home string, m *manifest.Manifest) {
	t.Helper()
	useBinary(t, testBin)
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("AppData", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("MAI_HOME", filepath.Join(home, "maihome"))
	m, err := manifest.Load(filepath.Join(home, "maihome", "state", manifest.FileName), "mai")
	if err != nil {
		t.Fatal(err)
	}
	return home, m
}

// originals are user files that exist before mai connect, in the canonical form of each
// indent. A file that is not in the list does not exist before mai connect.
func originals(home string) map[string]string {
	return map[string]string{
		filepath.Join(home, ".claude.json"):                    "{\n  \"numStartups\": 4,\n  \"mcpServers\": {\n    \"other\": {\n      \"command\": \"x\"\n    }\n  },\n  \"zeta\": \"<&>\"\n}\n",
		filepath.Join(home, ".claude", "settings.json"):        "{\n    \"model\": \"opus\",\n    \"hooks\": {\n        \"Stop\": [\n            {\n                \"hooks\": [\n                    {\n                        \"type\": \"command\",\n                        \"command\": \"other\"\n                    }\n                ]\n            }\n        ]\n    }\n}\n",
		filepath.Join(home, ".cursor", "hooks.json"):           "{\n\t\"version\": 1,\n\t\"hooks\": {}\n}",
		filepath.Join(home, ".codex", "config.toml"):           "model = \"gpt-5\"\n\n[mcp_servers.other]\ncommand = \"x\"",
		filepath.Join(home, ".codex", "hooks.json"):            `{"hooks":{"Stop":[]},"a":1.50}`,
		filepath.Join(home, ".gemini", "config", "hooks.json"): "{\n  \"other\": {}\n}\n",
	}
}

func writeAll(t *testing.T, files map[string]string) {
	t.Helper()
	for p, s := range files {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConnectThenDisconnectRestoresEachFile(t *testing.T) {
	for _, app := range Apps {
		t.Run(app, func(t *testing.T) {
			home, m := setup(t)
			orig := originals(home)
			writeAll(t, orig)
			p, err := Prepare(home, app, "cursor")
			if err != nil {
				t.Fatal(err)
			}
			if !p.Changed() || p.Diff() == "" {
				t.Fatal("the plan has no change")
			}
			if err := p.Apply(m); err != nil {
				t.Fatal(err)
			}
			for _, f := range p.Files {
				b, err := os.ReadFile(f.Path)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(b), "mai") {
					t.Fatalf("%s has no mai entry:\n%s", f.Path, b)
				}
				if !strings.Contains(string(b), `"cursor"`) && !strings.Contains(string(b), "mai hook --format "+app) {
					t.Fatalf("%s has no entry of the app:\n%s", f.Path, b)
				}
			}
			if len(m.Of(manifest.AppEntry)) == 0 {
				t.Fatal("the manifest has no app entry")
			}
			again, err := Prepare(home, app, "cursor")
			if err != nil {
				t.Fatal(err)
			}
			if again.Changed() {
				t.Fatalf("a second connect changes files:\n%s", again.Diff())
			}
			if err := Disconnect(app, m); err != nil {
				t.Fatal(err)
			}
			for _, f := range p.Files {
				b, err := os.ReadFile(f.Path)
				want, existed := orig[f.Path]
				if !existed {
					if !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("%s stays after disconnect: %q", f.Path, b)
					}
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				if string(b) != want {
					t.Fatalf("%s is different after disconnect:\n%s", f.Path, unifiedDiff("want", "got", want, string(b)))
				}
			}
			if n := len(m.Of(manifest.AppEntry)) + len(m.Of(manifest.File)); n != 0 {
				t.Fatalf("the manifest keeps %d records", n)
			}
		})
	}
}

func TestChangedEntryGivesErrChanged(t *testing.T) {
	home, m := setup(t)
	p, err := Prepare(home, "cursor", "cursor")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Apply(m); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".cursor", "mcp.json")
	b, _ := os.ReadFile(path)
	changed := strings.Replace(string(b), `"--agent"`, `"--agent", "--debug"`, 1)
	if err := os.WriteFile(path, []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	var it manifest.Item
	for _, x := range m.Of(manifest.AppEntry) {
		if x.Path == path {
			it = x
		}
	}
	err = RemoveEntry(it)
	var ce *ErrChanged
	if !errors.As(err, &ce) {
		t.Fatalf("got %v", err)
	}
	if !strings.Contains(ce.Diff, "+") || !strings.Contains(ce.Diff, "--debug") {
		t.Fatalf("the diff does not show the change:\n%s", ce.Diff)
	}
	after, _ := os.ReadFile(path)
	if string(after) != changed {
		t.Fatal("RemoveEntry changed the file after ErrChanged")
	}
	if err := ForceRemoveEntry(it); err != nil {
		t.Fatal(err)
	}
	after, _ = os.ReadFile(path)
	if strings.Contains(string(after), "mai") {
		t.Fatalf("the forced remove keeps the entry:\n%s", after)
	}
}

func TestChangedHookAndTOMLGiveErrChanged(t *testing.T) {
	home, m := setup(t)
	p, err := Prepare(home, "codex", "a")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Apply(m); err != nil {
		t.Fatal(err)
	}
	toml := filepath.Join(home, ".codex", "config.toml")
	b, _ := os.ReadFile(toml)
	_ = os.WriteFile(toml, []byte(strings.Replace(string(b), `"a"]`, `"b"]`, 1)), 0o600)
	hooks := filepath.Join(home, ".codex", "hooks.json")
	b, _ = os.ReadFile(hooks)
	_ = os.WriteFile(hooks, []byte(strings.Replace(string(b), `"timeout": 10`, `"timeout": 20`, 1)), 0o600)
	n := 0
	for _, it := range m.Of(manifest.AppEntry) {
		var ce *ErrChanged
		if err := RemoveEntry(it); errors.As(err, &ce) {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("got %d ErrChanged, want 2", n)
	}
	if err := Disconnect("codex", m); err == nil {
		t.Fatal("disconnect gives no error for changed entries")
	}
}

func TestExistingOtherEntryGivesErrExists(t *testing.T) {
	home, _ := setup(t)
	writeAll(t, map[string]string{
		filepath.Join(home, ".cursor", "mcp.json"): `{"mcpServers":{"mai":{"command":"old"}}}`,
	})
	if _, err := Prepare(home, "cursor", "cursor"); !errors.Is(err, ErrExists) {
		t.Fatalf("got %v", err)
	}
	if _, err := Prepare(home, "nope", "cursor"); err == nil {
		t.Fatal("an unknown app gives no error")
	}
}

func TestUnifiedDiff(t *testing.T) {
	d := unifiedDiff("a", "b", "1\n2\n3\n", "1\n2\nx\n3\n")
	want := "--- a\n+++ b\n@@ -1,3 +1,4 @@\n 1\n 2\n+x\n 3\n"
	if d != want {
		t.Fatalf("got\n%s", d)
	}
}
