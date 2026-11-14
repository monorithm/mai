package main

// mai guide tells that an app is connected when mai connect for the app and the agent has no
// change. mai connect plans with the manifest of the instance: for example, an earlier file
// that mai made goes when no content stays in it. Thus mai guide plans with that manifest too.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/connect"
	"github.com/monorithm/mai/cli/v3/internal/manifest"
)

func TestGuideSaysNotConnectedWhileMaiConnectRemovesAnEarlierFileThatMaiMade(t *testing.T) {
	g := newGuideTest(t)
	g.serve(t)
	mcp, old, _ := agyPaths(g.home)
	writeFiles(t, map[string]string{mcp: agyUserFile})
	if code, out, errOut := connectAgy(t, "--yes"); code != 0 {
		t.Fatalf("connect: exit %d:\n%s%s", code, out, errOut)
	}
	// mai 3.3.0 made the earlier file, and the manifest has the record of that file. Then the
	// user removed the entry of mai from that file by hand. The MCP entry and the hook of mai
	// are in the files that Antigravity reads.
	writeFiles(t, map[string]string{old: "{}\n"})
	real, err := filepath.EvalSymlinks(old)
	if err != nil {
		t.Fatal(err)
	}
	m := loadTestManifest(t, g.home)
	if err := m.Add(manifest.Item{Type: manifest.File, Name: "antigravity", Path: old, Target: real}); err != nil {
		t.Fatal(err)
	}

	p, err := connect.PrepareWith(g.home, "antigravity", "antigravity", m)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Changed() {
		t.Fatal("the test state gives no change of mai connect")
	}
	if r := g.report(t, "--app", "antigravity", "--agent", "antigravity"); field(r, "app", "connected") != false {
		t.Fatalf("mai connect has a change, but the guide gives app = %v", r["app"])
	}
	code, out, errOut := connectAgy(t, "--dry-run")
	if code != 0 || !strings.Contains(out, "--- "+old+"\n+++ /dev/null\n") {
		t.Fatalf("the dry run does not show that the earlier file goes: exit %d:\n%s%s", code, out, errOut)
	}

	if code, out, errOut := connectAgy(t, "--yes"); code != 0 {
		t.Fatalf("connect: exit %d:\n%s%s", code, out, errOut)
	}
	if _, err := os.Lstat(old); err == nil {
		t.Fatalf("the earlier file that mai made stays:\n%s", readFileText(t, old))
	}
	if code, out, _ := connectAgy(t, "--dry-run"); code != 0 || !strings.Contains(out, "antigravity has the entries of mai already") {
		t.Fatalf("a second connect: exit %d:\n%s", code, out)
	}
	if r := g.report(t, "--app", "antigravity", "--agent", "antigravity"); field(r, "app", "connected") != true {
		t.Fatalf("after mai connect, the guide gives app = %v", r["app"])
	}
}
