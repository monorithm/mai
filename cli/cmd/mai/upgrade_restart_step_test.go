package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/keychain"
	"github.com/monorithm/mai/cli/v3/internal/keys"
	"github.com/monorithm/mai/cli/v3/internal/manifest"
	"github.com/monorithm/mai/cli/v3/internal/paths"
)

func TestUpgradeTellsToStartTheAppsAgainAfterTheMoveOfAnAgentKey(t *testing.T) {
	for _, tc := range []struct {
		name  string
		items []string
		step  bool
	}{
		{"an agent key and the owner key", []string{keys.OwnerAccount("mai"), keys.AgentAccount("mai", "cursor")}, true},
		{"only the owner key", []string{keys.OwnerAccount("mai")}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MAI_HOME", t.TempDir())
			var out, errs bytes.Buffer
			oldOut, oldErr := lcOut, lcErr
			lcOut, lcErr = &out, &errs
			t.Cleanup(func() { lcOut, lcErr = oldOut, oldErr })
			legacy := keychain.NewMemory()
			var records []manifest.Item
			for _, a := range tc.items {
				legacy.Put(a, "mai1:x:"+a)
				records = append(records, manifest.Item{Type: manifest.KeychainKey, Name: a})
			}
			dirs, _ := paths.Default()
			old := openKeys
			openKeys = func() (*keys.File, error) { return keys.Open(dirs.State, legacy), nil }
			t.Cleanup(func() { openKeys = old })
			if !lcMoveKeys("mai", configPort(t), testManifest(t, records...)) {
				t.Fatalf("the move gave false: %s", errs.String())
			}
			text := out.String()
			got := strings.Contains(text, "start each app that runs mai mcp again") && strings.Contains(text, "AGENT_EXISTS")
			if got != tc.step {
				t.Fatalf("the step to start the apps again is in the output: %v, want %v:\n%s", got, tc.step, text)
			}
		})
	}
}
