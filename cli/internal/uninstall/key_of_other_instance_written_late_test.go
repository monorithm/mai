package uninstall

import (
	"bytes"
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/moby/moby/client"

	"github.com/monorithm/mai/cli/v3/internal/keychain"
	"github.com/monorithm/mai/cli/v3/internal/keys"
	"github.com/monorithm/mai/cli/v3/internal/manifest"
)

// armOnDelete is a legacy keychain that arms the engine below at the delete of an item.
type armOnDelete struct {
	*keychain.Memory
	armed *atomic.Bool
}

func (a armOnDelete) Delete(account string) error {
	a.armed.Store(true)
	return a.Memory.Delete(account)
}

// lateMoveEngine is the fake engine. At the first list of the containers after the delete of
// a legacy item, a bridge of a different instance moves its agent key from the keychain to
// the key file, as at its first lease request.
type lateMoveEngine struct {
	*fakeEngine
	armed  *atomic.Bool
	fired  atomic.Bool
	bridge *keys.File
	agent  string
	moved  string
}

func (l *lateMoveEngine) ContainerList(ctx context.Context, o client.ContainerListOptions) (client.ContainerListResult, error) {
	if l.armed.Load() && !l.fired.Swap(true) {
		l.moved, _ = l.bridge.Get(l.agent)
	}
	return l.fakeEngine.ContainerList(ctx, o)
}

// The instance mai-dev is an install of mai 3.1 in the same state folder: its container, its
// records, and the keychain item of its agent. Its bridge moves the agent key to the key file
// while mai uninstall removes the instance mai. The key of mai-dev must stay in the key file or
// in the keychain.
func TestUninstallKeepsAKeyOfADifferentInstanceThatABridgeMovesDuringTheRemove(t *testing.T) {
	e := setup(t)
	const dev = "mai-dev"
	devAgent := keys.AgentAccount(dev, "cursor")
	e.eng.containers = append(e.eng.containers, fakeContainer{ID: "c-dev", Name: dev, ImageID: "sha256:img3", Labels: managed(dev)})
	e.eng.volumes[DataVolume(dev)] = managed(dev)
	e.eng.volumes[SpoolVolume(dev)] = managed(dev)
	man, err := manifest.Load(filepath.Join(e.dirs.State, manifest.FileName), dev)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range []manifest.Item{
		{Type: manifest.Container, Name: dev, ID: "c-dev"},
		{Type: manifest.Volume, Name: DataVolume(dev)},
		{Type: manifest.Volume, Name: SpoolVolume(dev)},
		{Type: manifest.KeychainKey, Name: devAgent},
	} {
		if err := man.Add(it); err != nil {
			t.Fatal(err)
		}
	}
	e.kc.Put(devAgent, "mai1:cursor:devsecret")

	armed := &atomic.Bool{}
	e.keys = keys.Open(e.dirs.State, armOnDelete{Memory: e.kc, armed: armed})
	eng := &lateMoveEngine{fakeEngine: e.eng, armed: armed, bridge: keys.Open(e.dirs.State, e.kc), agent: devAgent}
	var out bytes.Buffer
	u := e.uninstaller(&out)
	u.Engine = eng
	u.Instance = "mai"
	_ = u.Run(context.Background())
	if eng.moved != "mai1:cursor:devsecret" {
		t.Fatalf("the bridge of %s did not move its key", dev)
	}
	inKeychain, _ := e.kc.Has(devAgent)
	inFile, err := keys.Open(e.dirs.State, nil).Has(devAgent)
	if err != nil {
		t.Fatal(err)
	}
	if !inKeychain && !inFile {
		t.Fatalf("the key %s of the instance %s is gone: it is not in the key file and not in the keychain:\n%s", devAgent, dev, out.String())
	}
}
