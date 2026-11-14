package connect

// Antigravity reads its MCP servers only from the file of "agy mcp add",
// ~/.gemini/config/mcp_config.json. The versions 3.3.0 and earlier of mai wrote the MCP entry
// of mai in ~/.gemini/antigravity/mcp_config.json. Antigravity does not read that file.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/manifest"
)

// agyFiles returns the MCP file that Antigravity reads, and the earlier MCP file of mai 3.3.0.
func agyFiles(home string) (mcp, old string) {
	return filepath.Join(home, ".gemini", "config", "mcp_config.json"),
		filepath.Join(home, ".gemini", "antigravity", "mcp_config.json")
}

// mcpEntryOf returns the compact text of /mcpServers/mai in the JSON file at path, or "".
func mcpEntryOf(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	root, err := scanJSON(b)
	if err != nil {
		t.Fatalf("%s is not JSON: %v\n%s", path, err, b)
	}
	n := nodeAt(root, []string{"mcpServers", "mai"})
	if n == nil {
		return ""
	}
	return string(canonical(b, n))
}

// connectAs330 writes the MCP entry of Antigravity as mai 3.3.0 wrote it: in the earlier file, with
// its records in m.
func connectAs330(t *testing.T, home string, m *manifest.Manifest) {
	t.Helper()
	_, old := agyFiles(home)
	c := change{path: old, pointer: "/mcpServers/mai", value: obj("command", testBin, "args", mcpArgs("a")), mcp: true}
	before, existed, err := readFile(old)
	if err != nil {
		t.Fatal(err)
	}
	fc, err := planFile(old, before, existed, []change{c}, "antigravity")
	if err != nil {
		t.Fatal(err)
	}
	if err := (&Plan{App: "antigravity", Agent: "a", Files: []*FileChange{fc}}).Apply(m); err != nil {
		t.Fatal(err)
	}
	if mcpEntryOf(t, old) == "" {
		t.Fatalf("the test wrote no entry of mai in %s", old)
	}
}

// recordsOf returns the records in m of the type typ for the file path.
func recordsOf(m *manifest.Manifest, typ, path string) []manifest.Item {
	var out []manifest.Item
	for _, it := range m.Of(typ) {
		if it.Path == path {
			out = append(out, it)
		}
	}
	return out
}

func TestAntigravityConnectWritesTheMCPEntryInTheFileThatAntigravityReads(t *testing.T) {
	home, m := setup(t)
	mcp, old := agyFiles(home)
	connectApp(t, home, "antigravity", m)
	want := string(encodeJSON(obj("command", testBin, "args", mcpArgs("a")), ""))
	if got := mcpEntryOf(t, mcp); got != want {
		t.Fatalf("the entry /mcpServers/mai in %s is %q, want %q", mcp, got, want)
	}
	if _, err := os.Lstat(old); err == nil {
		t.Fatalf("mai connect antigravity wrote %s, a file that Antigravity does not read:\n%s", old, readText(t, old))
	}
}

func TestAntigravityConnectMovesAnEarlierEntryOfMaiAndKeepsEachByteOfTheUser(t *testing.T) {
	home, m := setup(t)
	mcp, old := agyFiles(home)
	const user = "{\n    \"mcpServers\": {\n        \"other\": {\n            \"command\": \"/usr/bin/true\"\n        }\n    },\n    \"theme\": \"dark\"\n}\n"
	const agy = "{\n  \"mcpServers\": {\n    \"user-server\": {\n      \"command\": \"/usr/bin/true\"\n    }\n  }\n}\n"
	writeAll(t, map[string]string{old: user, mcp: agy})
	connectAs330(t, home, m)

	p, err := PrepareWith(home, "antigravity", "a", m)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Changed() {
		t.Fatal("the plan has no change, but the earlier file has an entry of mai")
	}
	d := p.Diff()
	if !strings.Contains(d, "--- "+old+"\n+++ "+old+"\n") || !strings.Contains(d, "-        \"mai\": {") ||
		!strings.Contains(d, "--- "+mcp+"\n+++ "+mcp+"\n") || !strings.Contains(d, "+    \"mai\": {") {
		t.Fatalf("the diff does not show the removal from the earlier file and the new entry:\n%s", d)
	}
	if got := fileAfter(t, p, old); got != user {
		t.Fatalf("the plan of the earlier file:\n%s", unifiedDiff("want", "got", user, got))
	}
	if err := p.Apply(m); err != nil {
		t.Fatal(err)
	}
	if got := readText(t, old); got != user {
		t.Fatalf("the earlier file after the move:\n%s", unifiedDiff("want", "got", user, got))
	}
	if mcpEntryOf(t, mcp) == "" || !strings.Contains(readText(t, mcp), "user-server") {
		t.Fatalf("the file of Antigravity after the move:\n%s", readText(t, mcp))
	}
	if r := recordsOf(m, manifest.AppEntry, old); len(r) != 0 {
		t.Fatalf("the manifest keeps the records of the earlier entry: %+v", r)
	}
	if r := recordsOf(m, manifest.AppEntry, mcp); len(r) != 1 || r[0].Pointer != "/mcpServers/mai" {
		t.Fatalf("the records of the new entry: %+v", r)
	}

	again, err := PrepareWith(home, "antigravity", "a", m)
	if err != nil {
		t.Fatal(err)
	}
	if again.Changed() || len(again.Notes()) != 0 {
		t.Fatalf("a second connect changes files:\n%s%v", again.Diff(), again.Notes())
	}

	if err := Disconnect("antigravity", m); err != nil {
		t.Fatal(err)
	}
	if got := readText(t, mcp); got != agy {
		t.Fatalf("the file of Antigravity after disconnect:\n%s", unifiedDiff("want", "got", agy, got))
	}
	if got := readText(t, old); got != user {
		t.Fatalf("the earlier file after disconnect:\n%s", unifiedDiff("want", "got", user, got))
	}
	if n := len(m.Of(manifest.AppEntry)) + len(m.Of(manifest.File)); n != 0 {
		t.Fatalf("the manifest keeps %d records", n)
	}
}

func TestAntigravityConnectRemovesTheEarlierFileThatMaiMade(t *testing.T) {
	home, m := setup(t)
	mcp, old := agyFiles(home)
	writeAll(t, map[string]string{filepath.Join(home, ".gemini", "settings.json"): "{}\n"})
	connectAs330(t, home, m)
	if r := recordsOf(m, manifest.File, old); len(r) != 1 {
		t.Fatalf("the test has no file record of the earlier file: %+v", m.Items)
	}

	p, err := PrepareWith(home, "antigravity", "a", m)
	if err != nil {
		t.Fatal(err)
	}
	if d := p.Diff(); !strings.Contains(d, "--- "+old+"\n+++ /dev/null\n") {
		t.Fatalf("the diff does not show that the earlier file goes:\n%s", d)
	}
	if err := p.Apply(m); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(old); err == nil {
		t.Fatalf("the earlier file that mai made stays:\n%s", readText(t, old))
	}
	if _, err := os.Lstat(filepath.Dir(old)); err == nil {
		t.Fatal("the folder that mai made for the earlier file stays")
	}
	if r := append(recordsOf(m, manifest.AppEntry, old), recordsOf(m, manifest.File, old)...); len(r) != 0 {
		t.Fatalf("the manifest keeps the records of the earlier file: %+v", r)
	}
	if mcpEntryOf(t, mcp) == "" {
		t.Fatalf("%s has no entry of mai", mcp)
	}
	if again, err := PrepareWith(home, "antigravity", "a", m); err != nil || again.Changed() {
		t.Fatalf("a second connect: error %v, change:\n%s", err, again.Diff())
	}
}

func TestAntigravityConnectKeepsAnEarlierFileThatMaiMadeWhenTheUserAddedContent(t *testing.T) {
	home, m := setup(t)
	_, old := agyFiles(home)
	connectAs330(t, home, m)
	b := readText(t, old)
	edited := strings.Replace(b, "\"mcpServers\": {\n", "\"mcpServers\": {\n    \"other\": {\n      \"command\": \"/usr/bin/true\"\n    },\n", 1)
	if edited == b {
		t.Fatalf("the test could not edit the file:\n%s", b)
	}
	writeAll(t, map[string]string{old: edited})

	connectApp(t, home, "antigravity", m)
	want := "{\n  \"mcpServers\": {\n    \"other\": {\n      \"command\": \"/usr/bin/true\"\n    }\n  }\n}\n"
	if got := readText(t, old); got != want {
		t.Fatalf("the earlier file after the move:\n%s", unifiedDiff("want", "got", want, got))
	}
	if r := recordsOf(m, manifest.AppEntry, old); len(r) != 0 {
		t.Fatalf("the manifest keeps the record of the earlier entry: %+v", r)
	}
}

func TestAntigravityConnectRemovesAnEarlierEntryOfMaiWithNoRecord(t *testing.T) {
	home, m := setup(t)
	_, old := agyFiles(home)
	const user = "{\"mcpServers\":{\"other\":{\"command\":\"x\"},\"mai\":{\"command\":\"/usr/local/bin/mai\",\"args\":[\"mcp\",\"--agent\",\"b\"],\"env\":{\"A\":\"1\"}}},\"z\":1}"
	writeAll(t, map[string]string{old: user})

	p, err := Prepare(home, "antigravity", "a")
	if err != nil {
		t.Fatal(err)
	}
	const want = "{\"mcpServers\":{\"other\":{\"command\":\"x\"}},\"z\":1}"
	if got := fileAfter(t, p, old); got != want {
		t.Fatalf("the plan of the earlier file:\n%s", unifiedDiff("want", "got", want, got))
	}
	if err := p.Apply(m); err != nil {
		t.Fatal(err)
	}
	if got := readText(t, old); got != want {
		t.Fatalf("the earlier file after the move:\n%s", unifiedDiff("want", "got", want, got))
	}
}

func TestAntigravityConnectKeepsAnEarlierEntryThatIsNotOfMai(t *testing.T) {
	home, m := setup(t)
	mcp, old := agyFiles(home)
	const user = "{\n  \"mcpServers\": {\n    \"mai\": {\n      \"command\": \"npx\",\n      \"args\": [\"-y\", \"mail-ai-server\"]\n    }\n  }\n}\n"
	writeAll(t, map[string]string{old: user})

	p, err := PrepareWith(home, "antigravity", "a", m)
	if err != nil {
		t.Fatal(err)
	}
	notes := p.Notes()
	if len(notes) != 1 || !strings.Contains(notes[0], old) || strings.Contains(notes[0], "\n") {
		t.Fatalf("the notes are %q, want one line about %s", notes, old)
	}
	if strings.Contains(p.Diff(), "--- "+old) {
		t.Fatalf("the diff changes the entry that is not of mai:\n%s", p.Diff())
	}
	if err := p.Apply(m); err != nil {
		t.Fatal(err)
	}
	if got := readText(t, old); got != user {
		t.Fatalf("the earlier file:\n%s", unifiedDiff("want", "got", user, got))
	}
	if mcpEntryOf(t, mcp) == "" {
		t.Fatalf("%s has no entry of mai", mcp)
	}
}

func TestAntigravityConnectKeepsAnEarlierFileThatIsNotPlainJSON(t *testing.T) {
	home, m := setup(t)
	_, old := agyFiles(home)
	const user = "{\n  // a comment\n  \"mcpServers\": {}\n}\n"
	writeAll(t, map[string]string{old: user})
	p, err := PrepareWith(home, "antigravity", "a", m)
	if err != nil {
		t.Fatalf("an earlier file that is not plain JSON stops mai connect: %v", err)
	}
	if notes := p.Notes(); len(notes) != 1 || !strings.Contains(notes[0], old) {
		t.Fatalf("the notes are %q, want one line about %s", notes, old)
	}
	if err := p.Apply(m); err != nil {
		t.Fatal(err)
	}
	if got := readText(t, old); got != user {
		t.Fatalf("the earlier file:\n%s", unifiedDiff("want", "got", user, got))
	}
}

func TestAntigravityDisconnectRemovesTheNewEntry(t *testing.T) {
	home, m := setup(t)
	mcp, _ := agyFiles(home)
	const agy = "{\n  \"mcpServers\": {\n    \"user-server\": {\n      \"command\": \"/usr/bin/true\"\n    }\n  }\n}\n"
	writeAll(t, map[string]string{mcp: agy})
	connectApp(t, home, "antigravity", m)
	if mcpEntryOf(t, mcp) == "" {
		t.Fatalf("%s has no entry of mai", mcp)
	}
	if err := Disconnect("antigravity", m); err != nil {
		t.Fatal(err)
	}
	if got := readText(t, mcp); got != agy {
		t.Fatalf("%s after disconnect:\n%s", mcp, unifiedDiff("want", "got", agy, got))
	}
}

func TestAntigravityDisconnectRemovesAnEarlierEntryThatTheManifestRecords(t *testing.T) {
	home, m := setup(t)
	_, old := agyFiles(home)
	const user = "{\n  \"mcpServers\": {\n    \"other\": {\n      \"command\": \"x\"\n    }\n  }\n}\n"
	writeAll(t, map[string]string{old: user})
	connectAs330(t, home, m)
	if err := Disconnect("antigravity", m); err != nil {
		t.Fatal(err)
	}
	if got := readText(t, old); got != user {
		t.Fatalf("the earlier file after disconnect:\n%s", unifiedDiff("want", "got", user, got))
	}
	if n := len(m.Of(manifest.AppEntry)); n != 0 {
		t.Fatalf("the manifest keeps %d records of entries", n)
	}
}
