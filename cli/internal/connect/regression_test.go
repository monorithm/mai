package connect

// Each test in this file gives a concrete input for a case that mai connect, mai
// disconnect, or mai uninstall must handle without a loss of content of the user.

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/manifest"
)

// maiStopHook is the end of the Stop hook of mai in a settings.json of Claude that mai connect
// made, with the indent of encodeJSON.
const maiStopHook = "\"" + testBin + " hook --format claude --event Stop\",\n            \"timeout\": 10\n          }"

// groupUserHookWithMai puts a hook of the user in the same group as the Stop hook of mai, as a
// user (or the hooks menu of Claude) does to add a second Stop hook.
func groupUserHookWithMai(t *testing.T, settings string) {
	t.Helper()
	b, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(b), maiStopHook) != 1 {
		t.Fatalf("the Stop hook of mai is not in the expected form:\n%s", b)
	}
	grouped := strings.Replace(string(b), maiStopHook,
		maiStopHook+",\n          {\n            \"type\": \"command\",\n            \"command\": \"say done\"\n          }", 1)
	writeAll(t, map[string]string{settings: grouped})
}

func TestConnectAgainKeepsAUserHookInTheGroupOfMai(t *testing.T) {
	home, m := setup(t)
	settings := filepath.Join(home, ".claude", "settings.json")
	p, err := Prepare(home, "claude", "a")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Apply(m); err != nil {
		t.Fatal(err)
	}
	groupUserHookWithMai(t, settings)
	again, err := Prepare(home, "claude", "a")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range again.Files {
		if f.Path == settings && !strings.Contains(string(f.After), "say done") {
			t.Fatalf("a second connect removes the hook of the user:\n%s", unifiedDiff("before", "after", string(f.Before), string(f.After)))
		}
	}
}

func TestForceDisconnectKeepsAUserHookInTheGroupOfMai(t *testing.T) {
	home, m := setup(t)
	settings := filepath.Join(home, ".claude", "settings.json")
	p, err := Prepare(home, "claude", "a")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Apply(m); err != nil {
		t.Fatal(err)
	}
	groupUserHookWithMai(t, settings)
	for _, it := range m.Of(manifest.AppEntry) {
		if it.Pointer == "/hooks/Stop/-" {
			if err := ForceRemoveEntry(it); err != nil {
				t.Fatal(err)
			}
		}
	}
	b, _ := os.ReadFile(settings)
	if !strings.Contains(string(b), "say done") {
		t.Fatalf("disconnect --force removes the hook of the user:\n%s", b)
	}
}

func TestForceDisconnectOfAHookThatTheUserRemoved(t *testing.T) {
	home, m := setup(t)
	settings := filepath.Join(home, ".claude", "settings.json")
	orig := "{\n  \"hooks\": {\n    \"Stop\": [\n      {\n        \"hooks\": [\n          {\n            \"type\": \"command\",\n            \"command\": \"other\"\n          }\n        ]\n      }\n    ]\n  }\n}\n"
	writeAll(t, map[string]string{settings: orig})
	p, err := Prepare(home, "claude", "a")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Apply(m); err != nil {
		t.Fatal(err)
	}
	// The user removes the hooks of mai by hand and keeps the own Stop hook.
	writeAll(t, map[string]string{settings: orig})
	for _, it := range m.Of(manifest.AppEntry) {
		if it.Path != settings {
			continue
		}
		// The entry is gone already: a remove must give no error, the more so a forced one.
		if err := ForceRemoveEntry(it); err != nil {
			t.Fatalf("%s: %v", it.Pointer, err)
		}
	}
}

func TestCRLFFileKeepsCRLF(t *testing.T) {
	home, _ := setup(t)
	mcp := filepath.Join(home, ".cursor", "mcp.json")
	writeAll(t, map[string]string{
		mcp: "{\r\n  \"mcpServers\": {\r\n    \"other\": {\r\n      \"command\": \"x\"\r\n    }\r\n  }\r\n}\r\n",
	})
	p, err := Prepare(home, "cursor", "cursor")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range p.Files {
		if f.Path != mcp {
			continue
		}
		if n := bytes.Count(f.After, []byte("\n")) - bytes.Count(f.After, []byte("\r\n")); n > 0 {
			t.Fatalf("the CRLF file gets %d lines with LF only:\n%q", n, f.After)
		}
	}
}

func TestEmptyContainerFormsRoundTrip(t *testing.T) {
	for _, c := range []struct{ name, file, text string }{
		{"object with a space", "mcp.json", "{\n  \"mcpServers\": { }\n}\n"},
		{"object on two lines", "mcp.json", "{\n  \"mcpServers\": {\n  }\n}\n"},
		{"root on two lines", "mcp.json", "{\n}\n"},
		{"array with a space", "hooks.json", "{\n  \"version\": 1,\n  \"hooks\": {\n    \"beforeSubmitPrompt\": [ ],\n    \"postToolUse\": [],\n    \"stop\": []\n  }\n}\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			home, m := setup(t)
			path := filepath.Join(home, ".cursor", c.file)
			writeAll(t, map[string]string{path: c.text})
			connectCursor(t, home, m)
			if err := Disconnect("cursor", m); err != nil {
				t.Fatal(err)
			}
			b, _ := os.ReadFile(path)
			if string(b) != c.text {
				t.Fatalf("%s after disconnect: got %q, want %q", c.file, b, c.text)
			}
		})
	}
}

func TestEmptyFileRoundTrip(t *testing.T) {
	for _, text := range []string{"", "\n"} {
		t.Run("", func(t *testing.T) {
			home, m := setup(t)
			mcp := filepath.Join(home, ".cursor", "mcp.json")
			writeAll(t, map[string]string{mcp: text})
			connectCursor(t, home, m)
			if err := Disconnect("cursor", m); err != nil {
				t.Fatal(err)
			}
			b, _ := os.ReadFile(mcp)
			if string(b) != text {
				t.Fatalf("mcp.json after disconnect: got %q, want %q", b, text)
			}
		})
	}
}

// fileAfter returns the planned text of path, and fails when the plan does not have the file.
func fileAfter(t *testing.T, p *Plan, path string) string {
	t.Helper()
	for _, f := range p.Files {
		if f.Path == path {
			return string(f.After)
		}
	}
	t.Fatalf("the plan has no file %s", path)
	return ""
}

// Codex edits config.toml with toml_edit, which puts a new table after the last table: after
// the table of mai when it is the last one. A folder that the user trusts in Codex, or a server
// of "codex mcp add", then comes after the table of mai. A second mai connect and mai
// disconnect must keep it. The file of 3.0.0 has the end comment of mai after the table, and
// toml_edit keeps that comment at the end of the file, after the new tables.
func TestCodexReconnectKeepsTablesThatCodexAddedAfterTheMaiTable(t *testing.T) {
	const added = "\n[projects.\"/Users/a/src/shop\"]\ntrust_level = \"trusted\"\n\n[mcp_servers.linear]\ncommand = \"npx\"\nargs = [\"-y\", \"mcp-remote\", \"https://mcp.linear.app/sse\"]\n"
	const user = "model = \"gpt-5-codex\"\nmodel_reasoning_effort = \"high\"\n\n[mcp_servers.github]\ncommand = \"npx\"\nargs = [\"-y\", \"@modelcontextprotocol/server-github\"]\n"
	for _, c := range []struct {
		name  string
		codex func(written string) string // the file after Codex wrote its tables
	}{
		{"3.0.1", func(w string) string { return w + added }},
		{"3.0.0 with the end comment", func(w string) string {
			// The table of 3.0.0 named the binary with no path and ended with the end comment.
			old := strings.Replace(w, "command = \""+testBin+"\"", "command = \"mai\"", 1)
			return old + tomlEnd + added
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			home, m := setup(t)
			cfg := filepath.Join(home, ".codex", "config.toml")
			writeAll(t, map[string]string{cfg: user})
			p, err := Prepare(home, "codex", "codex")
			if err != nil {
				t.Fatal(err)
			}
			if err := p.Apply(m); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(cfg)
			if err != nil {
				t.Fatal(err)
			}
			byCodex := c.codex(string(b))
			if c.name != "3.0.1" {
				// A file of 3.0.0 has a different entry: the manifest of the test starts again.
				byCodex = strings.Replace(byCodex, string(tomlBlock("[mcp_servers.mai]\ncommand = \""+testBin+"\"", "\n")), string(tomlBlock("[mcp_servers.mai]\ncommand = \"mai\"", "\n")), 1)
			}
			writeAll(t, map[string]string{cfg: byCodex})
			again, err := Prepare(home, "codex", "codex")
			if err != nil {
				t.Fatal(err)
			}
			after := fileAfter(t, again, cfg)
			if !strings.Contains(after, "[projects.\"/Users/a/src/shop\"]") || !strings.Contains(after, "[mcp_servers.linear]") {
				t.Fatalf("a second connect deletes the tables of Codex:\n%s", unifiedDiff("before", "after", byCodex, after))
			}
			if strings.Contains(after, tomlEnd) || strings.Count(after, "[mcp_servers.mai]") != 1 || !strings.Contains(after, testBin) {
				t.Fatalf("the table of mai is not one table with the path of mai:\n%s", after)
			}
			if err := again.Apply(m); err != nil {
				t.Fatal(err)
			}
			if err := Disconnect("codex", m); err != nil {
				t.Fatal(err)
			}
			got, _ := os.ReadFile(cfg)
			if want := user + added; string(got) != want {
				t.Fatalf("config.toml after disconnect:\n%s", unifiedDiff("want", "got", want, string(got)))
			}
		})
	}
}

// The hooks format of Claude Code puts a list of hooks in each group, and a user can put a
// hook of their own in the group of mai. A second mai connect must keep that hook.
func TestClaudeReconnectKeepsAUserHookInTheGroupOfMai(t *testing.T) {
	home, m := setup(t)
	settings := filepath.Join(home, ".claude", "settings.json")
	writeAll(t, map[string]string{settings: "{\n  \"model\": \"opus\"\n}\n"})
	p, err := Prepare(home, "claude", "claude")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Apply(m); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	// The user adds a sound to the Stop group of mai.
	mine := "/opt/mai/bin/mai hook --format claude --event Stop\",\n            \"timeout\": 10\n          },\n          {\n            \"type\": \"command\",\n            \"command\": \"afplay /System/Library/Sounds/Glass.aiff\""
	edited := strings.Replace(string(b), "/opt/mai/bin/mai hook --format claude --event Stop\",\n            \"timeout\": 10", mine, 1)
	if edited == string(b) {
		t.Fatalf("the test could not edit the file:\n%s", b)
	}
	writeAll(t, map[string]string{settings: edited})

	again, err := Prepare(home, "claude", "claude")
	if err != nil {
		t.Fatal(err)
	}
	after := fileAfter(t, again, settings)
	if !strings.Contains(after, "afplay /System/Library/Sounds/Glass.aiff") {
		t.Fatalf("a second connect with no change deletes the hook of the user:\n%s", unifiedDiff("before", "after", edited, after))
	}
}

// Many users keep ~/.claude/settings.json as a link to a dotfiles repository. mai connect must
// change the file that the link names, and keep the link.
func TestConnectKeepsASymlinkedSettingsFile(t *testing.T) {
	home, m := setup(t)
	target := filepath.Join(home, "dotfiles", "claude", "settings.json")
	orig := "{\n  \"model\": \"opus\"\n}\n"
	writeAll(t, map[string]string{target: orig})
	settings := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, settings); err != nil {
		t.Fatal(err)
	}
	p, err := Prepare(home, "claude", "claude")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Apply(m); err != nil {
		t.Fatal(err)
	}
	st, err := os.Lstat(settings)
	if err != nil {
		t.Fatal(err)
	}
	tb, _ := os.ReadFile(target)
	if st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("connect replaced the link with a regular file; the dotfiles file is unchanged: %v\n%s", string(tb) == orig, tb)
	}
}

// A user who removes the last server by hand in VS Code keeps the braces on two lines.
// connect then disconnect must give back the original bytes.
func TestDisconnectRestoresAnEmptyObjectOnTwoLines(t *testing.T) {
	home, m := setup(t)
	path := filepath.Join(vscodeUserDir(home), "mcp.json")
	orig := "{\n\t\"servers\": {\n\t},\n\t\"inputs\": []\n}"
	writeAll(t, map[string]string{path: orig})
	p, err := Prepare(home, "copilot", "copilot")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Apply(m); err != nil {
		t.Fatal(err)
	}
	if err := Disconnect("copilot", m); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != orig {
		t.Fatalf("disconnect gives other bytes:\nwant %q\ngot  %q", orig, b)
	}
}

func TestReconnectKeepsUserHookInSharedGroup(t *testing.T) {
	const mine = "/usr/local/bin/notify-done"
	cases := []struct{ app, path, text string }{
		{"claude", filepath.Join(".claude", "settings.json"), `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"` + testBin + ` hook --format claude --event Stop","timeout":10},{"type":"command","command":"` + mine + `"}]}]}}`},
		{"codex", filepath.Join(".codex", "hooks.json"), `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"` + testBin + ` hook --format codex --event Stop","timeout":10},{"type":"command","command":"` + mine + `"}]}]}}`},
	}
	for _, c := range cases {
		t.Run(c.app, func(t *testing.T) {
			home, m := setup(t)
			path := filepath.Join(home, c.path)
			writeAll(t, map[string]string{path: c.text})
			useBinary(t, "/usr/local/other/mai") // the binary moved
			p, err := Prepare(home, c.app, "a")
			if err != nil {
				t.Fatal(err)
			}
			if err := p.Apply(m); err != nil {
				t.Fatal(err)
			}
			after, _ := os.ReadFile(path)
			if !strings.Contains(string(after), mine) {
				_, stop, _ := strings.Cut(string(after), `"Stop":`)
				t.Fatalf("connect deletes the hook %s of the user; Stop is now %s", mine, stop)
			}
		})
	}
}

func TestTOMLUserTableOfMaiInOtherForm(t *testing.T) {
	for _, body := range []string{
		"[mcp_servers.\"mai\"]\ncommand = \"/opt/x/other\"\n",
		"[mcp_servers]\nmai = { command = \"/opt/x/other\" }\n",
		"[ mcp_servers.mai ]\ncommand = \"/opt/x/other\"\n",
	} {
		home, _ := setup(t)
		writeAll(t, map[string]string{filepath.Join(home, ".codex", "config.toml"): body})
		p, err := Prepare(home, "codex", "a")
		if errors.Is(err, ErrExists) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range p.Files {
			if strings.HasSuffix(f.Path, "config.toml") {
				t.Errorf("connect adds a second table of mai after %q:\n%s", strings.SplitN(body, "\n", 2)[0], f.After)
			}
		}
	}
}

func TestOwnEntryWithOtherBinaryName(t *testing.T) {
	t.Run("codex", func(t *testing.T) {
		home, m := setup(t)
		useBinary(t, "/Users/a/bin/mai-dev")
		p, err := Prepare(home, "codex", "a")
		if err != nil {
			t.Fatal(err)
		}
		if err := p.Apply(m); err != nil {
			t.Fatal(err)
		}
		useBinary(t, "/usr/local/bin/mai-dev")
		if p, err = Prepare(home, "codex", "a"); err != nil {
			t.Fatal(err)
		}
		if err := p.Apply(m); err != nil {
			t.Fatal(err)
		}
		if err := Disconnect("codex", m); err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(filepath.Join(home, ".codex", "hooks.json"))
		if n := strings.Count(string(b), "/Users/a/bin/mai-dev hook"); n > 0 {
			t.Errorf("after disconnect, hooks.json keeps %d hooks of mai with the old path", n)
		}
	})
	t.Run("cursor", func(t *testing.T) {
		home, m := setup(t)
		useBinary(t, "/Users/a/bin/mai-dev")
		p, err := Prepare(home, "cursor", "a")
		if err != nil {
			t.Fatal(err)
		}
		if err := p.Apply(m); err != nil {
			t.Fatal(err)
		}
		useBinary(t, "/usr/local/bin/mai-dev")
		if _, err := Prepare(home, "cursor", "a"); err != nil {
			t.Errorf("connect after a move refuses the entry that mai wrote: %v", err)
		}
	})
	t.Run("windows", func(t *testing.T) {
		// Windows file names are not case-sensitive: MAI.EXE is mai.exe.
		cmd := `C:\Users\a\go\bin\MAI.EXE hook --format cursor --event stop`
		if !isMaiHook(cmd, "cursor", "stop") {
			t.Errorf("isMaiHook(%s) = false", cmd)
		}
	})
}

func TestTOMLBlockWithoutLeadingNewline(t *testing.T) {
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
	if err := os.WriteFile(toml, bytes.TrimLeft(b, "\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = Disconnect("codex", m)
	b, _ = os.ReadFile(toml)
	if err == nil && bytes.Contains(b, []byte("[mcp_servers.mai]")) {
		t.Fatalf("disconnect gives no error and keeps the table of mai:\n%s", b)
	}
}

func TestTemporaryBuildDetection(t *testing.T) {
	for _, p := range []string{"/Users/a/go-builds/bin/mai", "/opt/go-buildkite/bin/mai"} {
		if isTemporaryBuild(p) {
			t.Errorf("isTemporaryBuild(%q) = true for an installed binary", p)
		}
	}
	// The second "go run" of Go 1.24 and later starts the executable from GOCACHE. This is
	// the path with GOCACHE=/Users/a/.cache/gocache.
	cached := "/Users/a/.cache/gocache/3c/3c05e1339c6365ff00d2358c502576e9e71f00d9172ceff22bed3fee2fbc3063-d/mai"
	if !isTemporaryBuild(cached) {
		t.Errorf("isTemporaryBuild(%q) = false for a go run build in the cache", cached)
	}
}

func TestBinaryPathNotUTF8(t *testing.T) {
	bin := "/home/jos\xe9/go/bin/mai"
	for _, app := range []string{"cursor", "codex"} {
		home, _ := setup(t)
		useBinary(t, bin)
		p, err := Prepare(home, app, "a")
		if err != nil {
			continue
		}
		for _, f := range p.Files {
			if strings.Contains(string(f.After), "mcp") && !bytes.Contains(f.After, []byte(bin)) {
				t.Errorf("%s: %s names a different binary:\n%s", app, filepath.Base(f.Path), f.After)
				break
			}
		}
	}
}
