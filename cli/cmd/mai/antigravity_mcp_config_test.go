package main

// Antigravity reads its MCP servers only from ~/.gemini/config/mcp_config.json. The versions
// 3.3.0 and earlier of mai wrote the MCP entry of mai in ~/.gemini/antigravity/mcp_config.json.
// Antigravity does not read that file. Thus mai connect antigravity moves the entry.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/paths"
	"github.com/monorithm/mai/cli/v3/internal/uninstall"
)

// The files of the tests:
//   - agyUserFile: the MCP file of the user, as "agy mcp add" wrote it.
//   - agyOldMai: the earlier file, with a server of the user and the entry of mai 3.3.0.
//   - agyOldUser: the earlier file with no entry of mai.
const (
	agyUserFile = "{\n  \"mcpServers\": {\n    \"user-server\": {\n      \"command\": \"/usr/bin/true\"\n    }\n  }\n}\n"
	agyOldUser  = "{\n  \"mcpServers\": {\n    \"other\": {\n      \"command\": \"/usr/bin/true\"\n    }\n  }\n}\n"
	agyOldMai   = "{\n  \"mcpServers\": {\n    \"other\": {\n      \"command\": \"/usr/bin/true\"\n    },\n    \"mai\": {\n      \"command\": \"/opt/mai/bin/mai\",\n      \"args\": [\n        \"mcp\",\n        \"--agent\",\n        \"antigravity\"\n      ]\n    }\n  }\n}\n"
)

// agyPaths returns the MCP file that Antigravity reads, the earlier MCP file, and the hooks file.
func agyPaths(home string) (mcp, old, hooks string) {
	g := filepath.Join(home, ".gemini")
	return filepath.Join(g, "config", "mcp_config.json"), filepath.Join(g, "antigravity", "mcp_config.json"),
		filepath.Join(g, "config", "hooks.json")
}

func writeFiles(t *testing.T, files map[string]string) {
	t.Helper()
	for p, s := range files {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func readFileText(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func connectAgy(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := runConnect(append([]string{"antigravity", "--agent", "antigravity"}, args...), strings.NewReader(""), &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestConnectAntigravityMovesTheEarlierEntryAndTheDryRunWritesNothing(t *testing.T) {
	_, _, home := testEnv(t)
	fixedConnectBinary(t)
	mcp, old, hooks := agyPaths(home)
	writeFiles(t, map[string]string{mcp: agyUserFile, old: agyOldMai})

	before := treeState(t, home)
	code, out, errOut := connectAgy(t, "--dry-run")
	if code != 0 {
		t.Fatalf("dry run: exit %d: %s", code, errOut)
	}
	sameTree(t, before, treeState(t, home))
	for _, want := range []string{
		"--- " + old + "\n+++ " + old + "\n", "-    \"mai\": {\n",
		"--- " + mcp + "\n+++ " + mcp + "\n", "+    \"mai\": {\n",
		"+++ " + hooks + "\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the dry run output has no %q:\n%s", want, out)
		}
	}

	if code, out, errOut := connectAgy(t, "--yes"); code != 0 {
		t.Fatalf("connect: exit %d:\n%s%s", code, out, errOut)
	}
	if got := readFileText(t, old); got != agyOldUser {
		t.Fatalf("the earlier file after the move:\n%s", got)
	}
	got := readFileText(t, mcp)
	if !strings.Contains(got, "user-server") || !strings.Contains(got, "\"mai\": {\n      \"command\": \"/opt/mai/bin/mai\"") {
		t.Fatalf("%s after the move:\n%s", mcp, got)
	}
	if code, out, _ := connectAgy(t, "--dry-run"); code != 0 || !strings.Contains(out, "antigravity has the entries of mai already") {
		t.Fatalf("a second connect: exit %d:\n%s", code, out)
	}

	var dout, derr bytes.Buffer
	if code := runDisconnect([]string{"antigravity"}, &dout, &derr); code != 0 {
		t.Fatalf("disconnect: exit %d: %s", code, derr.String())
	}
	if got := readFileText(t, mcp); got != agyUserFile {
		t.Fatalf("%s after disconnect:\n%s", mcp, got)
	}
	if got := readFileText(t, old); got != agyOldUser {
		t.Fatalf("the earlier file after disconnect:\n%s", got)
	}
	if _, err := os.Lstat(hooks); err == nil {
		t.Fatalf("the hooks file that mai made stays after disconnect:\n%s", readFileText(t, hooks))
	}
}

func TestConnectAntigravityTellsAboutAnEarlierEntryThatIsNotOfMai(t *testing.T) {
	_, _, home := testEnv(t)
	fixedConnectBinary(t)
	_, old, _ := agyPaths(home)
	const user = "{\"mcpServers\":{\"mai\":{\"command\":\"npx\",\"args\":[\"mail-ai-server\"]}}}\n"
	writeFiles(t, map[string]string{old: user})
	code, out, errOut := connectAgy(t, "--yes")
	if code != 0 {
		t.Fatalf("connect: exit %d:\n%s%s", code, out, errOut)
	}
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, old) {
			lines = append(lines, l)
		}
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "not an entry of mai") {
		t.Fatalf("the output tells about %s in %d lines, want 1:\n%s", old, len(lines), out)
	}
	if got := readFileText(t, old); got != user {
		t.Fatalf("the earlier file:\n%s", got)
	}
}

func TestGuideTellsThatAntigravityIsConnectedOnlyAfterTheMove(t *testing.T) {
	g := newGuideTest(t)
	g.serve(t)
	mcp, old, _ := agyPaths(g.home)
	writeFiles(t, map[string]string{mcp: agyUserFile, old: agyOldMai})
	// The hook is correct. Only the MCP entry is at the earlier place.
	if code, out, errOut := connectAgy(t, "--yes"); code != 0 {
		t.Fatalf("connect: exit %d:\n%s%s", code, out, errOut)
	}
	writeFiles(t, map[string]string{mcp: agyUserFile, old: agyOldMai})
	if r := g.report(t, "--app", "antigravity"); field(r, "app", "connected") != false {
		t.Fatalf("with the MCP entry at the earlier place, app = %v", r["app"])
	}
	if code, out, errOut := connectAgy(t, "--yes"); code != 0 {
		t.Fatalf("connect: exit %d:\n%s%s", code, out, errOut)
	}
	if r := g.report(t, "--app", "antigravity"); field(r, "app", "connected") != true {
		t.Fatalf("after the move, app = %v", r["app"])
	}
}

// The test runs only the app-entry and path part of the uninstall, with no engine.
func TestUninstallRemovesTheMCPEntryOfAntigravity(t *testing.T) {
	_, _, home := testEnv(t)
	fixedConnectBinary(t)
	mcp, _, _ := agyPaths(home)
	writeFiles(t, map[string]string{mcp: agyUserFile})
	if code, out, errOut := connectAgy(t, "--yes"); code != 0 {
		t.Fatalf("connect: exit %d:\n%s%s", code, out, errOut)
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
	if got := readFileText(t, mcp); got != agyUserFile {
		t.Fatalf("%s after the uninstall:\n%s\nresults:\n%s", mcp, got, report.String())
	}
}
