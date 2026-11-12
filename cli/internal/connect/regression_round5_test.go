package connect

// More cases of the hooks of the user, the records of a file, and the TOML file of Codex,
// each with a concrete input.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/manifest"
)

// cli/internal/connect/round5_json_test.go (package connect; imports errors, os, path/filepath, strings, testing, internal/manifest)
func TestReconnectKeepsAKeyThatTheUserAddedToAHookOfMai(t *testing.T) {
	for _, c := range []struct{ app, rel, from, added string }{
		{"claude", filepath.Join(".claude", "settings.json"),
			"--event Stop\",\n            \"timeout\": 10",
			",\n            \"statusMessage\": \"Checking mail\""},
		{"copilot", filepath.Join(".copilot", "hooks", "mai.json"),
			"--event agentStop\",\n        \"timeoutSec\": 10",
			",\n        \"env\": {\n          \"MAI_LOG\": \"debug\"\n        }"},
		{"cursor", filepath.Join(".cursor", "hooks.json"),
			"--event postToolUse\",\n        \"timeout\": 10",
			",\n        \"matcher\": \"Shell\""},
	} {
		t.Run(c.app, func(t *testing.T) {
			home, m := setup(t)
			path := filepath.Join(home, c.rel)
			connectApp(t, home, c.app, m)
			b := readText(t, path)
			edited := strings.Replace(b, c.from, c.from+c.added, 1)
			if edited == b {
				t.Fatalf("the test could not edit the file:\n%s", b)
			}
			writeAll(t, map[string]string{path: edited})

			useBinary(t, "/Users/a/.local/bin/mai") // the binary moved: connect again
			connectApp(t, home, c.app, m)
			got := readText(t, path)
			if !strings.Contains(got, "/Users/a/.local/bin/mai hook") {
				t.Fatalf("connect after the move keeps the old path:\n%s", got)
			}
			if !strings.Contains(got, strings.TrimPrefix(c.added, ",")) {
				t.Fatalf("connect after the move deletes the key that the user added to the hook of mai:\n%s", unifiedDiff("before", "after", edited, got))
			}
		})
	}
}

// cli/internal/connect/round5_json_test.go (package connect)
const claudeUserSettings = `{
  "$schema": "https://json.schemastore.org/claude-code-settings.json",
  "permissions": {
    "allow": [
      "Bash(npm run lint)",
      "Bash(npm run test:*)"
    ],
    "deny": [
      "Read(./.env)"
    ]
  },
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "Edit|MultiEdit|Write",
        "hooks": [
          {
            "type": "command",
            "command": "npx prettier --write \"$CLAUDE_FILE_PATHS\""
          }
        ]
      }
    ]
  },
  "statusLine": {
    "type": "command",
    "command": "~/.claude/statusline.sh"
  },
  "model": "opus"
}
`

func TestForceDisconnectRemovesTheGroupOfMaiThatHasAMatcherOfTheUser(t *testing.T) {
	home, m := setup(t)
	settings := filepath.Join(home, ".claude", "settings.json")
	writeAll(t, map[string]string{settings: claudeUserSettings})
	connectApp(t, home, "claude", m)
	b := readText(t, settings)
	const group = "      {\n        \"hooks\": [\n          {\n            \"type\": \"command\",\n            \"command\": \"" + testBin + " hook --format claude --event PostToolUse\""
	edited := strings.Replace(b, group, strings.Replace(group, "      {\n", "      {\n        \"matcher\": \"Edit|Write\",\n", 1), 1)
	if edited == b {
		t.Fatalf("the test could not edit the file:\n%s", b)
	}
	writeAll(t, map[string]string{settings: edited})

	var ce *ErrChanged
	if err := Disconnect("claude", m); !errors.As(err, &ce) {
		t.Fatalf("disconnect of the changed group of mai: %v, want *ErrChanged", err)
	}
	if err := forceDisconnect(t, "claude", m); err != nil {
		t.Fatal(err)
	}
	if got := readText(t, settings); got != claudeUserSettings {
		t.Fatalf("settings.json after mai disconnect --force:\n%s", unifiedDiff("want", "got", claudeUserSettings, got))
	}
}

// cli/internal/connect/round5_json_test.go (package connect)
func TestLaterDisconnectRemovesTheContainerThatMaiMade(t *testing.T) {
	t.Run("claude: a changed hook stays, then connect and disconnect", func(t *testing.T) {
		home, m := setup(t)
		settings := filepath.Join(home, ".claude", "settings.json")
		const orig = "{\n  \"model\": \"opus\"\n}\n"
		writeAll(t, map[string]string{settings: orig})
		connectApp(t, home, "claude", m)
		b := readText(t, settings)
		const stop = "--event Stop\",\n            \"timeout\": 10"
		edited := strings.Replace(b, stop, strings.Replace(stop, "10", "30", 1), 1)
		if edited == b {
			t.Fatalf("the test could not edit the file:\n%s", b)
		}
		writeAll(t, map[string]string{settings: edited})
		var ce *ErrChanged
		if err := Disconnect("claude", m); !errors.As(err, &ce) {
			t.Fatalf("disconnect of a changed hook: %v, want *ErrChanged", err)
		}

		connectApp(t, home, "claude", m) // the user connects again
		if err := Disconnect("claude", m); err != nil {
			t.Fatal(err)
		}
		if got := readText(t, settings); got != orig {
			t.Fatalf("settings.json after connect, disconnect, connect, disconnect:\n%s", unifiedDiff("want", "got", orig, got))
		}
	})

	t.Run("cursor: the version stays for a hook of the user, then the user removes the hook", func(t *testing.T) {
		home, m := setup(t)
		hooks := filepath.Join(home, ".cursor", "hooks.json")
		connectCursor(t, home, m) // mai makes hooks.json
		b := readText(t, hooks)
		edited := strings.Replace(b, "\"hooks\": {\n", "\"hooks\": {\n    \"afterFileEdit\": [\n      {\n        \"command\": \"/usr/local/bin/format-file\"\n      }\n    ],\n", 1)
		if edited == b {
			t.Fatalf("the test could not edit the file:\n%s", b)
		}
		writeAll(t, map[string]string{hooks: edited})
		if err := Disconnect("cursor", m); err != nil {
			t.Fatal(err)
		}

		// The user removes the hook of their own.
		writeAll(t, map[string]string{hooks: "{\n  \"version\": 1,\n  \"hooks\": {}\n}\n"})
		if err := Disconnect("cursor", m); err != nil {
			t.Fatal(err)
		}
		if got, err := os.ReadFile(hooks); err == nil {
			t.Fatalf("no entry of mai and no content of the user stays, but the hooks file that mai made stays (%d file records in the manifest):\n%s", len(m.Of(manifest.File)), got)
		}
	})
}

// cli/internal/connect/round5_json_test.go (package connect)
func TestConnectThenDisconnectOfAHooksFileWithNoVersion(t *testing.T) {
	home, m := setup(t)
	hooks := filepath.Join(home, ".cursor", "hooks.json")
	const orig = "{\n  \"hooks\": {\n    \"afterFileEdit\": [\n      {\n        \"command\": \"/usr/local/bin/format-file\"\n      }\n    ]\n  }\n}\n"
	writeAll(t, map[string]string{hooks: orig})
	connectCursor(t, home, m)
	err := Disconnect("cursor", m)
	if got := readText(t, hooks); err != nil || got != orig {
		t.Fatalf("connect and then disconnect: error %v; hooks.json:\n%s", err, unifiedDiff("want", "got", orig, got))
	}
}

// shopCodexConfig is a config.toml of Codex as a user has it: keys of the root table, a notify
// array on more than one line, developer instructions in a multi-line string with brackets
// and escaped quotes, profiles, projects, and servers with an env sub-table.
const shopCodexConfig = `model = "gpt-5-codex"
model_reasoning_effort = "high"
approval_policy = "on-request"
notify = [
  "python3",
  "/Users/a/.codex/notify.py", # [notify] script
]
developer_instructions = """
Rules of the shop:
[1] Run "make test" before each commit.
[2] Write \"""docstrings\""" for each public function.
"""

[profiles.fast]
model = "gpt-5-codex-mini"

[projects."/Users/a/src/shop"]
trust_level = "trusted"

[projects.'/Users/a/src/[old] site']
trust_level = "untrusted"

[mcp_servers.github]
command = "npx"
args = ["-y", "@modelcontextprotocol/server-github"]

[mcp_servers.github.env]
GITHUB_PERSONAL_ACCESS_TOKEN = "x"
`

// For an MCP entry in JSON, mai connect and mai disconnect --force take a server named mai as
// the entry of mai only when its program is mai and its first argument is "mcp". A server of
// the user that runs an other program with the name mai ("mai serve --stdio") is not mai.
// The same server in the config.toml of Codex must stay too.
func TestCodexKeepsAServerOfTheUserWhoseProgramIsNamedMai(t *testing.T) {
	const program = "/Users/a/.npm-global/bin/mai"
	const server = "[mcp_servers.mai]\ncommand = \"" + program + "\"\nargs = [\"serve\", \"--stdio\"]\n"
	for _, c := range []struct{ name, mine string }{
		{"the user replaced the block of mai", "\n" + server},
		{"the user changed the table of mai", "\n" + tomlBegin + server},
	} {
		t.Run(c.name, func(t *testing.T) {
			home, m := setup(t)
			cfg := filepath.Join(home, ".codex", "config.toml")
			writeAll(t, map[string]string{cfg: shopCodexConfig})
			connectApp(t, home, "codex", m)
			edited := shopCodexConfig + c.mine
			writeAll(t, map[string]string{cfg: edited})

			if p, err := Prepare(home, "codex", "a"); err == nil {
				if after := fileAfter(t, p, cfg); !strings.Contains(after, program) {
					t.Errorf("mai connect replaces a server of the user that is not mai:\n%s", unifiedDiff("before", "after", edited, after))
				}
			} else if !errors.Is(err, ErrExists) {
				t.Fatal(err)
			}
			forceRemoveTOML(t, m)
			if got := readText(t, cfg); !strings.Contains(got, program) {
				t.Fatalf("mai disconnect codex --force removes a server of the user that is not mai:\n%s", unifiedDiff("before", "after", edited, got))
			}
		})
	}
}

// codexWrite gives the text that Codex writes when it writes config.toml again with
// toml_edit: toml_edit writes "\n" after each key line and each header line, and keeps the
// raw text of each blank line and each comment line. The input must have no multi-line value.
func codexWrite(s string) string {
	var b strings.Builder
	for _, l := range strings.SplitAfter(s, "\n") {
		if l == "" {
			continue
		}
		line := strings.TrimRight(l, "\r\n")
		if t := strings.TrimSpace(line); t == "" || strings.HasPrefix(t, "#") {
			b.WriteString(l)
			continue
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

// A config.toml with CRLF (a new file of VS Code on Windows) gets the block of mai with CRLF.
// When the user trusts a project folder, Codex writes the file again with toml_edit and adds
// a table. The table of mai is the same as mai wrote it, thus mai disconnect must remove it.
func TestCodexDisconnectAfterCodexWroteAFileWithCRLF(t *testing.T) {
	const user = "model = \"gpt-5-codex\"\r\n\r\n# Servers of the shop\r\n[mcp_servers.github]\r\ncommand = \"npx\"\r\n"
	const trusted = "\n[projects.\"C:\\\\Users\\\\a\\\\src\\\\shop\"]\ntrust_level = \"trusted\"\n"
	home, m := setup(t)
	cfg := filepath.Join(home, ".codex", "config.toml")
	writeAll(t, map[string]string{cfg: user})
	connectApp(t, home, "codex", m)
	written := codexWrite(readText(t, cfg)) + trusted
	writeAll(t, map[string]string{cfg: written})

	err := Disconnect("codex", m)
	var ce *ErrChanged
	want := codexWrite(user) + trusted
	if got := readText(t, cfg); err != nil || got != want {
		t.Fatalf("disconnect of the unchanged table of mai in:\n%q\ngives the error %v (ErrChanged: %v):\n%s", written, err, errors.As(err, &ce), unifiedDiff("want", "got", want, got))
	}
}

// A table [mcp_servers.mai] with no begin comment of mai stays from mai 2.x (the uninstall of
// 2.x does not change the files of the apps), or from "codex mcp add mai". A user can also
// change the table of mai to a different program. mai connect refuses each such table, and
// its error says to run mai disconnect codex --force. When the error names that command, the
// command must make the next mai connect possible.
func TestCodexConnectAfterTheForcedDisconnectThatItsErrorNames(t *testing.T) {
	for _, c := range []struct {
		name, table string
		connected   bool // mai connect ran before the user wrote the table
	}{
		{"a table of mai 2.x", "[mcp_servers.mai]\ncommand = \"/Users/a/.mai/bin/mai\"\n", false},
		{"a table of codex mcp add", "[mcp_servers.mai]\ncommand = \"mai\"\nargs = [\"mcp\", \"--agent\", \"a\"]\n", false},
		{"the user changed the program of the table of mai", tomlBegin + "[mcp_servers.mai]\ncommand = \"npx\"\nargs = [\"-y\", \"@acme/mail-ai-server\"]\n", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			home, m := setup(t)
			cfg := filepath.Join(home, ".codex", "config.toml")
			if c.connected {
				writeAll(t, map[string]string{cfg: shopCodexConfig})
				connectApp(t, home, "codex", m)
			}
			before := shopCodexConfig + "\n" + c.table
			writeAll(t, map[string]string{cfg: before})
			_, err := Prepare(home, "codex", "a")
			if !errors.Is(err, ErrExists) {
				t.Fatalf("connect over a table of mai that mai 3 did not write: %v, want ErrExists", err)
			}
			const hint = "mai disconnect codex --force"
			if !strings.Contains(err.Error(), hint) {
				return // the error does not name the command
			}
			if err := forceDisconnect(t, "codex", m); err != nil {
				t.Fatal(err)
			}
			if _, err := Prepare(home, "codex", "a"); err != nil {
				t.Fatalf("mai connect says to run %q; after that command, mai connect gives: %v\nconfig.toml:\n%s", hint, err, unifiedDiff("before", "after", before, readText(t, cfg)))
			}
		})
	}
}

// mai connect writes a line break and then its block. When the last line of the user has
// only white space and no line break (or the file has only a byte order mark), that line
// stays a line of the user, and mai disconnect must give it back.
func TestCodexDisconnectKeepsALastLineOfOnlyWhiteSpace(t *testing.T) {
	for _, c := range []struct{ name, user string }{
		{"byte order mark only", "\xEF\xBB\xBF"},
		{"a tab after the last line break", shopCodexConfig + "\t"},
		{"spaces after the last CRLF", strings.ReplaceAll(shopCodexConfig, "\n", "\r\n") + "  "},
	} {
		t.Run(c.name, func(t *testing.T) {
			home, m := setup(t)
			cfg := filepath.Join(home, ".codex", "config.toml")
			writeAll(t, map[string]string{cfg: c.user})
			connectApp(t, home, "codex", m)
			err := Disconnect("codex", m)
			if got := readText(t, cfg); err != nil || got != c.user {
				t.Fatalf("disconnect gives the error %v and does not give back the file: the end of the file is %q, want %q", err, got[max(0, len(got)-8):], c.user[max(0, len(c.user)-8):])
			}
		})
	}
}
