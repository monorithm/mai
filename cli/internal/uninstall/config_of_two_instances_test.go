package uninstall

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/keys"
)

// config.json names mai. The uninstall of mai keeps the app entry, because mai-dev has keys
// and records. The entry names no instance, thus its bridge uses the instance of config.json.
// After the uninstall, that instance must be mai-dev, or the output must tell how to make
// mai-dev the instance of config.json. Else the bridge of the entry uses the instance mai,
// which is gone, and the commands of the agents cannot reach mai-dev.
func TestTheUninstallOfTheInstanceOfConfigTellsWhichInstanceTheEntriesThatStayServe(t *testing.T) {
	e := setup(t)
	addInstance(t, e, "mai-dev")
	e.eng.containers[len(e.eng.containers)-1].Port = 18766
	e.eng.containers[len(e.eng.containers)-1].Running = true
	if err := e.keys.Set(keys.AgentAccount("mai-dev", "cursor"), "mai1:cursor:dev"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	u := e.uninstaller(&out)
	if code := u.Run(context.Background()); code != 0 {
		t.Fatalf("mai uninstall: exit %d:\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "kept     app entry cursor in "+e.app) {
		t.Fatalf("the test needs the app entry to stay:\n%s", out.String())
	}
	cfg, err := LoadConfig(e.dirs.Config)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Instance != "mai-dev" && !strings.Contains(out.String(), "mai start --instance mai-dev") {
		t.Errorf("after the uninstall, the bridge of the app entry that stays uses the instance %q on the port %d, "+
			"which is gone. The output gives no step to make mai-dev the instance of config.json:\n%s",
			cfg.Instance, cfg.Port, out.String())
	}
}
