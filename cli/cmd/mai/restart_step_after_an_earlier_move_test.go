package main

import (
	"strings"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/keys"
	"github.com/monorithm/mai/cli/v3/internal/manifest"
)

// After "go install" of the new CLI, an app can start a new bridge before "mai upgrade". That
// bridge moves the agent key to the key file and deletes the keychain item. A bridge of 3.1 of
// the same agent still runs in a different app. "mai upgrade" then makes the container again,
// thus that bridge needs a new lease, and it reads the keychain item, which is gone. Thus the
// upgrade must give the step to start the apps again.
func TestUpgradeTellsToStartTheAppsAgainWhenABridgeMovedTheAgentKeyBefore(t *testing.T) {
	_, k, home := install311(t)

	// A new bridge of each agent reads its key at its first lease request.
	kf, _ := openKeys()
	for agent, want := range map[string]string{"cursor": legacyCursorKey, "claude": legacyClaudeKey} {
		if got, err := kf.Get(keys.AgentAccount("mai", agent)); err != nil || got != want {
			t.Fatalf("the read of the bridge of %s gave an error or a different key: %v", agent, err)
		}
	}
	if got := strings.Join(k.legacy.Accounts(), ","); got != "owner@mai" {
		t.Fatalf("after the reads of the bridges, the keychain items are %s", got)
	}
	if n := len(loadTestManifest(t, home).OfInstance("mai", manifest.KeychainKey)); n != 3 {
		t.Fatalf("the manifest has %d keychain records, want 3", n)
	}

	// Then the user runs mai upgrade.
	out, errs := captureLifecycle(t)
	if !lcMoveKeys("mai", loadTestManifest(t, home)) {
		t.Fatalf("the move failed: %s", errs.String())
	}
	if !strings.Contains(out.String(), "start each app that runs mai mcp again") {
		t.Fatalf("mai upgrade removed the record of the keychain item agent:cursor@mai, which is gone, "+
			"and the record of agent:claude@mai, but it gives no step to start the apps again:\n%s", out.String())
	}
}
