package connect

// A user can join the two MCP files of Antigravity with a symbolic link: the earlier file
// names the file that Antigravity reads, or the file that Antigravity reads names the earlier
// file. Then the two paths give one file. mai connect writes the entry of mai in that file,
// and does not remove it through the earlier path.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/manifest"
)

// The two directions of the link.
const (
	earlierLinksToNew = "the earlier path links to the file that Antigravity reads"
	newLinksToEarlier = "the path that Antigravity reads links to the earlier file"
)

// agyServerFile is the MCP file of the user, as "agy mcp add" wrote it.
const agyServerFile = "{\n  \"mcpServers\": {\n    \"user-server\": {\n      \"command\": \"/usr/bin/true\"\n    }\n  }\n}\n"

// linkAgyFiles writes text in the real file of the direction dir, and makes the other path a
// relative symbolic link to that file. It returns the real file and the link.
func linkAgyFiles(t *testing.T, home, dir, text string) (real, link string) {
	t.Helper()
	mcp, old := agyFiles(home)
	real, link = mcp, old
	if dir == newLinksToEarlier {
		real, link = old, mcp
	}
	writeAll(t, map[string]string{real: text})
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(filepath.Dir(link), real)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(rel, link); err != nil {
		t.Fatal(err)
	}
	return real, link
}

// isLink fails the test when path is not a symbolic link.
func isLink(t *testing.T, path string) {
	t.Helper()
	if st, err := os.Lstat(path); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s is not a symbolic link (error %v)", path, err)
	}
}

func TestAntigravityConnectSettlesWhenALinkJoinsTheTwoMCPFiles(t *testing.T) {
	for _, dir := range []string{earlierLinksToNew, newLinksToEarlier} {
		t.Run(dir, func(t *testing.T) {
			home, m := setup(t)
			mcp, _ := agyFiles(home)
			real, link := linkAgyFiles(t, home, dir, agyServerFile)
			for i := 1; i <= 3; i++ {
				p, err := PrepareWith(home, "antigravity", "a", m)
				if err != nil {
					t.Fatal(err)
				}
				d := p.Diff()
				if strings.Contains(d, "-    \"mai\": {") {
					t.Fatalf("connect %d removes the entry of mai through a path of the link:\n%s", i, d)
				}
				if i > 1 && (p.Changed() || d != "" || len(p.Notes()) != 0) {
					t.Fatalf("connect %d has a change:\n%s%v", i, d, p.Notes())
				}
				if err := p.Apply(m); err != nil {
					t.Fatal(err)
				}
				if mcpEntryOf(t, mcp) == "" {
					t.Fatalf("after connect %d, %s has no entry of mai:\n%s", i, mcp, readText(t, real))
				}
			}
			isLink(t, link)
			if !strings.Contains(readText(t, real), "user-server") {
				t.Fatalf("the server of the user is gone:\n%s", readText(t, real))
			}
			if err := Disconnect("antigravity", m); err != nil {
				t.Fatal(err)
			}
			if got := readText(t, real); got != agyServerFile {
				t.Fatalf("%s after disconnect:\n%s", real, unifiedDiff("want", "got", agyServerFile, got))
			}
			isLink(t, link)
			if n := len(m.Of(manifest.AppEntry)) + len(m.Of(manifest.File)); n != 0 {
				t.Fatalf("the manifest keeps %d records after disconnect: %+v", n, m.Items)
			}
		})
	}
}

func TestAntigravityConnectRemovesOnlyTheRecordOfAnEarlierEntryWhenALinkJoinsTheTwoMCPFiles(t *testing.T) {
	for _, dir := range []string{earlierLinksToNew, newLinksToEarlier} {
		t.Run(dir, func(t *testing.T) {
			home, m := setup(t)
			mcp, old := agyFiles(home)
			real, link := linkAgyFiles(t, home, dir, agyServerFile)
			// mai 3.3.0 wrote the entry through the earlier path, and recorded it there.
			connectAs330(t, home, m)
			written := readText(t, real)
			if r := recordsOf(m, manifest.AppEntry, old); len(r) != 1 {
				t.Fatalf("the test has no record of the earlier entry: %+v", m.Items)
			}

			p, err := PrepareWith(home, "antigravity", "a", m)
			if err != nil {
				t.Fatal(err)
			}
			if d := p.Diff(); strings.Contains(d, "--- "+old+"\n") || strings.Contains(d, "--- "+mcp+"\n") {
				t.Fatalf("the plan changes the MCP file:\n%s", d)
			}
			if err := p.Apply(m); err != nil {
				t.Fatal(err)
			}
			if got := readText(t, real); got != written {
				t.Fatalf("mai connect changed the MCP file:\n%s", unifiedDiff("want", "got", written, got))
			}
			isLink(t, link)
			if r := recordsOf(m, manifest.AppEntry, old); len(r) != 0 {
				t.Fatalf("the manifest keeps the record of the earlier entry: %+v", r)
			}
			if r := recordsOf(m, manifest.AppEntry, mcp); len(r) != 1 || r[0].Pointer != "/mcpServers/mai" {
				t.Fatalf("the records of the new entry: %+v", r)
			}

			again, err := PrepareWith(home, "antigravity", "a", m)
			if err != nil {
				t.Fatal(err)
			}
			if again.Changed() || again.Diff() != "" || len(again.Notes()) != 0 {
				t.Fatalf("a second connect has a change:\n%s%v", again.Diff(), again.Notes())
			}
			if err := Disconnect("antigravity", m); err != nil {
				t.Fatal(err)
			}
			if got := readText(t, real); got != agyServerFile {
				t.Fatalf("%s after disconnect:\n%s", real, unifiedDiff("want", "got", agyServerFile, got))
			}
			isLink(t, link)
		})
	}
}
