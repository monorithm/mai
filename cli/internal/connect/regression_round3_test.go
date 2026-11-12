package connect

// More cases of the TOML file of Codex, the records of earlier versions, and links, each with
// a concrete input.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/manifest"
)

func TestCodexReconnectKeepsTheKeysThatTheUserAddedToTheTableOfMai(t *testing.T) {
	const keys = "startup_timeout_sec = 30\nenv = { MAI_LOG = \"debug\" }\n"
	for _, bin := range []string{testBin, "/usr/local/bin/mai"} {
		t.Run(bin, func(t *testing.T) {
			home, m := setup(t)
			cfg := filepath.Join(home, ".codex", "config.toml")
			writeAll(t, map[string]string{cfg: "model = \"gpt-5-codex\"\n"})
			connectApp(t, home, "codex", m)
			edited := readText(t, cfg) + keys
			writeAll(t, map[string]string{cfg: edited})
			useBinary(t, bin)
			p, err := Prepare(home, "codex", "a")
			if err != nil {
				return // refused: no loss
			}
			after := fileAfter(t, p, cfg)
			if !strings.Contains(after, "startup_timeout_sec = 30") || !strings.Contains(after, "MAI_LOG") {
				t.Fatalf("a second mai connect deletes the keys of the user in the table of mai:\n%s", unifiedDiff("before", "after", edited, after))
			}
		})
	}
}

func TestCodexDisconnectKeepsALineBreakBetweenTheLinesOfTheUser(t *testing.T) {
	const user = "model = \"gpt-5-codex\"\n\n[mcp_servers.github]\ncommand = \"npx\""
	const added = "[mcp_servers.linear]\ncommand = \"npx\"\n"
	home, m := setup(t)
	cfg := filepath.Join(home, ".codex", "config.toml")
	writeAll(t, map[string]string{cfg: user})
	connectApp(t, home, "codex", m)
	writeAll(t, map[string]string{cfg: readText(t, cfg) + added})
	if err := Disconnect("codex", m); err != nil {
		t.Fatalf("disconnect of the unchanged table of mai: %v", err)
	}
	if got, want := readText(t, cfg), user+"\n"+added; got != want {
		t.Fatalf("config.toml after disconnect (an invalid TOML file when two lines join):\n%s", unifiedDiff("want", "got", want, got))
	}
}

// forceRemoveTOML runs the TOML part of mai disconnect codex --force, and turns a panic into a
// failure of the test.
func forceRemoveTOML(t *testing.T, m *manifest.Manifest) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("mai disconnect codex --force panics: %v", r)
		}
	}()
	for _, it := range m.Of(manifest.AppEntry) {
		if it.Pointer != TOMLPointer {
			continue
		}
		if err := ForceRemoveEntry(it); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCodexForceDisconnectOfASubTableDirectlyUnderTheTableOfMai(t *testing.T) {
	const user = "model = \"gpt-5-codex\"\n"
	const sub = "[mcp_servers.mai.env]\nMAI_LOG = \"debug\"\n"
	for _, c := range []struct{ name, after string }{
		{"at the end of the file", ""},
		{"before a table of Codex", "\n[projects.\"/Users/a/src/shop\"]\ntrust_level = \"trusted\"\n"},
		{"before a table of the user with no blank line", "[mcp_servers.linear]\ncommand = \"npx\"\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			home, m := setup(t)
			cfg := filepath.Join(home, ".codex", "config.toml")
			writeAll(t, map[string]string{cfg: user})
			connectApp(t, home, "codex", m)
			writeAll(t, map[string]string{cfg: readText(t, cfg) + sub + c.after})
			var ce *ErrChanged
			if err := Disconnect("codex", m); !errors.As(err, &ce) {
				t.Fatalf("disconnect of a table of mai with a sub-table of the user: %v, want ErrChanged", err)
			}
			forceRemoveTOML(t, m)
			if got, want := readText(t, cfg), user+c.after; got != want {
				t.Fatalf("config.toml after mai disconnect --force:\n%s", unifiedDiff("want", "got", want, got))
			}
		})
	}
}

// uses forceRemoveTOML (see the defect above; same file)
func TestCodexForceDisconnectOfA301BlockWithASubTableAboveTheEndComment(t *testing.T) {
	const user = "model = \"gpt-5-codex\"\n"
	table := "[mcp_servers.mai]\ncommand = \"" + testBin + "\"\nargs = [\"mcp\", \"--agent\", \"codex\"]\n"
	block301 := "\n" + tomlBegin + table + tomlEnd
	for _, c := range []struct{ name, after string }{
		{"at the end of the file", ""},
		{"before a table of Codex", "\n[projects.\"/Users/a/src/shop\"]\ntrust_level = \"trusted\"\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			home, m := setup(t)
			cfg := filepath.Join(home, ".codex", "config.toml")
			if err := m.Add(manifest.Item{Type: manifest.AppEntry, Name: "codex", Path: cfg, Pointer: TOMLPointer, Hash: hashOf([]byte(block301))}); err != nil {
				t.Fatal(err)
			}
			withSub := strings.Replace(block301, tomlEnd, "\n[mcp_servers.mai.env]\nMAI_LOG = \"debug\"\n"+tomlEnd, 1)
			writeAll(t, map[string]string{cfg: user + withSub + c.after})
			forceRemoveTOML(t, m)
			if got, want := readText(t, cfg), user+c.after; got != want {
				t.Fatalf("config.toml after mai disconnect --force:\n%s", unifiedDiff("want", "got", want, got))
			}
		})
	}
}

func TestCodexConnectReadsTheRootTableAsTOMLDoes(t *testing.T) {
	const instructions = "developer_instructions = \"\"\"\nRules of the shop:\n[1] Run the tests before each commit.\n\"\"\"\n"
	for _, c := range []struct{ name, body string }{
		{"byte order mark, header", "\xEF\xBB\xBF[mcp_servers.mai]\ncommand = \"/opt/x/other\"\n"},
		{"byte order mark, key in [mcp_servers]", "\xEF\xBB\xBF[mcp_servers]\nmai = { command = \"/opt/x/other\" }\n"},
		{"byte order mark, inline table", "\xEF\xBB\xBFmcp_servers = { github = { command = \"npx\" } }\n"},
		{"multi-line string, inline table", "model = \"gpt-5-codex\"\n" + instructions + "mcp_servers = { github = { command = \"npx\" } }\n"},
		{"multi-line string, dotted key", "model = \"gpt-5-codex\"\n" + instructions + "mcp_servers.mai.command = \"/opt/x/other\"\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			home, _ := setup(t)
			cfg := filepath.Join(home, ".codex", "config.toml")
			writeAll(t, map[string]string{cfg: c.body})
			p, err := Prepare(home, "codex", "a")
			if errors.Is(err, ErrExists) {
				return // refused: correct
			}
			if err != nil {
				t.Fatal(err)
			}
			if after := fileAfter(t, p, cfg); strings.Contains(after, tomlBegin) {
				t.Errorf("connect adds a table [mcp_servers.mai], an invalid TOML file:\n%q", after)
			}
		})
	}
}

func TestCodexDisconnectKeepsASubTableOfMaiFromBeforeConnect(t *testing.T) {
	const user = "model = \"gpt-5-codex\"\n\n[mcp_servers.mai.env]\nMAI_LOG = \"debug\"\n"
	home, m := setup(t)
	cfg := filepath.Join(home, ".codex", "config.toml")
	writeAll(t, map[string]string{cfg: user})
	p, err := Prepare(home, "codex", "a")
	if errors.Is(err, ErrExists) {
		return // refused: no loss
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Apply(m); err != nil {
		t.Fatal(err)
	}
	err = Disconnect("codex", m)
	if got := readText(t, cfg); err != nil || got != user {
		t.Fatalf("connect and then disconnect of a file with a sub-table of the user: error %v\n%s", err, unifiedDiff("want", "got", user, got))
	}
}

// helpers (in the same file)
func addOldRecord(t *testing.T, m *manifest.Manifest, app, path, pointer string, value any, made []string) manifest.Item {
	t.Helper()
	text := encodeJSON(value, "")
	it := manifest.Item{Type: manifest.AppEntry, Name: app, Path: path, Pointer: pointer, Hash: hashOf(text)}
	dir, err := entriesDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	rb, err := json.Marshal(struct {
		Text    string   `json:"text"`
		Created []string `json:"created,omitempty"`
	}{string(text), made})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, recordName(it)), rb, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.Add(it); err != nil {
		t.Fatal(err)
	}
	return it
}

// forceDisconnect runs mai disconnect APP --force.
func forceDisconnect(t *testing.T, app string, m *manifest.Manifest) error {
	t.Helper()
	return DisconnectForce(app, m)
}

func TestAntigravityUpgradeFromTheHookSetEntry(t *testing.T) {
	const orig = "{\n  \"other\": {}\n}\n"
	hookSet := func(cmd string) (any, string) {
		v := obj("PreInvocation", []any{obj("type", "command", "command", cmd+" hook --format antigravity --event PreInvocation", "timeout", 10)})
		text := "{\n  \"other\": {},\n  \"mai\": " + strings.ReplaceAll(string(encodeJSON(v, "  ")), "\n", "\n  ") + "\n}\n"
		return v, text
	}

	t.Run("3.0.0 entry, no change of the user: disconnect after the new connect", func(t *testing.T) {
		home, m := setup(t)
		hooks := filepath.Join(home, ".gemini", "config", "hooks.json")
		v, text := hookSet("mai") // 3.0.0 wrote the bare name mai
		writeAll(t, map[string]string{hooks: text})
		addOldRecord(t, m, "antigravity", hooks, "/mai", v, nil)

		connectApp(t, home, "antigravity", m)
		err := Disconnect("antigravity", m)
		got := readText(t, hooks)
		if err != nil || got != orig {
			t.Fatalf("disconnect after the upgrade: error %v; hooks.json:\n%s", err, unifiedDiff("want", "got", orig, got))
		}
	})

	t.Run("3.0.1 entry, hook of the user next to the hook of mai: disconnect --force", func(t *testing.T) {
		home, m := setup(t)
		hooks := filepath.Join(home, ".gemini", "config", "hooks.json")
		v, text := hookSet(testBin)
		writeAll(t, map[string]string{hooks: text})
		addOldRecord(t, m, "antigravity", hooks, "/mai", v, nil)

		connectApp(t, home, "antigravity", m) // the upgrade: connect again with the new version
		b := readText(t, hooks)
		const maiHook = "--event PreInvocation\",\n        \"timeout\": 10\n      }"
		edited := strings.Replace(b, maiHook, maiHook+",\n      {\n        \"type\": \"command\",\n        \"command\": \"/usr/local/bin/notify-start\"\n      }", 1)
		if edited == b {
			t.Fatalf("the test could not edit the file:\n%s", b)
		}
		writeAll(t, map[string]string{hooks: edited})

		plain := Disconnect("antigravity", m)
		if err := forceDisconnect(t, "antigravity", m); err != nil {
			t.Fatal(err)
		}
		if got := readText(t, hooks); !strings.Contains(got, "notify-start") {
			t.Fatalf("disconnect gave %v, then disconnect --force deleted the hook of the user:\n%s", plain, unifiedDiff("before", "after", edited, got))
		}
	})
}

func TestDisconnectKeepsTheVersionAfterAnUpgradeFrom301(t *testing.T) {
	for _, c := range []struct{ app, path, from, to string }{
		{"copilot", filepath.Join(".copilot", "hooks", "mai.json"), "\"hooks\": {\n",
			"\"hooks\": {\n    \"userPromptSubmitted\": [\n      {\n        \"type\": \"command\",\n        \"bash\": \"./scripts/log-prompt.sh\",\n        \"timeoutSec\": 10\n      }\n    ],\n"},
		{"cursor", filepath.Join(".cursor", "hooks.json"), "\"hooks\": {\n",
			"\"hooks\": {\n    \"afterFileEdit\": [\n      {\n        \"command\": \"/usr/local/bin/format-file\"\n      }\n    ],\n"},
	} {
		t.Run(c.app, func(t *testing.T) {
			home, m := setup(t)
			hooks := filepath.Join(home, c.path)
			connectApp(t, home, c.app, m)
			// The state of 3.0.1: the same file, and a record of "version" with no if_absent.
			dir, err := entriesDir()
			if err != nil {
				t.Fatal(err)
			}
			for _, it := range m.Of(manifest.AppEntry) {
				if it.Path == hooks && it.Pointer == "/version" {
					if err := os.WriteFile(filepath.Join(dir, recordName(it)), []byte(`{"text":"1"}`), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			connectApp(t, home, c.app, m) // the upgrade: connect again with the new version

			b := readText(t, hooks)
			edited := strings.Replace(b, c.from, c.to, 1)
			if edited == b {
				t.Fatalf("the test could not edit the file:\n%s", b)
			}
			writeAll(t, map[string]string{hooks: edited})
			if err := Disconnect(c.app, m); err != nil {
				t.Fatal(err)
			}
			if got := readText(t, hooks); !strings.Contains(got, "\"version\"") {
				t.Fatalf("disconnect removes the version of a hooks file that holds a hook of the user:\n%s", got)
			}
		})
	}
}

func TestDisconnectKeepsTheVersionForAHookOfMaiThatStays(t *testing.T) {
	home, m := setup(t)
	hooks := filepath.Join(home, ".cursor", "hooks.json")
	connectCursor(t, home, m)
	b := readText(t, hooks)
	const stop = "--event stop\",\n        \"timeout\": 10,"
	edited := strings.Replace(b, stop, strings.Replace(stop, "10", "30", 1), 1)
	if edited == b {
		t.Fatalf("the test could not edit the file:\n%s", b)
	}
	writeAll(t, map[string]string{hooks: edited})

	err := Disconnect("cursor", m)
	var ce *ErrChanged
	if !errors.As(err, &ce) {
		t.Fatalf("disconnect of a changed hook gives %v, want *ErrChanged", err)
	}
	got := readText(t, hooks)
	if strings.Contains(got, "--event stop") && !strings.Contains(got, "\"version\"") {
		t.Fatalf("disconnect keeps the changed stop hook of mai and removes the version that it needs:\n%s", got)
	}
}

func TestDisconnectRightAfterConnectOfAnEntryInOtherKeyOrder(t *testing.T) {
	home, m := setup(t)
	mcp := filepath.Join(home, ".cursor", "mcp.json")
	const orig = "{\n  \"mcpServers\": {\n    \"github\": {\n      \"url\": \"https://api.githubcopilot.com/mcp/\"\n    }\n  }\n}\n"
	writeAll(t, map[string]string{mcp: orig})
	useBinary(t, "/Users/a/go/bin/mai")
	connectCursor(t, home, m)
	sorted := strings.Replace(readText(t, mcp),
		"\"mai\": {\n      \"command\": \"/Users/a/go/bin/mai\",\n      \"args\": [\n        \"mcp\",\n        \"--agent\",\n        \"cursor\"\n      ]\n    }",
		"\"mai\": {\n      \"args\": [\n        \"mcp\",\n        \"--agent\",\n        \"cursor\"\n      ],\n      \"command\": \"/Users/a/go/bin/mai\"\n    }", 1)
	if sorted == readText(t, mcp) {
		t.Fatalf("the test could not edit the file:\n%s", sorted)
	}
	writeAll(t, map[string]string{mcp: sorted})

	useBinary(t, testBin) // the binary moved: connect again
	connectCursor(t, home, m)
	written := readText(t, mcp)
	err := Disconnect("cursor", m)
	got := readText(t, mcp)
	if err != nil || got != orig {
		t.Fatalf("disconnect right after connect wrote:\n%s\ngives the error %v and:\n%s", written, err, unifiedDiff("want", "got", orig, got))
	}
}

func TestConnectThroughADanglingRelativeLinkInALinkedFolder(t *testing.T) {
	home, m := setup(t)
	dot := filepath.Join(home, "dotfiles", "cursor")
	if err := os.MkdirAll(dot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dot, filepath.Join(home, ".cursor")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "shared", "mcp.json"), filepath.Join(dot, "mcp.json")); err != nil {
		t.Fatal(err)
	}
	connectCursor(t, home, m)

	b, err := os.ReadFile(filepath.Join(home, ".cursor", "mcp.json"))
	_, stray := os.Stat(filepath.Join(home, "shared", "mcp.json"))
	if err != nil || !strings.Contains(string(b), testBin) || stray == nil {
		t.Fatalf("Cursor reads ~/.cursor/mcp.json: %v %q; a file ~/shared/mcp.json exists: %v", err, b, stray == nil)
	}
}

func TestForceDisconnectKeepsAServerOfTheUserNamedMai(t *testing.T) {
	home, m := setup(t)
	mcp := filepath.Join(home, ".cursor", "mcp.json")
	connectCursor(t, home, m)
	const mine = "{\n  \"mcpServers\": {\n    \"mai\": {\n      \"command\": \"npx\",\n      \"args\": [\"-y\", \"@acme/mail-ai-server\"]\n    }\n  }\n}\n"
	writeAll(t, map[string]string{mcp: mine})

	if err := forceDisconnect(t, "cursor", m); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(mcp)
	if err != nil || !strings.Contains(string(got), "@acme/mail-ai-server") {
		t.Fatalf("disconnect --force removes a server of the user that is not mai (read error %v):\n%s", err, unifiedDiff("before", "after", mine, string(got)))
	}
}
