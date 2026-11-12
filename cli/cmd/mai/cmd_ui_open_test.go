//go:build presencefake

package main

import (
	"bytes"
	"strings"
	"testing"
)

// This test needs the fake presence check: go test -tags presencefake ./...
func TestUIOpenUsesTicketAndNeverPrintsIt(t *testing.T) {
	f, _, _ := testEnv(t)
	var opened string
	old := uiOpenBrowser
	uiOpenBrowser = func(url string) error { opened = url; return nil }
	t.Cleanup(func() { uiOpenBrowser = old })

	var out, errOut bytes.Buffer
	if code := runUI([]string{"open"}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "UI_OFF") {
		t.Fatalf("open with the UI off: %d %q", code, errOut.String())
	}
	f.ui = true
	out.Reset()
	errOut.Reset()
	if code := runUI([]string{"open"}, &out, &errOut); code != 0 {
		t.Fatalf("open: %d %s", code, errOut.String())
	}
	if !strings.HasSuffix(opened, "/ui/sign-in#t=secretticket") || !strings.HasPrefix(opened, "http://127.0.0.1:") {
		t.Fatalf("opened %q", opened)
	}
	if strings.Contains(out.String()+errOut.String(), "secretticket") {
		t.Fatal("the CLI printed the ticket")
	}
	t.Setenv("MAI_TEST_PRESENCE", "refuse")
	opened = ""
	if code := runUI([]string{"open"}, &out, &errOut); code != 1 || opened != "" {
		t.Fatalf("open after a refusal: %d %q", code, opened)
	}
}
