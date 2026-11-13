package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A bridge of 3.0 or 3.1 that runs in an app reads the agent key from the keychain item at
// each lease request. After "mai upgrade" moves the keys and deletes the items, that bridge
// stops at its next lease request with BAD_KEY and the step "Run mai agent add NAME", which
// gives AGENT_EXISTS. Thus the text of mai upgrade in the change log and in the reference must
// tell the user to start each app (or its MCP server) again after the upgrade.
func TestTheUpgradeTextTellsToStartTheAppsAgain(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	section := func(text, start, end string) string {
		i := strings.Index(text, start)
		if i < 0 {
			t.Fatalf("no section %q", start)
		}
		rest := text[i+len(start):]
		if j := strings.Index(rest, end); j >= 0 {
			rest = rest[:j]
		}
		return rest
	}
	step := regexp.MustCompile(`(?is)(restart|start)\b[^.]{0,80}\b(app|apps|MCP server|bridge|bridges)\b[^.]{0,40}\bagain\b|restart[^.]{0,60}\b(app|apps|MCP server|bridges?)\b`)
	for name, text := range map[string]string{
		"CHANGELOG.md [3.2.0]":                          section(read("CHANGELOG.md"), "## [3.2.0]", "\n## ["),
		"docs/reference/cli.md Keys of mai 3.0 and 3.1": section(read(filepath.Join("docs", "reference", "cli.md")), "### Keys of mai 3.0 and 3.1", "\n## "),
	} {
		if !step.MatchString(text) {
			t.Errorf("%s does not tell to start the apps that run mai mcp again after mai upgrade", name)
		}
	}
}
