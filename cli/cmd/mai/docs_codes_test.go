package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
