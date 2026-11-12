package connect

// More cases of links, folders, line breaks, and byte order marks, each with a concrete input.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// file cli/internal/connect/round6_toml_test.go, package connect; imports errors, path/filepath, strings, testing
func TestCodexDisconnectOfATableThatASecondConnectWroteInAFileWithTwoKindsOfLineBreak(t *testing.T) {
	const user = "model = \"gpt-5-codex\"\r\n\r\n# Servers of the shop\r\n[mcp_servers.github]\r\ncommand = \"npx\"\r\n"
	const shop = "\n[projects.\"C:\\\\Users\\\\a\\\\src\\\\shop\"]\ntrust_level = \"trusted\"\n"
	const blog = "\n[projects.\"C:\\\\Users\\\\a\\\\src\\\\blog\"]\ntrust_level = \"trusted\"\n"
	for _, c := range []struct {
		name  string
		moved bool                // the binary moves before the second connect
		after func(string) string // the change of the file after the second connect
		want  func() string
	}{
		{"the binary moved, then Codex wrote the file again", true,
			func(s string) string { return codexWrite(s) + blog },
			func() string { return codexWrite(user) + shop + blog }},
		{"connect again, then the file got LF line breaks", false,
			func(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") },
			func() string { return strings.ReplaceAll(codexWrite(user)+shop, "\r\n", "\n") }},
		{"connect again, then the file got CRLF line breaks", false,
			func(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n") },
			func() string {
				return strings.ReplaceAll(strings.ReplaceAll(codexWrite(user)+shop, "\r\n", "\n"), "\n", "\r\n")
			}},
	} {
		t.Run(c.name, func(t *testing.T) {
			home, m := setup(t)
			cfg := filepath.Join(home, ".codex", "config.toml")
			writeAll(t, map[string]string{cfg: user})
			connectApp(t, home, "codex", m)
			writeAll(t, map[string]string{cfg: codexWrite(readText(t, cfg)) + shop})
			if c.moved {
				useBinary(t, "/usr/local/bin/mai")
			}
			connectApp(t, home, "codex", m)
			written := c.after(readText(t, cfg))
			writeAll(t, map[string]string{cfg: written})

			err := Disconnect("codex", m)
			var ce *ErrChanged
			want := c.want()
			if got := readText(t, cfg); err != nil || got != want {
				t.Fatalf("disconnect of the unchanged table of mai in:\n%q\ngives the error %v (ErrChanged: %v):\n%s", written, err, errors.As(err, &ce), unifiedDiff("want", "got", want, got))
			}
		})
	}
}

// file cli/internal/connect/round6_toml_test.go, package connect; imports errors, path/filepath, strings, testing
func TestCodexForceDisconnectOfATableOfMaiWithArgsOnMoreThanOneLine(t *testing.T) {
	home, m := setup(t)
	cfg := filepath.Join(home, ".codex", "config.toml")
	writeAll(t, map[string]string{cfg: shopCodexConfig})
	connectApp(t, home, "codex", m)
	b := readText(t, cfg)
	const one = `args = ["mcp", "--agent", "a"]`
	edited := strings.Replace(b, one, "args = [\n  \"mcp\",\n  \"--agent\", \"a\",\n  \"--log-level\", \"debug\",\n]", 1)
	if edited == b {
		t.Fatalf("the test could not edit the file:\n%s", b)
	}
	writeAll(t, map[string]string{cfg: edited})

	if err := forceDisconnect(t, "codex", m); err != nil {
		t.Fatal(err)
	}
	if got := readText(t, cfg); got != shopCodexConfig {
		t.Fatalf("mai disconnect codex --force reports success and keeps the table of mai:\n%s", unifiedDiff("want", "got", shopCodexConfig, got))
	}
}

// file cli/internal/connect/round6_toml_test.go, package connect; imports errors, path/filepath, strings, testing
func TestCodexForceDisconnectRemovesTheBeginCommentOfATableThatIsNotMarked(t *testing.T) {
	const mine = "# mail for the agents of the shop"
	for _, c := range []struct{ name, between string }{
		{"a comment of the user", mine + "\n"},
		{"a blank line", "\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			home, m := setup(t)
			cfg := filepath.Join(home, ".codex", "config.toml")
			writeAll(t, map[string]string{cfg: shopCodexConfig})
			connectApp(t, home, "codex", m)
			b := readText(t, cfg)
			edited := strings.Replace(b, tomlBegin, tomlBegin+c.between, 1)
			if edited == b {
				t.Fatalf("the test could not edit the file:\n%s", b)
			}
			writeAll(t, map[string]string{cfg: edited})

			if err := forceDisconnect(t, "codex", m); err != nil {
				t.Fatal(err)
			}
			got := readText(t, cfg)
			if strings.Contains(got, "[mcp_servers.mai]") {
				return // the table stays: a different case
			}
			begin := strings.TrimRight(tomlBegin, "\n")
			if strings.Contains(got, begin) {
				connectApp(t, home, "codex", m)
				t.Fatalf("mai disconnect codex --force removes the table of mai and keeps its begin comment; a new connect gives %d begin comments:\n%s",
					strings.Count(readText(t, cfg), begin), unifiedDiff("before", "after", edited, got))
			}
			if strings.Contains(c.between, mine) && !strings.Contains(got, mine+"\n") {
				t.Fatalf("mai disconnect codex --force removes the line break of the comment of the user:\n%q", got[max(0, len(got)-60):])
			}
		})
	}
}

// mai connect makes ~/.cursor/mcp.json. Then the user installs the dotfiles: ~/.cursor/mcp.json
// becomes a link to the file of the user in the dotfiles folder (ln -sf, or GNU Stow). mai
// did not make that file, and it holds no entry of mai. mai disconnect must not remove it.
func TestDisconnectKeepsADotfileThatTheUserLinkedAfterConnect(t *testing.T) {
	home, m := setup(t)
	mcp := filepath.Join(home, ".cursor", "mcp.json")
	connectCursor(t, home, m) // mai makes ~/.cursor/mcp.json
	const mine = "{\n  \"mcpServers\": {}\n}\n"
	dot := filepath.Join(home, "dotfiles", "cursor", "mcp.json")
	writeAll(t, map[string]string{dot: mine})
	if err := os.Remove(mcp); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dot, mcp); err != nil {
		t.Fatal(err)
	}

	err := Disconnect("cursor", m)
	got, rerr := os.ReadFile(dot)
	if err != nil || rerr != nil || string(got) != mine {
		t.Fatalf("mai disconnect cursor gives the error %v; the dotfile of the user %s: read error %v, text %q, want %q", err, dot, rerr, got, mine)
	}
}

// mai connect, then the user changes the timeout of one hook of mai. mai disconnect keeps that
// hook (*ErrChanged), removes the other entries, and forgets their records. The user connects
// again, then disconnects. Before the first connect, the file was empty, or had an empty
// container with bytes between its brackets. mai disconnect must give back those bytes, as
// it does with no change of the user in between.
func TestDisconnectAfterAPartialDisconnectAndAConnectKeepsTheBytesOfAnEmptyContainer(t *testing.T) {
	const claudeStop = "--event Stop\",\n            \"timeout\": 10"
	const cursorStop = "--event stop\",\n        \"timeout\": 10"
	for _, c := range []struct{ name, app, rel, orig, stop string }{
		{"claude: an empty settings.json", "claude", filepath.Join(".claude", "settings.json"), "", claudeStop},
		{"claude: a root object on two lines", "claude", filepath.Join(".claude", "settings.json"), "{\n}\n", claudeStop},
		{"claude: an empty hooks object on two lines", "claude", filepath.Join(".claude", "settings.json"), "{\n  \"model\": \"opus\",\n  \"hooks\": {\n  }\n}\n", claudeStop},
		{"cursor: an empty hooks object with a space", "cursor", filepath.Join(".cursor", "hooks.json"), "{\n  \"version\": 1,\n  \"hooks\": { }\n}\n", cursorStop},
	} {
		t.Run(c.name, func(t *testing.T) {
			home, m := setup(t)
			path := filepath.Join(home, c.rel)
			writeAll(t, map[string]string{path: c.orig})
			connectApp(t, home, c.app, m)
			b := readText(t, path)
			edited := strings.Replace(b, c.stop, strings.Replace(c.stop, "10", "30", 1), 1)
			if edited == b {
				t.Fatalf("the test could not edit the file:\n%s", b)
			}
			writeAll(t, map[string]string{path: edited})
			var ce *ErrChanged
			if err := Disconnect(c.app, m); !errors.As(err, &ce) {
				t.Fatalf("disconnect of a changed hook: %v, want *ErrChanged", err)
			}

			connectApp(t, home, c.app, m) // the user connects again
			if err := Disconnect(c.app, m); err != nil {
				t.Fatal(err)
			}
			if got := readText(t, path); got != c.orig {
				t.Fatalf("%s after connect, disconnect, connect, disconnect: got %q, want %q", filepath.Base(path), got, c.orig)
			}
		})
	}
}

// A JSON file with a UTF-8 byte order mark, as Windows PowerShell 5.1 writes it with
// "Out-File -Encoding utf8" and as Visual Studio Code reads it. mai connect must add its entry
// and keep the mark, and mai disconnect must give back the file.
func TestConnectThenDisconnectOfAJSONFileWithAByteOrderMark(t *testing.T) {
	home, m := setup(t)
	mcp := filepath.Join(vscodeUserDir(home), "mcp.json")
	const orig = "\xEF\xBB\xBF{\n\t\"servers\": {\n\t\t\"github\": {\n\t\t\t\"type\": \"http\",\n\t\t\t\"url\": \"https://api.githubcopilot.com/mcp/\"\n\t\t}\n\t}\n}\n"
	writeAll(t, map[string]string{mcp: orig})
	p, err := Prepare(home, "copilot", "a")
	if err != nil {
		t.Fatalf("mai connect copilot of an mcp.json with a byte order mark: %v", err)
	}
	if err := p.Apply(m); err != nil {
		t.Fatal(err)
	}
	if got := readText(t, mcp); !strings.HasPrefix(got, "\xEF\xBB\xBF") || !strings.Contains(got, testBin) {
		t.Fatalf("mcp.json after connect: %q", got)
	}
	if err := Disconnect("copilot", m); err != nil {
		t.Fatal(err)
	}
	if got := readText(t, mcp); got != orig {
		t.Fatalf("mcp.json after disconnect:\n%s", unifiedDiff("want", "got", orig, got))
	}
}

// A user who does not use the Copilot CLI has no folder ~/.copilot. mai connect copilot makes
// ~/.copilot/hooks/mai.json and its folders, and mai disconnect copilot removes the file. The
// folders that mai connect made must go too, so that the home folder is as before.
func TestDisconnectRemovesTheFoldersThatConnectMade(t *testing.T) {
	home, m := setup(t)
	dir := filepath.Join(home, ".copilot")
	connectApp(t, home, "copilot", m)
	if _, err := os.Stat(filepath.Join(dir, "hooks", "mai.json")); err != nil {
		t.Fatalf("connect made no hooks file: %v", err)
	}
	if err := Disconnect("copilot", m); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err == nil {
		var left []string
		_ = filepath.Walk(dir, func(p string, _ os.FileInfo, _ error) error {
			left = append(left, strings.TrimPrefix(p, home))
			return nil
		})
		t.Fatalf("mai disconnect copilot keeps the folders that mai connect made: %v", left)
	}
}
