package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/manifest"
)

// treeState gives the type, the mode, the size, the time of the last change, and the hash of
// each file and folder under root.
func treeState(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		s := fi.Mode().String() + " " + strconv.FormatInt(fi.Size(), 10) + " " + fi.ModTime().String()
		if fi.Mode().IsRegular() {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			h := sha256.Sum256(b)
			s += " " + hex.EncodeToString(h[:])
		}
		out[p] = s
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func sameTree(t *testing.T, before, after map[string]string) {
	t.Helper()
	for p, s := range after {
		if before[p] != s {
			t.Errorf("the command changed %s: %q, then %q", p, before[p], s)
		}
	}
	for p := range before {
		if _, ok := after[p]; !ok {
			t.Errorf("the command removed %s", p)
		}
	}
}

func TestConnectDryRunWritesNothing(t *testing.T) {
	_, _, home := testEnv(t)
	fixedConnectBinary(t)
	before := treeState(t, home)
	var out, errOut bytes.Buffer
	code := runConnect([]string{"cursor", "--agent", "cursor", "--dry-run"}, strings.NewReader("y\n"), &out, &errOut)
	if code != 0 {
		t.Fatalf("dry run: exit %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "The entries name the mai binary /opt/mai/bin/mai.") ||
		!strings.Contains(out.String(), "+++ "+filepath.Join(home, ".cursor", "mcp.json")) ||
		strings.Contains(out.String(), "Write these changes?") {
		t.Fatalf("dry run output:\n%s", out.String())
	}
	sameTree(t, before, treeState(t, home))

	// For an app that has the entries, a dry run also writes no record in the manifest.
	if code := runConnect([]string{"cursor", "--agent", "cursor", "--yes"}, strings.NewReader(""), &out, &errOut); code != 0 {
		t.Fatalf("connect: %d %s", code, errOut.String())
	}
	mpath := filepath.Join(home, "maihome", "state", manifest.FileName)
	if err := os.Remove(mpath); err != nil {
		t.Fatal(err)
	}
	before = treeState(t, home)
	out.Reset()
	if code := runConnect([]string{"cursor", "--agent", "cursor", "--dry-run"}, strings.NewReader(""), &out, &errOut); code != 0 {
		t.Fatalf("second dry run: exit %d", code)
	}
	if !strings.Contains(out.String(), "cursor has the entries of mai already") {
		t.Fatalf("second dry run output:\n%s", out.String())
	}
	sameTree(t, before, treeState(t, home))
}

func TestConnectDryRunWithYesIsAUsageError(t *testing.T) {
	_, _, home := testEnv(t)
	fixedConnectBinary(t)
	before := treeState(t, home)
	var out, errOut bytes.Buffer
	code := runConnect([]string{"cursor", "--agent", "cursor", "--dry-run", "--yes"}, strings.NewReader(""), &out, &errOut)
	if code != 2 || !strings.Contains(errOut.String(), connectUse) || !strings.Contains(connectUse, "[--dry-run | --yes]") {
		t.Fatalf("exit %d\nout: %s\nerr: %s", code, out.String(), errOut.String())
	}
	sameTree(t, before, treeState(t, home))
}

func TestConnectWithNoTerminalTellsAnAgentTheTwoFlags(t *testing.T) {
	testEnv(t)
	fixedConnectBinary(t)
	var out, errOut bytes.Buffer
	code := runConnect([]string{"cursor", "--agent", "cursor"}, strings.NewReader(""), &out, &errOut)
	if code != 1 || !strings.Contains(out.String(), "no change") {
		t.Fatalf("exit %d\nout: %s", code, out.String())
	}
	line := "mai: an AI agent: run mai connect cursor --agent cursor --dry-run to show the change, and run it with --yes only after the user agrees.\n"
	if errOut.String() != line {
		t.Fatalf("stderr = %q, want %q", errOut.String(), line)
	}
}
