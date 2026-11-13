package uninstall

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/manifest"
)

// The record of an app entry of mai 3.1 has no instance, and the entry names no instance. The
// uninstall of each instance keeps the entry while a different instance stays, and tells it.
// The uninstall of the last instance removes the entry.
func TestAnAppEntryOf31StaysWhileAnInstanceStaysAndGoesWithTheLastInstance(t *testing.T) {
	e := setup(t)
	addInstance(t, e, "mai-dev")
	writeManifest(t, e, `{"instance": "mai-dev", "items": [
  {"type": "container", "name": "mai", "id": "c1"},
  {"type": "container", "name": "mai-dev", "id": "c-mai-dev"},
  {"type": "app-entry", "name": "cursor", "path": `+jsonText(e.app)+`, "pointer": "/mcpServers/mai"}]}`)
	var removed []manifest.Item
	RemoveAppEntry = func(it manifest.Item) error { removed = append(removed, it); return nil }
	t.Cleanup(func() { RemoveAppEntry = nil })
	for _, inst := range []string{"mai-dev", "mai"} {
		var out bytes.Buffer
		u := e.uninstaller(&out)
		u.Instance = inst
		if code := u.Run(context.Background()); code != 0 {
			t.Fatalf("mai uninstall --instance %s: exit %d:\n%s", inst, code, out.String())
		}
		switch {
		case inst == "mai-dev" && (len(removed) != 0 || !strings.Contains(out.String(), "kept     app entry cursor in "+e.app)):
			t.Fatalf("the uninstall of mai-dev did not keep and tell the app entry, while mai stays:\n%s", out.String())
		case inst == "mai" && (len(removed) != 1 || !strings.Contains(out.String(), "gone     app entry cursor in "+e.app)):
			t.Fatalf("the uninstall of the last instance did not remove the app entry:\n%s", out.String())
		}
	}
}
