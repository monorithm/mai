package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// From contract 3.2.0, each tool call with a label or a login can do a lease request in the
// bridge. Thus the bridge can give each of these codes in the result of a tool call, also
// BAD_KEY when the key file has no key for the agent, and KEY_FILE_ERROR when the bridge
// cannot read the key file. The reference of the MCP tools must list each code that a tool
// result can have, and the errors of a call with a label must tell the codes of the agent
// key.
func TestTheReferenceListsEachCodeOfACallWithALabel(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	b, err := os.ReadFile(filepath.Join(root, "docs", "reference", "mcp-tools.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	for _, code := range []string{"BAD_KEY", "KEY_FILE_ERROR", "BAD_LABEL", "SESSION_UNKNOWN", "SESSION_BUSY",
		"SESSION_REVOKED", "SERVER_UNAVAILABLE", "INVALID_ARGUMENT", "BUSY", "INTERNAL"} {
		row := regexp.MustCompile("(?m)^\\| `" + code + "` \\|")
		if !row.MatchString(text) {
			t.Errorf("the table of error codes in mcp-tools.md has no row for %s", code)
		}
	}
	start := strings.Index(text, "### Errors of a call with a label")
	end := strings.Index(text, "### Pool labels")
	if start < 0 || end < start {
		t.Fatal("no section of the errors of a call with a label")
	}
	for _, code := range []string{"BAD_KEY", "KEY_FILE_ERROR", "BUSY"} {
		if !strings.Contains(text[start:end], "`"+code+"`") {
			t.Errorf("the errors of a call with a label do not tell %s", code)
		}
	}
}

// The guide of the server lists the codes of the bridge, for example SERVER_UNAVAILABLE.
// Thus it also lists the codes of the agent key, and the reference of the CLI tells them
// in the section of the bridge.
func TestTheGuideAndTheCLIReferenceTellTheCodesOfTheAgentKey(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	guide, err := os.ReadFile(filepath.Join(root, "src", "mai", "modules", "mcp", "domain", "agent_guide.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"SERVER_UNAVAILABLE", "BAD_KEY", "KEY_FILE_ERROR"} {
		if !regexp.MustCompile("(?m)^\\| `" + code + "` \\|").Match(guide) {
			t.Errorf("the error table of the guide has no row for %s", code)
		}
	}
	cli, err := os.ReadFile(filepath.Join(root, "docs", "reference", "cli.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(cli)
	start := strings.Index(text, "## Bridge")
	end := strings.Index(text, "## Key file")
	if start < 0 || end < start {
		t.Fatal("no section of the bridge in cli.md")
	}
	for _, code := range []string{"BAD_KEY", "KEY_FILE_ERROR", "BUSY"} {
		if !strings.Contains(text[start:end], "`"+code+"`") {
			t.Errorf("the section of the bridge in cli.md does not tell %s", code)
		}
	}
}

// The how-to guide for Windows tells the step for BAD_KEY. For an agent that exists on the
// server, "mai agent add NAME" gives AGENT_EXISTS, thus the step must also give
// "mai agent rotate NAME".
func TestTheStepForBadKeyAlsoCoversAnAgentThatExists(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	b, err := os.ReadFile(filepath.Join(root, "docs", "how-to", "run-on-windows.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.Contains(line, "`BAD_KEY`") && !strings.Contains(line, "mai agent rotate") {
			t.Errorf("run-on-windows.md: %s", strings.TrimSpace(line))
		}
	}
}
