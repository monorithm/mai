package connect

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/manifest"
)

// The files of a user who has a different MCP server and its hooks, written by hand with
// inline arrays and objects.
const (
	userMCP = `{
  "mcpServers": {
    "other": {
      "command": "/opt/bridge/bin/other",
      "args": ["mcp"],
      "env": { "ADDRESS": "cursor" }
    }
  }
}
`
	userHooks = `{
  "version": 1,
  "hooks": {
    "beforeSubmitPrompt": [ { "command": "/opt/bridge/bin/other hook --event beforeSubmitPrompt", "timeout": 10 } ],
    "postToolUse": [ { "command": "/opt/bridge/bin/other hook --event postToolUse", "timeout": 10 } ],
    "stop": [ { "command": "/opt/bridge/bin/other hook --event stop", "timeout": 10, "loop_limit": 1 } ]
  }
}
`
)

func diffLines(d, kind string) []string {
	var out []string
	for _, l := range strings.Split(d, "\n") {
		if strings.HasPrefix(l, kind) && !strings.HasPrefix(l, kind+kind+kind) {
			out = append(out, l)
		}
	}
	return out
}

func connectCursor(t *testing.T, home string, m *manifest.Manifest) *Plan {
	t.Helper()
	p, err := Prepare(home, "cursor", "cursor")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Apply(m); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestConnectChangesOnlyTheLinesOfTheEntries(t *testing.T) {
	home, m := setup(t)
	mcp := filepath.Join(home, ".cursor", "mcp.json")
	hooks := filepath.Join(home, ".cursor", "hooks.json")
	writeAll(t, map[string]string{mcp: userMCP, hooks: userHooks})
	p, err := Prepare(home, "cursor", "cursor")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range p.Files {
		d := unifiedDiff(f.Path, f.Path, string(f.Before), string(f.After))
		removed := diffLines(d, "-")
		switch f.Path {
		case mcp:
			// The diff shows only added lines: the entry of mai and a comma after the entry
			// before it.
			if len(removed) != 0 {
				t.Fatalf("mcp.json: removed lines %q in\n%s", removed, d)
			}
			want := strings.Replace(userMCP, "    }\n  }\n}\n",
				"    },\n    \"mai\": {\n      \"command\": \""+testBin+"\",\n      \"args\": [\n        \"mcp\",\n        \"--agent\",\n        \"cursor\"\n      ]\n    }\n  }\n}\n", 1)
			if string(f.After) != want {
				t.Fatalf("mcp.json:\n%s", unifiedDiff("want", "got", want, string(f.After)))
			}
		case hooks:
			// Each inline array gets the hook of mai on the same line. The other lines stay.
			if len(removed) != 3 {
				t.Fatalf("hooks.json: removed lines %q in\n%s", removed, d)
			}
			for _, e := range []string{"beforeSubmitPrompt", "postToolUse"} {
				line := `    "` + e + `": [ { "command": "/opt/bridge/bin/other hook --event ` + e + `", "timeout": 10 }, { "command": "` + testBin + ` hook --format cursor --event ` + e + `", "timeout": 10 } ],`
				if !strings.Contains(string(f.After), line+"\n") {
					t.Fatalf("hooks.json has no line\n%s\nin\n%s", line, f.After)
				}
			}
		}
		if i, j := strings.Index(d, "\n-"), strings.Index(d, "\n+ "); i < 0 || j < 0 || i > j {
			if strings.Contains(d, "\n-") {
				t.Fatalf("the removed line does not come before the added lines:\n%s", d)
			}
		}
	}
	if err := p.Apply(m); err != nil {
		t.Fatal(err)
	}
	if err := Disconnect("cursor", m); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{mcp: userMCP, hooks: userHooks} {
		b, _ := os.ReadFile(path)
		if string(b) != want {
			t.Fatalf("%s after disconnect:\n%s", path, unifiedDiff("want", "got", want, string(b)))
		}
	}
}

func TestEntriesNameTheFullPathOfMai(t *testing.T) {
	for _, app := range Apps {
		t.Run(app, func(t *testing.T) {
			home, _ := setup(t)
			p, err := Prepare(home, app, "cursor")
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range p.Files {
				text := string(f.After)
				if strings.Contains(text, `"command": "mai"`) || strings.Contains(text, `"mai hook`) || strings.Contains(text, `command = "mai"`) {
					t.Fatalf("%s names mai with no path:\n%s", f.Path, text)
				}
				if !strings.Contains(text, testBin) {
					t.Fatalf("%s does not name %s:\n%s", f.Path, testBin, text)
				}
			}
		})
	}
}

func TestConnectAfterAMoveReplacesTheEntries(t *testing.T) {
	for _, app := range Apps {
		t.Run(app, func(t *testing.T) {
			home, m := setup(t)
			orig := originals(home)
			writeAll(t, orig)
			p, err := Prepare(home, app, "cursor")
			if err != nil {
				t.Fatal(err)
			}
			if err := p.Apply(m); err != nil {
				t.Fatal(err)
			}
			moved := "/usr/local/other/mai"
			useBinary(t, moved)
			again, err := Prepare(home, app, "cursor")
			if err != nil {
				t.Fatalf("connect after a move: %v", err)
			}
			if !again.Changed() {
				t.Fatal("connect after a move changes nothing")
			}
			if err := again.Apply(m); err != nil {
				t.Fatal(err)
			}
			for _, f := range again.Files {
				b, _ := os.ReadFile(f.Path)
				if strings.Contains(string(b), testBin) {
					t.Fatalf("%s keeps the old path:\n%s", f.Path, b)
				}
				if n := strings.Count(string(b), "hook --format "+app+" --event "); n > 0 {
					events := map[string]int{}
					for _, l := range strings.Split(string(b), "hook --format "+app+" --event ")[1:] {
						events[strings.FieldsFunc(l, func(r rune) bool { return r == '"' || r == ' ' })[0]]++
					}
					for e, k := range events {
						if k != 1 {
							t.Fatalf("%s has %d hooks of mai for %s:\n%s", f.Path, k, e, b)
						}
					}
				}
			}
			if err := Disconnect(app, m); err != nil {
				t.Fatal(err)
			}
			for path, want := range orig {
				b, err := os.ReadFile(path)
				if err == nil && string(b) != want {
					t.Fatalf("%s after disconnect:\n%s", path, unifiedDiff("want", "got", want, string(b)))
				}
			}
		})
	}
}

func TestConnectReplacesAnEntryOfMaiWithNoPath(t *testing.T) {
	home, m := setup(t)
	mcp := filepath.Join(home, ".cursor", "mcp.json")
	writeAll(t, map[string]string{
		mcp: `{"mcpServers":{"mai":{"command":"mai","args":["mcp","--agent","cursor"]}}}`,
	})
	connectCursor(t, home, m)
	b, _ := os.ReadFile(mcp)
	if !strings.Contains(string(b), `"command":"`+testBin+`"`) {
		t.Fatalf("the entry with no path stays:\n%s", b)
	}
}

func TestHookCommandQuotesForTheShell(t *testing.T) {
	cases := []struct{ bin, goos, want string }{
		{"/Users/a/go/bin/mai", "darwin", "/Users/a/go/bin/mai hook --format cursor --event stop"},
		{"/Users/a b/go/bin/mai", "darwin", "'/Users/a b/go/bin/mai' hook --format cursor --event stop"},
		{"/Users/o'k/mai", "linux", `'/Users/o'\''k/mai' hook --format cursor --event stop`},
		{`C:\Users\a\go\bin\mai.exe`, "windows", `C:\Users\a\go\bin\mai.exe hook --format cursor --event stop`},
		{`C:\Users\a b\go\bin\mai.exe`, "windows", `"C:\Users\a b\go\bin\mai.exe" hook --format cursor --event stop`},
	}
	for _, c := range cases {
		got := hookCommand(c.bin, c.goos, "cursor", "stop")
		if got != c.want {
			t.Errorf("hookCommand(%q, %s) = %s, want %s", c.bin, c.goos, got, c.want)
		}
		if !isMaiHook(got, "cursor", "stop") || isMaiHook(got, "claude", "stop") {
			t.Errorf("isMaiHook does not find %s", got)
		}
	}
	if isMaiHook("/opt/bridge/bin/other hook --format cursor --event stop", "", "") {
		t.Error("isMaiHook finds a hook of a different program")
	}
}

func TestTOMLStringEscapes(t *testing.T) {
	if got := tomlString(`C:\Users\a "b"`); got != `"C:\\Users\\a \"b\""` {
		t.Fatalf("got %s", got)
	}
}

func TestATemporaryBuildIsRefused(t *testing.T) {
	if !isTemporaryBuild("/var/folders/x/T/go-build123/b001/exe/mai") || isTemporaryBuild("/Users/a/go/bin/mai") {
		t.Fatal("isTemporaryBuild")
	}
}

func TestScanJSONRefusesNonStrictText(t *testing.T) {
	for _, s := range []string{`{"a":1,}`, `{"a":1} // c`, `{a:1}`, `{"a":tru}`, `[1 2]`, `{"a":"x`} {
		if _, err := scanJSON([]byte(s)); err == nil {
			t.Errorf("scanJSON(%s) gives no error", s)
		}
	}
	n, err := scanJSON([]byte(` {"a": [1, {"b": "\"}"}], "c": null} `))
	if err != nil {
		t.Fatal(err)
	}
	if _, v := n.member("a"); v == nil || len(v.elems) != 2 {
		t.Fatal("the scan lost a value")
	}
}

func TestUnifiedDiffPutsRemovedLinesFirst(t *testing.T) {
	d := unifiedDiff("a", "b", "1\nx\n3\n", "1\ny\n3\n")
	want := "--- a\n+++ b\n@@ -1,3 +1,3 @@\n 1\n-x\n+y\n 3\n"
	if d != want {
		t.Fatalf("got\n%s", d)
	}
}

func TestExistingEntryOfADifferentProgramStays(t *testing.T) {
	home, _ := setup(t)
	writeAll(t, map[string]string{
		filepath.Join(home, ".cursor", "mcp.json"): `{"mcpServers":{"mai":{"command":"/opt/x/notmai","args":["mcp"]}}}`,
	})
	if _, err := Prepare(home, "cursor", "cursor"); !errors.Is(err, ErrExists) {
		t.Fatalf("got %v", err)
	}
}
