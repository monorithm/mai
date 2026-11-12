package connect

// More cases of linked dotfiles and folders, CODEX_HOME, and the args of Codex, each with a
// concrete input.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/manifest"
)

// realHome returns the home folder of setup with no link in its path, as /Users/NAME on macOS
// (the temporary folder of a test on macOS is under the link /var).
func realHome(t *testing.T, home string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

// A user with no ~/.cursor runs mai connect cursor: mai makes ~/.cursor, hooks.json, and
// mcp.json. Then the user installs the dotfiles, which link the whole folder:
// rm -rf ~/.cursor && ln -s ~/dotfiles/cursor ~/.cursor (or GNU Stow, which folds a folder
// into one link). The folder of the dotfiles has the mcp.json of the user and no hooks.json.
// mai disconnect cursor must keep the link of the user: it is not the folder that mai made.
func TestDisconnectKeepsALinkOfTheUserAtAFolderThatConnectMade(t *testing.T) {
	home, m := setup(t)
	home = realHome(t, home)
	dir := filepath.Join(home, ".cursor")
	connectApp(t, home, "cursor", m) // mai makes ~/.cursor, hooks.json, and mcp.json
	dot := filepath.Join(home, "dotfiles", "cursor")
	const mine = "{\n  \"mcpServers\": {\n    \"github\": {\n      \"url\": \"https://api.githubcopilot.com/mcp/\"\n    }\n  }\n}\n"
	writeAll(t, map[string]string{filepath.Join(dot, "mcp.json"): mine})
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dot, dir); err != nil {
		t.Fatal(err)
	}

	err := Disconnect("cursor", m)
	st, lerr := os.Lstat(dir)
	if lerr != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("mai disconnect cursor (error %v) removes the link ~/.cursor of the user to %s: lstat error %v; Cursor does not find the mcp.json of the user now", err, dot, lerr)
	}
	if got := readText(t, filepath.Join(dir, "mcp.json")); err != nil || got != mine {
		t.Fatalf("mai disconnect cursor gives the error %v; ~/.cursor/mcp.json:\n%s", err, unifiedDiff("want", "got", mine, got))
	}
}

// mai connect makes ~/.cursor/mcp.json. Then the user installs the dotfiles (GNU Stow, or
// ln -sf): ~/.cursor/mcp.json becomes a link to the file of the user, as Cursor writes it
// with "Add new global MCP server". Cursor does not see mai now, thus the user runs mai
// connect cursor again, and later mai disconnect cursor. The dotfile of the user must get
// back its bytes from before the second connect: mai did not make its "mcpServers".
func TestReconnectAfterTheUserLinkedADotfileKeepsTheBytesOfTheDotfile(t *testing.T) {
	const mine = "{\n  \"mcpServers\": {}\n}\n"
	for _, c := range []struct {
		name   string
		before *string // ~/.cursor/mcp.json before the first connect; nil: no file
	}{
		{"mai made mcp.json", nil},
		{"mcp.json was an empty file", new(string)},
	} {
		t.Run(c.name, func(t *testing.T) {
			home, m := setup(t)
			mcp := filepath.Join(home, ".cursor", "mcp.json")
			if c.before != nil {
				writeAll(t, map[string]string{mcp: *c.before})
			}
			connectCursor(t, home, m) // mai writes "mcpServers" in ~/.cursor/mcp.json
			dot := filepath.Join(home, "dotfiles", "cursor", ".cursor", "mcp.json")
			writeAll(t, map[string]string{dot: mine})
			if err := os.Remove(mcp); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(dot, mcp); err != nil {
				t.Fatal(err)
			}

			connectCursor(t, home, m) // the user connects again
			if got := readText(t, dot); !strings.Contains(got, testBin) {
				t.Fatalf("the second connect wrote no entry in the dotfile:\n%s", got)
			}
			if err := Disconnect("cursor", m); err != nil {
				t.Fatal(err)
			}
			if got := readText(t, dot); got != mine {
				t.Fatalf("the dotfile of the user after connect and disconnect (%d bytes):\n%s", len(got), unifiedDiff("want", "got", mine, got))
			}
		})
	}
}

// mai connect makes ~/.cursor/hooks.json with "version" and the hooks of mai. Then the user
// installs the dotfiles: ~/.cursor/hooks.json becomes a link to the hooks file of the user,
// which has its own "version" and no hook yet. mai did not write that file. mai disconnect
// cursor must keep each byte of it.
func TestDisconnectKeepsTheVersionOfAHooksDotfileThatTheUserLinkedAfterConnect(t *testing.T) {
	home, m := setup(t)
	hooks := filepath.Join(home, ".cursor", "hooks.json")
	connectCursor(t, home, m) // mai makes ~/.cursor/hooks.json
	const mine = "{\n  \"version\": 1,\n  \"hooks\": {}\n}\n"
	dot := filepath.Join(home, "dotfiles", "cursor", ".cursor", "hooks.json")
	writeAll(t, map[string]string{dot: mine})
	if err := os.Remove(hooks); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dot, hooks); err != nil {
		t.Fatal(err)
	}

	err := Disconnect("cursor", m)
	if got := readText(t, dot); err != nil || got != mine {
		t.Fatalf("mai disconnect cursor gives the error %v; the hooks dotfile of the user:\n%s", err, unifiedDiff("want", "got", mine, got))
	}
}

// adoptIntoDotfiles moves the file at path into the package folder of the dotfiles and puts a
// link at path, as "stow --adopt", "mkrc" of rcm, and "mackup backup" do.
func adoptIntoDotfiles(t *testing.T, path, dot string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dot), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, dot); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dot, path); err != nil {
		t.Fatal(err)
	}
}

// The user has ~/.claude/settings.json with no hooks and runs mai connect claude. Later the
// user puts the file in the dotfiles with "stow --adopt" (or "mkrc" of rcm): the file moves
// into the dotfiles, and a link takes its place. It is the same file with the same bytes.
// mai disconnect claude must give back the bytes of the user, with no "hooks" that mai made.
func TestDisconnectOfAFileThatTheUserAdoptedIntoTheDotfilesAfterConnect(t *testing.T) {
	home, m := setup(t)
	settings := filepath.Join(home, ".claude", "settings.json")
	const orig = "{\n  \"model\": \"opus\"\n}\n"
	writeAll(t, map[string]string{settings: orig})
	connectApp(t, home, "claude", m)
	dot := filepath.Join(home, "dotfiles", "claude", ".claude", "settings.json")
	adoptIntoDotfiles(t, settings, dot)

	err := Disconnect("claude", m)
	if got := readText(t, dot); err != nil || got != orig {
		t.Fatalf("mai disconnect claude gives the error %v; the settings.json of the user in the dotfiles:\n%s", err, unifiedDiff("want", "got", orig, got))
	}
}

// leftIn returns each path under dir, relative to home.
func leftIn(home, dir string) []string {
	var left []string
	_ = filepath.Walk(dir, func(p string, _ os.FileInfo, _ error) error {
		left = append(left, strings.TrimPrefix(p, home))
		return nil
	})
	return left
}

// A user who has not started Cursor, Antigravity, or Codex yet has no folder of the app. mai
// connect makes the two files of the app in that folder, and mai disconnect removes the two
// files. The folder that mai connect made must go too, so that the home folder is as before.
func TestDisconnectRemovesAFolderThatConnectMadeForTwoFiles(t *testing.T) {
	for _, c := range []struct{ app, dir string }{
		{"cursor", ".cursor"},
		{"antigravity", ".gemini"},
		{"codex", ".codex"},
	} {
		t.Run(c.app, func(t *testing.T) {
			home, m := setup(t)
			dir := filepath.Join(home, c.dir)
			connectApp(t, home, c.app, m)
			if _, err := os.Stat(dir); err != nil {
				t.Fatalf("connect made no folder %s: %v", c.dir, err)
			}
			if err := Disconnect(c.app, m); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(dir); err == nil {
				t.Fatalf("mai disconnect %s keeps the folder that mai connect made: %v (file records in the manifest: %d)", c.app, leftIn(home, dir), len(m.Of(manifest.File)))
			}
		})
	}
}

// (uses realHome from the same file)
// ~/.cursor is a link to the folder of the dotfiles (GNU Stow folds a folder into one link).
// mai connect cursor makes hooks.json in that folder. Then the user removes the file, for
// example with "git clean" in the dotfiles. Nothing of mai stays, thus mai disconnect cursor
// must forget the file, and not keep it as a file "that holds content that is not of mai".
func TestDisconnectForgetsAFileThatConnectMadeInALinkedFolderAndTheUserRemoved(t *testing.T) {
	home, m := setup(t)
	home = realHome(t, home)
	dot := filepath.Join(home, "dotfiles", "cursor", ".cursor")
	const mine = "{\n  \"mcpServers\": {}\n}\n"
	writeAll(t, map[string]string{filepath.Join(dot, "mcp.json"): mine})
	if err := os.Symlink(dot, filepath.Join(home, ".cursor")); err != nil {
		t.Fatal(err)
	}
	connectApp(t, home, "cursor", m) // mai makes hooks.json in the folder of the dotfiles
	if err := os.Remove(filepath.Join(dot, "hooks.json")); err != nil {
		t.Fatal(err)
	}

	if err := Disconnect("cursor", m); err != nil {
		t.Fatal(err)
	}
	for _, it := range m.Of(manifest.File) {
		_, serr := os.Stat(it.Path)
		gone, err := RemoveCreatedFile(it)
		t.Errorf("after mai disconnect cursor, the manifest keeps the file %s (target %s; stat error %v); RemoveCreatedFile gives %v, %v, thus mai uninstall reports that it holds content that is not of mai", it.Path, it.Target, serr, gone, err)
	}
}

// A user writes the args of mai on more than one line, with comments. A second mai connect
// (also with no change of the binary) writes the args on one line. It must keep each comment of
// the user, as it keeps a comment on the line of command or of args.
func TestCodexReconnectKeepsTheCommentsInAnArgsArrayOnMoreThanOneLine(t *testing.T) {
	const comment1 = "# the name of this agent in the mail of the shop"
	const comment2 = "# set with mai connect --agent"
	for _, moved := range []bool{false, true} {
		name := "same binary"
		if moved {
			name = "the binary moved"
		}
		t.Run(name, func(t *testing.T) {
			home, m := setup(t)
			cfg := filepath.Join(home, ".codex", "config.toml")
			writeAll(t, map[string]string{cfg: shopCodexConfig})
			connectApp(t, home, "codex", m)
			b := readText(t, cfg)
			const one = `args = ["mcp", "--agent", "a"]`
			edited := strings.Replace(b, one, "args = [\n  \"mcp\",\n  "+comment1+"\n  \"--agent\", \"a\", "+comment2+"\n]", 1)
			if edited == b {
				t.Fatalf("the test could not edit the file:\n%s", b)
			}
			writeAll(t, map[string]string{cfg: edited})
			if moved {
				useBinary(t, "/usr/local/bin/mai")
			}
			p, err := Prepare(home, "codex", "a")
			if err != nil {
				t.Fatal(err)
			}
			after := fileAfter(t, p, cfg)
			if !strings.Contains(after, comment1) || !strings.Contains(after, comment2) {
				t.Fatalf("a second mai connect deletes the comments of the user in the args array:\n%s", unifiedDiff("before", "after", edited, after))
			}
		})
	}
}

// Codex reads its configuration from $CODEX_HOME when the variable is set, not from ~/.codex.
// mai connect codex must write the table of mai in the config.toml that Codex reads.
func TestCodexConnectWritesTheConfigTOMLOfCodexHome(t *testing.T) {
	home, _ := setup(t)
	codexHome := filepath.Join(home, ".config", "codex")
	cfg := filepath.Join(codexHome, "config.toml")
	writeAll(t, map[string]string{cfg: shopCodexConfig})
	t.Setenv("CODEX_HOME", codexHome)

	p, err := Prepare(home, "codex", "a")
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range p.Files {
		paths = append(paths, f.Path)
		if f.Path == cfg && strings.Contains(string(f.After), "[mcp_servers.mai]") {
			return
		}
	}
	t.Fatalf("with CODEX_HOME=%s, mai connect codex writes the files %v, and not %s, the file that Codex reads", codexHome, paths, cfg)
}

// An args array on more than one line with a comment after the opening bracket is still the
// args of mai. mai disconnect --force must remove the table, and mai connect must accept it.
func TestCodexForceDisconnectOfAnArgsArrayWithACommentAfterTheBracket(t *testing.T) {
	home, m := setup(t)
	cfg := filepath.Join(home, ".codex", "config.toml")
	writeAll(t, map[string]string{cfg: shopCodexConfig})
	connectApp(t, home, "codex", m)
	b := readText(t, cfg)
	const one = `args = ["mcp", "--agent", "a"]`
	edited := strings.Replace(b, one, "args = [ # the MCP server of mai\n  \"mcp\",\n  \"--agent\", \"a\",\n]", 1)
	if edited == b {
		t.Fatalf("the test could not edit the file:\n%s", b)
	}
	writeAll(t, map[string]string{cfg: edited})

	_, connectErr := Prepare(home, "codex", "a")
	if err := forceDisconnect(t, "codex", m); err != nil {
		t.Fatal(err)
	}
	if got := readText(t, cfg); got != shopCodexConfig {
		t.Fatalf("mai disconnect codex --force reports success and keeps the table of mai (mai connect gives: %v):\n%s", connectErr, unifiedDiff("want", "got", shopCodexConfig, got))
	}
}

// On Windows the path of mai has backslashes. mai writes it as a basic string with escapes.
// "codex mcp add" writes all servers again with toml_edit, which writes a string with a
// backslash as a literal string: the same value. mai disconnect must remove the table of mai.
func TestCodexDisconnectOfAWindowsPathThatTomlEditWroteAsALiteralString(t *testing.T) {
	const win = `C:\Users\a\AppData\Local\Programs\mai\mai.exe`
	home, m := setup(t)
	useBinary(t, win)
	cfg := filepath.Join(home, ".codex", "config.toml")
	writeAll(t, map[string]string{cfg: shopCodexConfig})
	connectApp(t, home, "codex", m)
	b := readText(t, cfg)
	edited := strings.Replace(b, "command = "+tomlString(win)+"\n", "command = '"+win+"'\n", 1)
	if edited == b {
		t.Fatalf("the test could not edit the file:\n%s", b)
	}
	writeAll(t, map[string]string{cfg: edited})

	err := Disconnect("codex", m)
	var ce *ErrChanged
	if got := readText(t, cfg); err != nil || got != shopCodexConfig {
		t.Fatalf("disconnect of the table of mai with the same command gives the error %v (ErrChanged: %v):\n%s", err, errors.As(err, &ce), unifiedDiff("want", "got", shopCodexConfig, got))
	}
}

// A user writes a comment of their own above the begin comment of mai, at the end of the file.
// mai disconnect removes the block of mai and must keep the line break of that comment.
func TestCodexDisconnectKeepsTheLineBreakOfACommentAboveTheBeginComment(t *testing.T) {
	const mine = "# Mail for AI, see the wiki of the shop"
	home, m := setup(t)
	cfg := filepath.Join(home, ".codex", "config.toml")
	writeAll(t, map[string]string{cfg: shopCodexConfig})
	connectApp(t, home, "codex", m)
	b := readText(t, cfg)
	edited := strings.Replace(b, tomlBegin, mine+"\n"+tomlBegin, 1)
	if edited == b {
		t.Fatalf("the test could not edit the file:\n%s", b)
	}
	writeAll(t, map[string]string{cfg: edited})

	if err := Disconnect("codex", m); err != nil {
		t.Fatal(err)
	}
	got := readText(t, cfg)
	if strings.Contains(got, "[mcp_servers.mai]") {
		t.Fatalf("the table of mai stays:\n%s", got)
	}
	if !strings.HasSuffix(got, mine+"\n") {
		t.Fatalf("mai disconnect removes the line break of the comment of the user: the end of the file is %q", got[max(0, len(got)-50):])
	}
}

// mai 3.0.0 wrote an end comment after its table, and toml_edit keeps that comment at the end
// of the file. When the user removes the server with "codex mcp remove mai", toml_edit
// removes the table of mai with the comment above it, and keeps the end comment. mai
// disconnect must remove that end comment of mai, and give back the file of the user.
func TestCodexDisconnectRemovesTheEndCommentOf300WhenTheTableIsGone(t *testing.T) {
	const user = "model = \"gpt-5-codex\"\n\n[mcp_servers.github]\ncommand = \"npx\"\n"
	table := "[mcp_servers.mai]\ncommand = \"mai\"\nargs = [\"mcp\", \"--agent\", \"codex\"]\n"
	block300 := "\n" + tomlBegin + table + tomlEnd
	home, m := setup(t)
	cfg := filepath.Join(home, ".codex", "config.toml")
	if err := m.Add(manifest.Item{Type: manifest.AppEntry, Name: "codex", Path: cfg, Pointer: TOMLPointer, Hash: hashOf([]byte(block300))}); err != nil {
		t.Fatal(err)
	}
	// The file after "codex mcp remove mai": the table of mai and its decor are gone, and the
	// end comment stays as the trailing comment of the document.
	afterCodex := user + tomlEnd
	writeAll(t, map[string]string{cfg: afterCodex})

	err := Disconnect("codex", m)
	if got := readText(t, cfg); err != nil || got != user {
		t.Fatalf("disconnect gives the error %v and keeps a comment of mai:\n%s", err, unifiedDiff("want", "got", user, got))
	}
}
