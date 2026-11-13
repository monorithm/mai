package main

import (
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/config"
	"github.com/monorithm/mai/cli/v3/internal/keys"
	"github.com/monorithm/mai/cli/v3/internal/manifest"
)

// The container of the instance mai is stopped, thus its server does not use the port of
// config.json. A different program uses that port. The key file and the keychain item hold
// two different owner keys of mai. "mai upgrade" must not send these keys to that program,
// because no server of mai answers on the port.
func TestUpgradeSendsNoOwnerKeyToAPortWhenTheContainerOfTheInstanceIsStopped(t *testing.T) {
	t.Setenv("MAI_HOME", t.TempDir())
	other := &bearerLog{}
	s := httptest.NewServer(other)
	t.Cleanup(s.Close)
	port := s.Listener.Addr().(*net.TCPAddr).Port
	dir := filepath.Join(os.Getenv("MAI_HOME"), "config")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, config.FileName), []byte(`{"instance":"mai","port":`+strconv.Itoa(port)+`}`), 0o600); err != nil {
		t.Fatal(err)
	}
	k := useTestKeys(t)
	if err := k.file.Set(keys.OwnerAccount("mai"), fileKeyOfA); err != nil {
		t.Fatal(err)
	}
	k.legacy.Put(keys.OwnerAccount("mai"), legacyKeyOfA)
	m, err := manifest.Load(filepath.Join(os.Getenv("MAI_HOME"), "state", manifest.FileName), "mai")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Add(manifest.Item{Type: manifest.KeychainKey, Name: keys.OwnerAccount("mai")}); err != nil {
		t.Fatal(err)
	}
	f := &engineFake{ctrs: []fakeCtr{{ID: "c1", Name: "mai", Image: "sha256:new", Instance: "mai", Port: port, Running: false}},
		images: map[string]string{"mai:test": "sha256:new"}}
	useEngineFake(t, f)
	_, _ = captureLifecycle(t)
	_ = runUpgrade([]string{"--image", "mai:test", "--no-pull"})
	if got := other.keysOfA(); len(got) > 0 {
		t.Fatalf("mai upgrade sent the owner keys of mai to a different program on the port %d: %v", port, got)
	}
}
