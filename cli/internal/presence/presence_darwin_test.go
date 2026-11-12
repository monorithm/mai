//go:build darwin && cgo && !presencefake

package presence

import "testing"

// The test calls only canEvaluatePolicy. It never shows the prompt of macOS.
func TestAvailableShowsNoPrompt(t *testing.T) {
	first := Available()
	if Available() != first {
		t.Fatal("Available changed between two calls")
	}
	t.Logf("Available = %v", first)
}
