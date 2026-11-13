package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/config"
	"github.com/monorithm/mai/cli/v3/internal/keys"
	"github.com/monorithm/mai/cli/v3/internal/manifest"
)

// ownerServerOfA is a fake server of mai-wf-a. Its owner API accepts only fileKeyOfA.
func ownerServerOfA(t *testing.T) (port int, seen func() int) {
	t.Helper()
	var mu sync.Mutex
	n := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		n++
		mu.Unlock()
		if r.URL.Path == "/api/agents" && r.Header.Get("Authorization") == "Bearer "+fileKeyOfA {
			_, _ = w.Write([]byte(`{"agents":[]}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(s.Close)
	return s.Listener.Addr().(*net.TCPAddr).Port, func() int { mu.Lock(); defer mu.Unlock(); return n }
}

// upgradeOfA makes the state of two instances: config.json names mai-wf-b, and the engine has
// the container ctr of mai-wf-a. The key file and the keychain item hold two different owner
// keys of mai-wf-a.
func upgradeOfA(t *testing.T, ctr fakeCtr) (*bearerLog, *testKeyFile, *engineFake) {
	t.Helper()
	t.Setenv("MAI_HOME", t.TempDir())
	log, _ := serverOfB(t)
	k := useTestKeys(t)
	if err := k.file.Set(keys.OwnerAccount(instA), fileKeyOfA); err != nil {
		t.Fatal(err)
	}
	k.legacy.Put(keys.OwnerAccount(instA), legacyKeyOfA)
	m, err := manifest.Load(filepath.Join(os.Getenv("MAI_HOME"), "state", manifest.FileName), instA)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Add(manifest.Item{Type: manifest.KeychainKey, Name: keys.OwnerAccount(instA)}); err != nil {
		t.Fatal(err)
	}
	f := &engineFake{ctrs: []fakeCtr{ctr}, images: map[string]string{"mai:test": "sha256:new"}}
	useEngineFake(t, f)
	return log, k, f
}

// "mai upgrade --instance mai-wf-a" does the check of the two owner keys on the port of the
// container of mai-wf-a. The server of mai-wf-a accepts only the key of the key file, thus the
// keychain item goes. No key of mai-wf-a goes to the server of mai-wf-b.
func TestUpgradeChecksTheOwnerKeysOnThePortOfTheContainerOfTheInstance(t *testing.T) {
	portA, seen := ownerServerOfA(t)
	log, k, _ := upgradeOfA(t, fakeCtr{ID: "ca", Name: instA, Image: "sha256:new", Instance: instA, Port: portA, Running: true})
	out, errs := captureLifecycle(t)
	code := runUpgrade([]string{"--instance", instA, "--image", "mai:test", "--no-pull"})
	if got := log.keysOfA(); len(got) > 0 {
		t.Errorf("mai upgrade --instance %s sent the owner keys of %s to the server of %s: %v", instA, instA, instB, got)
	}
	if seen() != 2 {
		t.Errorf("the server of %s got %d checks, want 2", instA, seen())
	}
	if ok, _ := k.legacy.Has(keys.OwnerAccount(instA)); ok || code != 0 {
		t.Fatalf("exit %d; the keychain item stays: %v\nstdout: %s\nstderr: %s", code, ok, out.String(), errs.String())
	}
}

// When a different program uses the port of the container of mai-wf-a, the new container
// cannot start. The upgrade must find this before it stops and removes the old container.
func TestUpgradeKeepsTheContainerWhenADifferentProgramUsesItsPort(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(other.Close)
	portA := other.Listener.Addr().(*net.TCPAddr).Port
	_, _, f := upgradeOfA(t, fakeCtr{ID: "ca", Name: instA, Image: "sha256:old", Instance: instA, Port: portA})
	_, errs := captureLifecycle(t)
	if code := runUpgrade([]string{"--instance", instA, "--image", "mai:test", "--no-pull"}); code == 0 {
		t.Fatal("the upgrade gave exit 0")
	}
	if f.called("POST", "/containers/ca/stop") || f.called("DELETE", "/containers/ca") {
		t.Errorf("the upgrade stopped or removed the container of %s before it found that its port is in use:\n%s", instA, errs.String())
	}
	if want := "127.0.0.1:" + strconv.Itoa(portA) + " is in use"; !strings.Contains(errs.String(), want) {
		t.Errorf("the error does not tell %q:\n%s", want, errs.String())
	}
}

// The new container of mai-wf-a gets the port and the data folder of the old container of
// mai-wf-a, not the values of config.json, which names mai-wf-b. The upgrade also does not
// change config.json of mai-wf-b.
func TestUpgradeMakesTheNewContainerWithThePortAndTheDataFolderOfTheOldContainer(t *testing.T) {
	dataA := filepath.Join(t.TempDir(), "data-of-a")
	if err := os.MkdirAll(dataA, 0o700); err != nil {
		t.Fatal(err)
	}
	portA := freePort(t)
	_, _, f := upgradeOfA(t, fakeCtr{ID: "ca", Name: instA, Image: "sha256:old", Instance: instA, Port: portA, Data: dataA, Running: true})
	dir := filepath.Join(os.Getenv("MAI_HOME"), "config")
	if err := config.Update(dir, config.Overrides{Instance: instB, Port: freePort(t), DataDir: filepath.Join(t.TempDir(), "data-of-b")}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(config.Path(dir))
	_, errs := captureLifecycle(t)
	_ = runUpgrade([]string{"--instance", instA, "--image", "mai:test", "--no-pull"})
	if len(f.created) != 1 {
		t.Fatalf("the upgrade made %d containers:\n%s", len(f.created), errs.String())
	}
	if got := createdPort(f.created[0]); got != strconv.Itoa(portA) {
		t.Errorf("the new container of %s has the port %s, want the port %d of the old container", instA, got, portA)
	}
	if got := createdData(f.created[0]); got != dataA {
		t.Errorf("the new container of %s has the data %s, want the folder %s of the old container", instA, got, dataA)
	}
	if after, _ := os.ReadFile(config.Path(dir)); string(after) != string(before) {
		t.Errorf("the upgrade of %s changed config.json of %s:\n%s", instA, instB, after)
	}
}

// Each step for mai-wf-a names --instance mai-wf-a, because config.json names mai-wf-b, and each
// command acts on the instance of config.json by default.
func TestTheStepsOfTheMoveNameTheInstanceThatConfigDoesNotName(t *testing.T) {
	t.Setenv("MAI_HOME", t.TempDir())
	serverOfB(t)
	refused := keys.Conflict{Account: keys.OwnerAccount(instA), Checked: true}
	for _, tc := range []struct{ text, want string }{
		{lcConflictText(instA, "keys.json", refused), "mai exec --instance " + instA + " -- mai owner set-key --replace"},
		{lcConflictText(instA, "keys.json", keys.Conflict{Account: keys.OwnerAccount(instA)}), "mai upgrade --instance " + instA + " again"},
		{lcConflictText(instA, "keys.json", keys.Conflict{Account: keys.AgentAccount(instA, "cursor")}), "mai start --instance " + instA},
		{lcStayStep(keys.OwnerAccount(instA)), "mai upgrade --instance " + instA + " again"},
	} {
		if !strings.Contains(tc.text, tc.want) {
			t.Errorf("the step does not have %q: %s", tc.want, tc.text)
		}
	}
	// For the instance of config.json, the step names no instance.
	if text := lcConflictText(instB, "keys.json", keys.Conflict{Account: keys.OwnerAccount(instB), Checked: true}); !strings.Contains(text, "mai exec -- mai owner set-key --replace") {
		t.Errorf("the step for %s is %s", instB, text)
	}
}
