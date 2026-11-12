package main

// Each test in this file gives a concrete input for a case that mai connect, mai
// disconnect, or mai uninstall must handle without a loss of content of the user.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/connect"
	"github.com/monorithm/mai/cli/v3/internal/paths"
	"github.com/monorithm/mai/cli/v3/internal/uninstall"
)

func fixedConnectBinary(t *testing.T) {
	t.Helper()
	old := connect.Binary
	connect.Binary = func() (string, error) { return "/opt/mai/bin/mai", nil }
	t.Cleanup(func() { connect.Binary = old })
}

// mai connect cursor makes ~/.cursor/mcp.json when it does not exist. Then the user adds a
// server of their own in Cursor. mai uninstall removes the entry of mai through
// uninstall.RemoveAppEntry = connect.RemoveEntry, and must keep the server of the user.
// The test runs only the app-entry and path part of the uninstall, with no engine.
func TestUninstallKeepsAFileOfAnAppThatTheUserFilled(t *testing.T) {
	_, _, home := testEnv(t)
	fixedConnectBinary(t)
	var out, errOut bytes.Buffer
	if code := runConnect([]string{"cursor", "--agent", "cursor", "--yes"}, strings.NewReader(""), &out, &errOut); code != 0 {
		t.Fatalf("connect: %d %s", code, errOut.String())
	}
	mcp := filepath.Join(home, ".cursor", "mcp.json")
	b, err := os.ReadFile(mcp)
	if err != nil {
		t.Fatal(err)
	}
	userServer := "{\n  \"mcpServers\": {\n    \"github\": {\n      \"url\": \"https://api.githubcopilot.com/mcp/\"\n    },\n"
	filled := strings.Replace(string(b), "{\n  \"mcpServers\": {\n", userServer, 1)
	if filled == string(b) {
		t.Fatalf("the test could not edit the file:\n%s", b)
	}
	if err := os.WriteFile(mcp, []byte(filled), 0o600); err != nil {
		t.Fatal(err)
	}

	dirs, err := paths.Default()
	if err != nil {
		t.Fatal(err)
	}
	var uout bytes.Buffer
	u := &uninstall.Uninstaller{Dirs: dirs, Yes: true, Out: &uout}
	p, err := u.MakePlan()
	if err != nil {
		t.Fatal(err)
	}
	p.Containers, p.Volumes, p.Images, p.Keys = nil, nil, nil, nil
	rs := u.Remove(context.Background(), p)
	var report bytes.Buffer
	uninstall.Report(&report, rs)

	got, err := os.ReadFile(mcp)
	if err != nil {
		t.Fatalf("mai uninstall deleted %s with the server of the user (%v); results:\n%s", mcp, err, report.String())
	}
	if !strings.Contains(string(got), "githubcopilot") || strings.Contains(string(got), "/opt/mai/bin/mai") {
		t.Fatalf("mcp.json after uninstall:\n%s", got)
	}
}

// slowAnswer is the answer of a user who reads the diff for some time. During that time,
// Claude Code writes ~/.claude.json, as it does at each start of a session.
type slowAnswer struct {
	path, text string
	done       bool
}

func (s *slowAnswer) Read(b []byte) (int, error) {
	if !s.done {
		s.done = true
		_ = os.WriteFile(s.path, []byte(s.text), 0o600)
	}
	return copy(b, "y\n"), nil
}

func TestConnectKeepsAWriteOfTheAppDuringTheQuestion(t *testing.T) {
	_, _, home := testEnv(t)
	fixedConnectBinary(t)
	claudeJSON := filepath.Join(home, ".claude.json")
	orig := "{\n  \"numStartups\": 41,\n  \"projects\": {}\n}"
	if err := os.WriteFile(claudeJSON, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	byClaude := "{\n  \"numStartups\": 42,\n  \"projects\": {\n    \"/Users/a/src/shop\": {\n      \"hasTrustDialogAccepted\": true\n    }\n  }\n}"
	var out, errOut bytes.Buffer
	in := &slowAnswer{path: claudeJSON, text: byClaude}
	code := runConnect([]string{"claude", "--agent", "claude"}, in, &out, &errOut)
	got, _ := os.ReadFile(claudeJSON)
	if code == 0 && !strings.Contains(string(got), "\"numStartups\": 42") {
		t.Fatalf("connect wrote the old text over the write of Claude Code:\n%s", got)
	}
}
