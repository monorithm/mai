package connect

// More cases that mai connect, mai disconnect, and mai uninstall must handle without a loss of
// content of the user, each with a concrete input.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/manifest"
)

// Helpers used by the tests in verify_variants_test.go.
func connectApp(t *testing.T, home, app string, m *manifest.Manifest) {
	t.Helper()
	p, err := Prepare(home, app, "a")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Apply(m); err != nil {
		t.Fatal(err)
	}
}

func readText(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCodexKeepsAUserCommentAfterTheMaiTable(t *testing.T) {
	home, m := setup(t)
	cfg := filepath.Join(home, ".codex", "config.toml")
	const user = "model = \"gpt-5-codex\"\n"
	writeAll(t, map[string]string{cfg: user})
	connectApp(t, home, "codex", m)
	const added = "\n# Linear, added by hand\n[mcp_servers.linear]\ncommand = \"npx\"\n"
	edited := readText(t, cfg) + added
	writeAll(t, map[string]string{cfg: edited})

	again, err := Prepare(home, "codex", "a")
	if err != nil {
		t.Fatal(err)
	}
	if after := fileAfter(t, again, cfg); !strings.Contains(after, "# Linear, added by hand") {
		t.Fatalf("a second connect with no change deletes the comment of the user:\n%s", unifiedDiff("before", "after", edited, after))
	}
	if err := Disconnect("codex", m); err != nil {
		t.Fatalf("disconnect of an unchanged table of mai: %v", err)
	}
	if got, want := readText(t, cfg), user+added; got != want {
		t.Fatalf("config.toml after disconnect:\n%s", unifiedDiff("want", "got", want, got))
	}
}

func TestCodexRefusesAQuotedMaiKeyInARootInlineTable(t *testing.T) {
	for _, body := range []string{
		"model = \"gpt-5\"\nmcp_servers = { \"mai\" = { command = \"/opt/x/other\" } }\n",
		"model = \"gpt-5\"\nmcp_servers = { github = { command = \"npx\" }, 'mai' = { command = \"/opt/x/other\" } }\n",
	} {
		home, _ := setup(t)
		cfg := filepath.Join(home, ".codex", "config.toml")
		writeAll(t, map[string]string{cfg: body})
		p, err := Prepare(home, "codex", "a")
		if errors.Is(err, ErrExists) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if after := fileAfter(t, p, cfg); strings.Contains(after, "[mcp_servers.mai]") {
			t.Errorf("connect adds a second definition of mcp_servers.mai:\n%s", after)
		}
	}
}

func TestCodexDisconnectWithASubTableOfMai(t *testing.T) {
	home, m := setup(t)
	cfg := filepath.Join(home, ".codex", "config.toml")
	writeAll(t, map[string]string{cfg: "model = \"gpt-5-codex\"\n"})
	connectApp(t, home, "codex", m)
	writeAll(t, map[string]string{cfg: readText(t, cfg) + "\n[mcp_servers.mai.env]\nMAI_LOG = \"debug\"\n"})

	err := Disconnect("codex", m)
	got := readText(t, cfg)
	if err == nil && strings.Contains(got, "[mcp_servers.mai.env]") && !strings.Contains(got, "[mcp_servers.mai]\n") {
		t.Fatalf("disconnect gives no error and leaves a server mai with no command:\n%s", got)
	}
}

func TestAntigravityReconnectKeepsAUserHookNextToTheHookOfMai(t *testing.T) {
	home, m := setup(t)
	hooks := filepath.Join(home, ".gemini", "config", "hooks.json")
	connectApp(t, home, "antigravity", m)
	b := readText(t, hooks)
	const maiHook = "--event PreInvocation\",\n        \"timeout\": 10\n      }"
	edited := strings.Replace(b, maiHook, maiHook+",\n      {\n        \"type\": \"command\",\n        \"command\": \"/usr/local/bin/notify-start\"\n      }", 1)
	if edited == b {
		t.Fatalf("the test could not edit the file:\n%s", b)
	}
	writeAll(t, map[string]string{hooks: edited})

	again, err := Prepare(home, "antigravity", "a")
	if err != nil {
		t.Fatal(err)
	}
	if after := fileAfter(t, again, hooks); !strings.Contains(after, "notify-start") {
		t.Fatalf("a second connect with no change deletes the hook of the user:\n%s", unifiedDiff("before", "after", edited, after))
	}
}

func TestCodexDisconnectDoesNotForgetATableItCannotFind(t *testing.T) {
	home, m := setup(t)
	cfg := filepath.Join(home, ".codex", "config.toml")
	writeAll(t, map[string]string{cfg: "model = \"gpt-5-codex\"\n"})
	connectApp(t, home, "codex", m)
	b := readText(t, cfg)
	edited := strings.Replace(b, tomlBegin, "", 1)
	if edited == b {
		t.Fatalf("the test could not edit the file:\n%s", b)
	}
	writeAll(t, map[string]string{cfg: edited})

	err := Disconnect("codex", m)
	got := readText(t, cfg)
	if err == nil && strings.Contains(got, "[mcp_servers.mai]") {
		_, again := Prepare(home, "codex", "a")
		t.Fatalf("disconnect gives no error, forgets the entry (%d entries stay in the manifest), and keeps the table of mai; a new connect gives %v:\n%s",
			len(m.Of(manifest.AppEntry)), again, got)
	}
}

func TestCursorDisconnectKeepsTheVersionForTheHooksOfTheUser(t *testing.T) {
	home, m := setup(t)
	hooks := filepath.Join(home, ".cursor", "hooks.json")
	connectCursor(t, home, m)
	b := readText(t, hooks)
	edited := strings.Replace(b, "\"hooks\": {\n", "\"hooks\": {\n    \"afterFileEdit\": [\n      {\n        \"command\": \"/usr/local/bin/format-file\"\n      }\n    ],\n", 1)
	if edited == b {
		t.Fatalf("the test could not edit the file:\n%s", b)
	}
	writeAll(t, map[string]string{hooks: edited})

	if err := Disconnect("cursor", m); err != nil {
		t.Fatal(err)
	}
	got := readText(t, hooks)
	if strings.Contains(got, "afterFileEdit") && !strings.Contains(got, "\"version\"") {
		t.Fatalf("disconnect removes the version of a hooks.json that holds hooks of the user:\n%s", got)
	}
}

// in cli/internal/connect/verify_newcode_test.go (imports: os, path/filepath, strings, testing, internal/manifest)
const codexUser = `model = "gpt-5-codex"
`

func connectCodex(t *testing.T, home string, m *manifest.Manifest) (string, string) {
	t.Helper()
	cfg := filepath.Join(home, ".codex", "config.toml")
	writeAll(t, map[string]string{cfg: codexUser})
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
	return cfg, string(b)
}

func TestReconnectKeepsAUserCommentAboveATableAfterMai(t *testing.T) {
	home, m := setup(t)
	cfg, written := connectCodex(t, home, m)
	const added = `
# Linear: issues of the shop team
[mcp_servers.linear]
command = "npx"
args = ["-y", "mcp-remote", "https://mcp.linear.app/sse"]
`
	edited := written + added
	writeAll(t, map[string]string{cfg: edited})

	again, err := Prepare(home, "codex", "codex")
	if err != nil {
		t.Fatal(err)
	}
	if after := fileAfter(t, again, cfg); !strings.Contains(after, "# Linear: issues of the shop team") {
		t.Errorf("a second connect with the same binary deletes the comment of the user:\n%s", unifiedDiff("before", "after", edited, after))
	}
	if err := Disconnect("codex", m); err != nil {
		t.Fatalf("disconnect of the unchanged table of mai: %v", err)
	}
	if got, _ := os.ReadFile(cfg); string(got) != codexUser+added {
		t.Fatalf("config.toml after disconnect:\n%s", unifiedDiff("want", "got", codexUser+added, string(got)))
	}
}

func TestInlineTableMcpServersGetsNoTableOfMai(t *testing.T) {
	for _, c := range []struct{ name, body string }{
		{"quoted key mai", `mcp_servers = { "mai" = { command = "/opt/x/other" } }
`},
		{"other server", `mcp_servers = { github = { command = "npx", args = ["-y", "@modelcontextprotocol/server-github"] } }
`},
	} {
		t.Run(c.name, func(t *testing.T) {
			home, _ := setup(t)
			cfg := filepath.Join(home, ".codex", "config.toml")
			writeAll(t, map[string]string{cfg: c.body})
			p, err := Prepare(home, "codex", "a")
			if err != nil {
				return // refused: correct
			}
			if after := fileAfter(t, p, cfg); strings.Contains(after, "[mcp_servers.mai]") {
				t.Errorf("connect adds a table [mcp_servers.mai] after the inline table mcp_servers, an invalid TOML file:\n%s", after)
			}
		})
	}
}

// uses codexUser and connectCodex (see the first defect; same file)
func TestDisconnectLeavesNoServerMaiWithoutCommand(t *testing.T) {
	home, m := setup(t)
	cfg, written := connectCodex(t, home, m)
	writeAll(t, map[string]string{cfg: written + `
[mcp_servers.mai.env]
MAI_LOG = "debug"
`})
	err := Disconnect("codex", m)
	got, _ := os.ReadFile(cfg)
	if err == nil && strings.Contains(string(got), "[mcp_servers.mai.env]") && !strings.Contains(string(got), "[mcp_servers.mai]\n") {
		t.Fatalf("disconnect leaves the server mai with only an env table and no command:\n%s", got)
	}
}

// uses codexUser and connectCodex (see the first defect; same file)
func TestDisconnectDoesNotSilentlyKeepAnEditedTableOfMai(t *testing.T) {
	for _, c := range []struct{ name, from, to string }{
		{"comment after the header", "[mcp_servers.mai]\n", "[mcp_servers.mai] # mail for the agents\n"},
		{"blank line under the begin comment", tomlBegin, tomlBegin + "\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			home, m := setup(t)
			cfg, written := connectCodex(t, home, m)
			edited := strings.Replace(written, c.from, c.to, 1)
			if edited == written {
				t.Fatalf("the test could not edit the file:\n%s", written)
			}
			writeAll(t, map[string]string{cfg: edited})
			err := Disconnect("codex", m)
			got, _ := os.ReadFile(cfg)
			if err == nil && strings.Contains(string(got), "[mcp_servers.mai]") {
				t.Fatalf("disconnect reports success and keeps the table of mai:\n%s", got)
			}
		})
	}
}

// uses codexUser (see the first defect; same file)
func TestDisconnectOfA301TableAfterCodexAddedATable(t *testing.T) {
	home, m := setup(t)
	cfg := filepath.Join(home, ".codex", "config.toml")
	table := `[mcp_servers.mai]
command = "` + testBin + `"
args = ["mcp", "--agent", "codex"]
`
	old := "\n" + tomlBegin + table + tomlEnd // the block of 3.0.1
	if err := m.Add(manifest.Item{Type: manifest.AppEntry, Name: "codex", Path: cfg, Pointer: TOMLPointer, Hash: hashOf([]byte(old))}); err != nil {
		t.Fatal(err)
	}
	const added = `
[projects."/Users/a/src/shop"]
trust_level = "trusted"
`
	writeAll(t, map[string]string{cfg: codexUser + "\n" + tomlBegin + table + added + tomlEnd})
	if err := Disconnect("codex", m); err != nil {
		t.Fatalf("disconnect of the unchanged table of 3.0.1: %v", err)
	}
	if got, _ := os.ReadFile(cfg); string(got) != codexUser+added {
		t.Fatalf("config.toml after disconnect:\n%s", unifiedDiff("want", "got", codexUser+added, string(got)))
	}
}

func TestConnectKeepsADanglingSymlink(t *testing.T) {
	home, m := setup(t)
	target := filepath.Join(home, "dotfiles", "cursor", "mcp.json")
	link := filepath.Join(home, ".cursor", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	connectCursor(t, home, m)
	if st, err := os.Lstat(link); err != nil || st.Mode()&os.ModeSymlink == 0 {
		_, terr := os.Stat(target)
		t.Errorf("connect replaced the link with a regular file (lstat error %v); the target exists: %v", err, terr == nil)
	}
	if err := Disconnect("cursor", m); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Lstat(link); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("after connect and disconnect, the link of the user is gone: %v", err)
	}
}
