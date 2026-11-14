package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// jsonFields gives the JSON name of each field of the struct type t, and of each struct in
// it, also in a slice.
func jsonFields(t reflect.Type) []string {
	var out []string
	for i := range t.NumField() {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		out = append(out, name)
		ft := f.Type
		for ft.Kind() == reflect.Slice || ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Struct {
			out = append(out, jsonFields(ft)...)
		}
	}
	return out
}

func readDoc(t *testing.T, parts ...string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(append([]string{"..", "..", ".."}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// section gives the text of the doc from the heading to the next heading of the same level.
func section(t *testing.T, doc, heading string) string {
	t.Helper()
	start := strings.Index(doc, "\n"+heading+"\n")
	if start < 0 {
		t.Fatalf("the doc has no heading %q", heading)
	}
	level := heading[:strings.Index(heading, " ")+1]
	rest := doc[start+len(heading)+2:]
	if end := strings.Index(rest, "\n"+level); end >= 0 {
		rest = rest[:end]
	}
	return rest
}

// The reference of the CLI tells each flag, each step, and each JSON field of mai guide, and
// the flags --dry-run and --yes of mai connect.
func TestTheCLIReferenceTellsEachFieldOfMaiGuide(t *testing.T) {
	cli := readDoc(t, "docs", "reference", "cli.md")
	guide := section(t, cli, "### mai guide")
	for _, name := range jsonFields(reflect.TypeOf(guideReport{})) {
		if !strings.Contains(guide, "`"+name+"`") {
			t.Errorf("the section mai guide of cli.md does not tell the field %q", name)
		}
	}
	for _, s := range []string{"`--app APP`", "`--agent NAME`", "`--json`", "`install-engine`", "`start`", "`select-app`",
		"`rotate-agent-key`", "`add-agent`", "`show-change`", "`connect`", "`restart-app`", "`check`", "`\"ai-agent\"`",
		"`config-error`", "`legacy-container`", "`container-error`", "`key-file-error`", "`owner-key`", "`confirm-app`"} {
		if !strings.Contains(guide, s) {
			t.Errorf("the section mai guide of cli.md does not tell %s", s)
		}
	}
	conn := section(t, cli, "### mai connect")
	for _, s := range []string{"`--dry-run`", "`--yes`", "exit code 2"} {
		if !strings.Contains(conn, s) {
			t.Errorf("the section mai connect of cli.md does not tell %s", s)
		}
	}
	if !strings.Contains(cli, "| `mai guide [--app APP] [--agent NAME] [--json]` |") {
		t.Error("the table of the host CLI has no row for mai guide")
	}
}

// The how-to, the README, and the guide of the server tell an agent and a user about
// mai guide.
func TestTheGuidesTellHowAnAgentConnectsItsApp(t *testing.T) {
	const ask = "Run mai guide, and connect this app to mai."
	howTo := section(t, readDoc(t, "docs", "how-to", "connect-an-agent.md"), "## Let an agent connect its app")
	for _, s := range []string{ask, "`mai guide`", "--dry-run", "`--yes`", "approval"} {
		if !strings.Contains(howTo, s) {
			t.Errorf("the section of the how-to does not tell %q", s)
		}
	}
	if !strings.Contains(strings.Join(strings.Fields(readDoc(t, "README.md")), " "), ask) {
		t.Errorf("the quick start of the README does not tell %q", ask)
	}
	server := section(t, readDoc(t, "src", "mai", "modules", "mcp", "domain", "agent_guide.md"), "## Connect a different app")
	if !strings.Contains(server, "`mai guide --app codex`") || !strings.Contains(server, "approval of the user") ||
		!strings.Contains(server, "run the same\n   command again") || !strings.Contains(server, "the user does the check there") {
		t.Errorf("the guide of the server does not tell mai guide:\n%s", server)
	}
}
