package uninstall

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/keys"
	"github.com/monorithm/mai/cli/v3/internal/manifest"
)

// config.json names mai. "mai connect cursor" wrote the entry while config.json named
// mai-dev, thus its record has the instance mai-dev. The uninstall of mai removes config.json,
// and the entry stays. The entry names no instance, thus its bridge uses the instance of
// config.json. After the uninstall, that instance must be mai-dev, or the output must tell how
// to make mai-dev the instance of config.json.
func TestTheUninstallOfTheInstanceOfConfigTellsWhichInstanceTheEntryOfADifferentInstanceServes(t *testing.T) {
	e := setup(t)
	addInstance(t, e, "mai-dev")
	e.eng.containers[len(e.eng.containers)-1].Port = 18766
	e.eng.containers[len(e.eng.containers)-1].Running = true
	if err := e.keys.Set(keys.AgentAccount("mai-dev", "cursor"), "mai1:cursor:dev"); err != nil {
		t.Fatal(err)
	}
	man, err := manifest.Load(filepath.Join(e.dirs.State, manifest.FileName), "mai-dev")
	if err != nil {
		t.Fatal(err)
	}
	if err := man.Add(manifest.Item{Type: manifest.AppEntry, Name: "cursor", Path: e.app, Pointer: "/mcpServers/mai", Instance: "mai-dev"}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := e.uninstaller(&out).Run(context.Background()); code != 0 {
		t.Fatalf("mai uninstall: exit %d:\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "kept     app entry cursor in "+e.app) {
		t.Errorf("the report has no line for the app entry of mai-dev that stays:\n%s", out.String())
	}
	cfg, err := LoadConfig(e.dirs.Config)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Instance != "mai-dev" && !strings.Contains(out.String(), "mai start --instance mai-dev") {
		t.Errorf("after the uninstall, the bridge of the entry of mai-dev uses the instance %q on the port %d, "+
			"which is gone. The output gives no step to make mai-dev the instance of config.json:\n%s",
			cfg.Instance, cfg.Port, out.String())
	}
}
