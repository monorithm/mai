package uninstall

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/keys"
	"github.com/monorithm/mai/cli/v3/internal/manifest"
)

// addInstance records a second instance in the same state folder, as "mai start --instance
// NAME" does: its container and volumes in the engine and in the manifest, and its owner key
// in the key file.
func addInstance(t *testing.T, e *env, name string) {
	t.Helper()
	e.eng.containers = append(e.eng.containers, fakeContainer{ID: "c-" + name, Name: name, ImageID: "sha256:img3", Labels: managed(name)})
	e.eng.volumes[DataVolume(name)] = managed(name)
	e.eng.volumes[SpoolVolume(name)] = managed(name)
	man, err := manifest.Load(filepath.Join(e.dirs.State, manifest.FileName), name)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range []manifest.Item{
		{Type: manifest.Container, Name: name, ID: "c-" + name},
		{Type: manifest.Volume, Name: DataVolume(name)},
		{Type: manifest.Volume, Name: SpoolVolume(name)},
	} {
		if err := man.Add(it); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.keys.Set(keys.OwnerAccount(name), "mai1:owner:"+name); err != nil {
		t.Fatal(err)
	}
}

func TestUninstallOfOneInstanceKeepsTheContainerAndTheVolumesOfADifferentInstance(t *testing.T) {
	e := setup(t)
	addInstance(t, e, "mai-dev")
	u := e.uninstaller(&bytes.Buffer{})
	u.Instance = "mai-dev"
	var out bytes.Buffer
	u.Out = &out
	_ = u.Run(context.Background())
	if e.eng.findContainer("mai") < 0 {
		t.Errorf("mai uninstall --instance mai-dev removed the container of the instance mai:\n%s", out.String())
	}
	for _, v := range []string{DataVolume("mai"), SpoolVolume("mai")} {
		if _, ok := e.eng.volumes[v]; !ok {
			t.Errorf("mai uninstall --instance mai-dev removed the volume %s of the instance mai", v)
		}
	}
}

func TestUninstallOfOneInstanceKeepsTheKeychainRecordsOfADifferentInstance(t *testing.T) {
	e := setup(t)
	addInstance(t, e, "mai-dev")
	u := e.uninstaller(&bytes.Buffer{})
	u.Instance = "mai-dev"
	var out bytes.Buffer
	u.Out = &out
	_ = u.Run(context.Background())
	// The keychain item of cursor of the instance mai stays, because it is not of mai-dev.
	if ok, _ := e.kc.Has(keys.AgentAccount("mai", "cursor")); !ok {
		t.Fatal("mai uninstall --instance mai-dev removed a keychain item of the instance mai")
	}
	// The key file keeps the keys of mai, thus mai is still installed. Its record of the
	// keychain item must stay, because the move of mai upgrade, mai doctor, and mai uninstall
	// find an agent item that the key file does not have only through its record.
	m, err := manifest.Load(filepath.Join(e.dirs.State, manifest.FileName), "mai")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, it := range m.Of(manifest.KeychainKey) {
		if it.Name == keys.AgentAccount("mai", "cursor") {
			found = true
		}
	}
	if !found {
		t.Fatalf("after mai uninstall --instance mai-dev, the manifest has no record of the keychain item agent:cursor@mai of the instance mai:\n%s", out.String())
	}
	after := keys.Open(e.dirs.State, e.kc)
	cands, err := after.Candidates("mai", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range cands {
		if a == keys.AgentAccount("mai", "cursor") {
			return
		}
	}
	t.Fatalf("with no record, the candidates %v of the instance mai do not hold agent:cursor@mai, thus mai upgrade does not move it", cands)
}
