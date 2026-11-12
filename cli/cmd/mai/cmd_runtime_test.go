package main

import (
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// runtimeCapture runs f with os.Stdout and os.Stdin replaced, and returns the output.
func runtimeCapture(t *testing.T, stdin string, f func() int) (string, int) {
	t.Helper()
	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldIn, oldOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = inR, outW
	defer func() { os.Stdin, os.Stdout = oldIn, oldOut }()
	go func() { _, _ = io.WriteString(inW, stdin); _ = inW.Close() }()
	code := f()
	_ = outW.Close()
	data, _ := io.ReadAll(outR)
	return string(data), code
}

func TestHookPrintsTheNoticeOfAnAncestor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MAI_HOME", home)
	dir := filepath.Join(home, "state", "notices")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(dir, strconv.Itoa(os.Getppid())+"-cursor.json")
	if err := os.WriteFile(name, []byte(`{"session":"cursor.s2","new_mail":2,"updated_at":"2026-11-12T17:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out, code := runtimeCapture(t, `{"hook_event_name":"Stop"}`, func() int {
		return runHook([]string{"--format", "cursor", "--event", "stop", "--agent", "cursor"})
	})
	if code != 0 || !strings.Contains(out, `"followup_message"`) || !strings.Contains(out, "cursor.s2 has 2") {
		t.Fatalf("code %d, output %q", code, out)
	}
}

func TestHookNeverFails(t *testing.T) {
	t.Setenv("MAI_HOME", t.TempDir())
	out, code := runtimeCapture(t, "not json", func() int {
		return runHook([]string{"--bad-flag"})
	})
	if code != 0 || out != "" {
		t.Fatalf("code %d, output %q", code, out)
	}
	out, code = runtimeCapture(t, "", func() int {
		return runHook([]string{"--format", "claude", "--event", "Stop"})
	})
	if code != 0 || out != "" {
		t.Fatalf("code %d, output %q", code, out)
	}
}

func TestMCPNeedsAnAgent(t *testing.T) {
	if code := runMCP(nil); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
}
