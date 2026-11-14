package main

// A user can join the two MCP files of Antigravity with a symbolic link. Then the two paths
// give one file, and mai connect writes the entry of mai in it. A second mai connect has no
// change, and mai guide tells that the app is connected.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGuideSaysConnectedWhenALinkJoinsTheTwoMCPFilesOfAntigravity(t *testing.T) {
	for _, dir := range []string{"the earlier path links to the new file", "the new path links to the earlier file"} {
		t.Run(dir, func(t *testing.T) {
			g := newGuideTest(t)
			g.serve(t)
			mcp, old, _ := agyPaths(g.home)
			real, link := mcp, old
			if strings.HasPrefix(dir, "the new path") {
				real, link = old, mcp
			}
			writeFiles(t, map[string]string{real: agyUserFile})
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

			if code, out, errOut := connectAgy(t, "--yes"); code != 0 {
				t.Fatalf("connect 1: exit %d:\n%s%s", code, out, errOut)
			}
			for i := 2; i <= 3; i++ {
				code, out, errOut := connectAgy(t, "--dry-run")
				if code != 0 || !strings.Contains(out, "antigravity has the entries of mai already") || strings.Contains(out, "--- ") {
					t.Fatalf("connect %d --dry-run has a change: exit %d:\n%s%s", i, code, out, errOut)
				}
				if code, out, errOut := connectAgy(t, "--yes"); code != 0 {
					t.Fatalf("connect %d: exit %d:\n%s%s", i, code, out, errOut)
				}
			}
			got := readFileText(t, mcp)
			if !strings.Contains(got, "user-server") || !strings.Contains(got, "\"mai\": {\n      \"command\": \"/opt/mai/bin/mai\"") {
				t.Fatalf("%s after mai connect:\n%s", mcp, got)
			}
			if st, err := os.Lstat(link); err != nil || st.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("%s is not a symbolic link after mai connect (error %v)", link, err)
			}
			if r := g.report(t, "--app", "antigravity", "--agent", "antigravity"); field(r, "app", "connected") != true {
				t.Fatalf("after mai connect, app = %v", r["app"])
			}
		})
	}
}
