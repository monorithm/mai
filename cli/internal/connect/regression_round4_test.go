package connect

// More cases of the TOML scanner, of --force, and of the records of version, each with a
// concrete input.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/manifest"
)

func TestCodexForceDisconnectKeepsAServerOfTheUserNamedMai(t *testing.T) {
	const user = "model = \"gpt-5-codex\"\n"
	const server = "[mcp_servers.mai]\ncommand = \"npx\"\nargs = [\"-y\", \"@acme/mail-ai-server\"]\n"
	for _, c := range []struct{ name, mine string }{
		{"the user changed the table of mai", "\n" + tomlBegin + server},
		{"the user replaced the block of mai", "\n" + server},
	} {
		t.Run(c.name, func(t *testing.T) {
			home, m := setup(t)
			cfg := filepath.Join(home, ".codex", "config.toml")
			writeAll(t, map[string]string{cfg: user})
			connectApp(t, home, "codex", m)
			edited := user + c.mine
			writeAll(t, map[string]string{cfg: edited})
			forceRemoveTOML(t, m)
			if got := readText(t, cfg); !strings.Contains(got, "@acme/mail-ai-server") {
				t.Fatalf("mai disconnect --force removes a server of the user that is not mai:\n%s", unifiedDiff("before", "after", edited, got))
			}
		})
	}
}

func TestCodexScannerReadsAnEscapedQuoteInAMultiLineString(t *testing.T) {
	const user = "developer_instructions = \"\"\"\nWrite a docstring in \\\"\"\" quotes for each public function.\n\"\"\"\n"
	home, m := setup(t)
	cfg := filepath.Join(home, ".codex", "config.toml")
	writeAll(t, map[string]string{cfg: user})
	connectApp(t, home, "codex", m)

	p, err := Prepare(home, "codex", "a")
	if err == nil {
		if after := fileAfter(t, p, cfg); strings.Count(after, "[mcp_servers.mai]") != 1 {
			t.Errorf("a second mai connect writes a second table [mcp_servers.mai], an invalid TOML file:\n%s", after)
		}
	}
	err = Disconnect("codex", m)
	if got := readText(t, cfg); err != nil || got != user {
		t.Fatalf("disconnect gives the error %v and does not give back the file:\n%s", err, unifiedDiff("want", "got", user, got))
	}
}

func TestCodexReconnectOfAHeaderAtTheEndOfTheFile(t *testing.T) {
	home, m := setup(t)
	cfg := filepath.Join(home, ".codex", "config.toml")
	writeAll(t, map[string]string{cfg: "model = \"gpt-5-codex\"\n"})
	connectApp(t, home, "codex", m)
	writeAll(t, map[string]string{cfg: "model = \"gpt-5-codex\"\n\n" + tomlBegin + "[mcp_servers.mai]"})

	var p *Plan
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("mai connect panics: %v", r)
			}
		}()
		p, err = Prepare(home, "codex", "a")
	}()
	if err != nil {
		return // refused: no loss
	}
	if after := fileAfter(t, p, cfg); strings.Contains(after, "]command") {
		t.Fatalf("mai connect joins the header and the key, an invalid TOML file:\n%q", after)
	}
}

func TestCodexDisconnectAfterReconnectOfANestedArrayInTheTableOfMai(t *testing.T) {
	const user = "model = \"gpt-5-codex\"\n"
	home, m := setup(t)
	cfg := filepath.Join(home, ".codex", "config.toml")
	writeAll(t, map[string]string{cfg: user})
	connectApp(t, home, "codex", m)
	writeAll(t, map[string]string{cfg: readText(t, cfg) + "tool_groups = [\n  [\"send\", \"read\"],\n]\n"})
	useBinary(t, "/usr/local/bin/mai") // the binary moved: connect again
	connectApp(t, home, "codex", m)
	written := readText(t, cfg)

	err := Disconnect("codex", m)
	if got := readText(t, cfg); err == nil && got != user {
		t.Fatalf("disconnect of:\n%s\nleaves part of the table of mai, an invalid TOML file:\n%s", written, unifiedDiff("want", "got", user, got))
	}
}

func TestCodexDisconnectOfA30xBlockInACRLFFile(t *testing.T) {
	const user = "model = \"gpt-5-codex\"\r\n\r\n[mcp_servers.github]\r\ncommand = \"npx\"\r\n"
	for _, command := range []string{"mai", testBin} {
		t.Run(command, func(t *testing.T) {
			old := "\n" + tomlBegin + "[mcp_servers.mai]\ncommand = \"" + command + "\"\nargs = [\"mcp\", \"--agent\", \"codex\"]\n" + tomlEnd
			home, m := setup(t)
			cfg := filepath.Join(home, ".codex", "config.toml")
			if err := m.Add(manifest.Item{Type: manifest.AppEntry, Name: "codex", Path: cfg, Pointer: TOMLPointer, Hash: hashOf([]byte(old))}); err != nil {
				t.Fatal(err)
			}
			writeAll(t, map[string]string{cfg: user + old})
			err := Disconnect("codex", m)
			var ce *ErrChanged
			if got := readText(t, cfg); err != nil || got != user {
				t.Fatalf("disconnect of the unchanged block (ErrChanged: %v): %v\n%s", errors.As(err, &ce), err, unifiedDiff("want", "got", user, got))
			}
		})
	}
}

func TestCodexReconnectKeepsACommentOfTheUserOnTheCommandLine(t *testing.T) {
	home, m := setup(t)
	cfg := filepath.Join(home, ".codex", "config.toml")
	writeAll(t, map[string]string{cfg: "model = \"gpt-5-codex\"\n"})
	connectApp(t, home, "codex", m)
	b := readText(t, cfg)
	edited := strings.Replace(b, "command = \""+testBin+"\"\n", "command = \""+testBin+"\" # installed with go install\n", 1)
	if edited == b {
		t.Fatalf("the test could not edit the file:\n%s", b)
	}
	writeAll(t, map[string]string{cfg: edited})

	p, err := Prepare(home, "codex", "a")
	if err != nil {
		t.Fatal(err)
	}
	if after := fileAfter(t, p, cfg); !strings.Contains(after, "# installed with go install") {
		t.Fatalf("a second mai connect deletes the comment of the user:\n%s", unifiedDiff("before", "after", edited, after))
	}
}

// cli/internal/connect/round4_json_test.go (package connect; imports errors, os, path/filepath, strings, testing, internal/manifest; uses addOldRecord and forceDisconnect from regression_round3_test.go)
func TestForceDisconnectOfA300HookSetKeepsTheHooksOfTheUser(t *testing.T) {
	maiHook := obj("type", "command", "command", "mai hook --format antigravity --event PreInvocation", "timeout", 10)
	set := obj("PreInvocation", []any{maiHook})
	written := "{\n  \"other\": {},\n  \"mai\": " + strings.ReplaceAll(string(encodeJSON(set, "  ")), "\n", "\n  ") + "\n}\n"
	const maiEnd = "--event PreInvocation\",\n        \"timeout\": 10\n      }\n    ]"
	for _, c := range []struct{ name, to string }{
		{"a hook of the user next to the hook of mai",
			strings.TrimSuffix(maiEnd, "\n    ]") + ",\n      {\n        \"type\": \"command\",\n        \"command\": \"/usr/local/bin/notify-start\"\n      }\n    ]"},
		{"a hook of the user for a second event",
			maiEnd + ",\n    \"PostInvocation\": [\n      {\n        \"type\": \"command\",\n        \"command\": \"/usr/local/bin/notify-start\"\n      }\n    ]"},
	} {
		t.Run(c.name, func(t *testing.T) {
			home, m := setup(t)
			hooks := filepath.Join(home, ".gemini", "config", "hooks.json")
			edited := strings.Replace(written, maiEnd, c.to, 1)
			if edited == written {
				t.Fatalf("the test could not edit the file:\n%s", written)
			}
			writeAll(t, map[string]string{hooks: edited})
			addOldRecord(t, m, "antigravity", hooks, "/mai", set, nil)

			var ce *ErrChanged
			if err := Disconnect("antigravity", m); !errors.As(err, &ce) {
				t.Fatalf("disconnect of a changed hook set: %v, want ErrChanged", err)
			}
			if err := forceDisconnect(t, "antigravity", m); err != nil {
				t.Fatal(err)
			}
			if got := readText(t, hooks); !strings.Contains(got, "notify-start") {
				t.Fatalf("mai disconnect --force deletes the hook of the user:\n%s", unifiedDiff("before", "after", edited, got))
			}
		})
	}
}

func TestForceDisconnectKeepsAServerOfTheUserWhoseProgramIsNamedMai(t *testing.T) {
	home, m := setup(t)
	mcp := filepath.Join(home, ".cursor", "mcp.json")
	connectCursor(t, home, m)
	const mine = "{\n  \"mcpServers\": {\n    \"mai\": {\n      \"command\": \"/Users/a/.npm-global/bin/mai\",\n      \"args\": [\"serve\", \"--stdio\"]\n    }\n  }\n}\n"
	writeAll(t, map[string]string{mcp: mine})
	if _, err := Prepare(home, "cursor", "cursor"); !errors.Is(err, ErrExists) {
		t.Fatalf("connect over the server of the user: %v, want ErrExists", err)
	}

	if err := forceDisconnect(t, "cursor", m); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(mcp)
	if err != nil || !strings.Contains(string(got), "/Users/a/.npm-global/bin/mai") {
		t.Fatalf("mai disconnect --force removes a server that mai connect calls a different entry (read error %v):\n%s", err, unifiedDiff("before", "after", mine, string(got)))
	}
}

func TestForceDisconnectAfterAChangedHookRemovesTheHooksFileThatConnectMade(t *testing.T) {
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

	var ce *ErrChanged
	if err := Disconnect("cursor", m); !errors.As(err, &ce) {
		t.Fatalf("disconnect of a changed hook: %v, want ErrChanged", err)
	}
	// mai disconnect cursor --force, in the order of the removal.
	for _, it := range RemovalOrder(m.Of(manifest.AppEntry)) {
		if it.Name != "cursor" {
			continue
		}
		if err := ForceRemoveEntry(it); err != nil {
			t.Fatal(err)
		}
		if err := m.Remove(it); err != nil {
			t.Fatal(err)
		}
	}
	if err := Disconnect("cursor", m); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(hooks); err == nil {
		t.Fatalf("no entry of mai stays, but the hooks file that mai made stays (%d file records in the manifest):\n%s", len(m.Of(manifest.File)), got)
	}
}

func TestDisconnectAfterTheUserRemovedTheHooksOfMaiRemovesTheFileThatConnectMade(t *testing.T) {
	home, m := setup(t)
	hooks := filepath.Join(home, ".cursor", "hooks.json")
	connectCursor(t, home, m)
	writeAll(t, map[string]string{hooks: "{\n  \"version\": 1,\n  \"hooks\": {}\n}\n"})

	if err := Disconnect("cursor", m); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(hooks); err == nil {
		t.Fatalf("the hooks file that mai made stays with the object that mai made (%d file records in the manifest):\n%s", len(m.Of(manifest.File)), got)
	}
}
