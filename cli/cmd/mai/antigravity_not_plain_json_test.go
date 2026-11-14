package main

// The earlier MCP file of Antigravity, ~/.gemini/antigravity/mcp_config.json, can hold text
// that is not plain JSON, for example a comment. mai connect antigravity does not change that
// file, and tells it in one line. It writes the entries in the other files.

import (
	"regexp"
	"strings"
	"testing"
)

func TestConnectAntigravityKeepsAnEarlierFileThatIsNotPlainJSONAndWritesTheOtherFiles(t *testing.T) {
	_, _, home := testEnv(t)
	fixedConnectBinary(t)
	mcp, old, hooks := agyPaths(home)
	const earlier = "{\n  // servers\n  \"mcpServers\": {\n    \"mai\": {\n      \"command\": \"/opt/mai/bin/mai\",\n      \"args\": [\"mcp\", \"--agent\", \"antigravity\"]\n    }\n  }\n}\n"
	writeFiles(t, map[string]string{mcp: agyUserFile, old: earlier})

	notes := func(out string) []string {
		var lines []string
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(l, old) && !strings.HasPrefix(l, "--- ") && !strings.HasPrefix(l, "+++ ") {
				lines = append(lines, l)
			}
		}
		return lines
	}
	code, out, errOut := connectAgy(t, "--yes")
	if code != 0 {
		t.Fatalf("connect: exit %d:\n%s%s", code, out, errOut)
	}
	if n := notes(out); len(n) != 1 || !strings.Contains(n[0], "not plain JSON") {
		t.Fatalf("the output tells about %s in %d lines, want 1 line about text that is not plain JSON:\n%s", old, len(n), out)
	}
	if got := readFileText(t, old); got != earlier {
		t.Fatalf("mai connect changed the earlier file:\n%s", got)
	}
	got := readFileText(t, mcp)
	if !strings.Contains(got, "user-server") || !strings.Contains(got, "\"mai\": {\n      \"command\": \"/opt/mai/bin/mai\"") {
		t.Fatalf("%s after mai connect:\n%s", mcp, got)
	}
	if !strings.Contains(readFileText(t, hooks), "/opt/mai/bin/mai hook --format antigravity --event PreInvocation") {
		t.Fatalf("%s has no hook of mai:\n%s", hooks, readFileText(t, hooks))
	}

	code, out, errOut = connectAgy(t, "--dry-run")
	if code != 0 || !strings.Contains(out, "antigravity has the entries of mai already") || len(notes(out)) != 1 {
		t.Fatalf("a second connect: exit %d:\n%s%s", code, out, errOut)
	}
}

// The reference and the change log tell what mai connect antigravity does with an earlier MCP
// file that is not plain JSON.
func TestTheDocsTellAboutAnEarlierMCPFileOfAntigravityThatIsNotPlainJSON(t *testing.T) {
	text := regexp.MustCompile(`(?s)not plain JSON[^.]*\bdoes not change\b[^.]*\bone line\b`)
	for name, doc := range map[string]string{
		"docs/reference/cli.md, mai connect": section(t, readDoc(t, "docs", "reference", "cli.md"), "### mai connect"),
		"CHANGELOG.md, [3.3.1]":              section(t, readDoc(t, "CHANGELOG.md"), "## [3.3.1] - 2026-11-14"),
	} {
		if !text.MatchString(strings.Join(strings.Fields(doc), " ")) {
			t.Errorf("%s does not tell that mai connect does not change an earlier file that is not plain JSON, and tells it in one line", name)
		}
	}
}
