package main

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moby/moby/client"

	"github.com/monorithm/mai/cli/v3/internal/keys"
	"github.com/monorithm/mai/cli/v3/internal/manifest"
	"github.com/monorithm/mai/cli/v3/internal/manifest311"
	"github.com/monorithm/mai/cli/v3/internal/paths"
	"github.com/monorithm/mai/cli/v3/internal/uninstall"
)

const legacyClaudeKey = "mai1:claude:legacyclaudesecret"

// noImageEngine is an empty engine that also has no image.
type noImageEngine struct{ emptyEngine }

func (noImageEngine) ImageInspect(_ context.Context, id string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
	return client.ImageInspectResult{}, uninstall.NotFoundError{What: id}
}

// install311 makes the state of the 3.1.1 install of the user: the instance mai with the
// default configuration, three keychain items, and a manifest that the manifest code of 3.1.1
// wrote in the sequence of mai start, mai agent add cursor claude, and mai connect.
func install311(t *testing.T) (*fakeOwner, *testKeyFile, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("MAI_HOME", filepath.Join(home, "maihome"))
	owner := &fakeOwner{}
	lf := &leaseFake{valid: map[string]string{}}
	ownerAPI := owner.handler(t)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mcp" || strings.HasPrefix(r.URL.Path, "/api/leases") {
			lf.ServeHTTP(w, r)
			return
		}
		ownerAPI.ServeHTTP(w, r)
	}))
	t.Cleanup(s.Close)
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(s.URL, "http://"))
	dirs, _ := paths.Default()
	_ = os.MkdirAll(dirs.Config, 0o700)
	// mai 3.1.1 wrote only the fields that a flag changed. The instance is the default.
	if err := os.WriteFile(filepath.Join(dirs.Config, "config.json"), []byte(`{"port":`+port+`,"approval":"each-bridge"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	k := useTestKeys(t)
	k.legacy.Put("owner@mai", testOwnerKey)
	k.legacy.Put("agent:cursor@mai", legacyCursorKey)
	k.legacy.Put("agent:claude@mai", legacyClaudeKey)

	m, err := manifest311.Load(filepath.Join(dirs.State, manifest311.FileName), "mai")
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range []manifest311.Item{
		{Type: manifest311.Folder, Name: "config", Path: dirs.Config},
		{Type: manifest311.Folder, Name: "state", Path: dirs.State},
		{Type: manifest311.Image, Name: "ghcr.io/monorithm/mai:3.1.1", ID: "sha256:0311"},
		{Type: manifest311.Volume, Name: "mai3-data"},
		{Type: manifest311.Volume, Name: "mai3-spool"},
		{Type: manifest311.Container, Name: "mai", ID: "c0311"},
		{Type: manifest311.KeychainKey, Name: "owner@mai"},
		{Type: manifest311.KeychainKey, Name: "agent:cursor@mai"},
		{Type: manifest311.KeychainKey, Name: "agent:claude@mai"},
	} {
		if err := m.Add(it); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(m.Path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(raw), `"instance"`) != 1 {
		t.Fatalf("the manifest of 3.1.1 has an instance in a record:\n%s", raw)
	}
	return owner, k, home
}

func TestTheManifestOf311GivesEachRecordTheInstanceMai(t *testing.T) {
	_, _, home := install311(t)
	m := loadTestManifest(t, home)
	for _, it := range m.Items {
		if it.Instance != "mai" {
			t.Errorf("the record %s %s has the instance %q", it.Type, it.Name, it.Instance)
		}
	}
	if got := strings.Join(m.Instances(), ","); got != "mai" {
		t.Fatalf("the instances of the manifest are %q", got)
	}
	if n := len(m.OfInstance("mai", manifest.KeychainKey)); n != 3 {
		t.Fatalf("the manifest has %d keychain records of the instance mai", n)
	}
}

func TestDoctorAndUpgradeMoveEachItemOfThe311Manifest(t *testing.T) {
	_, k, home := install311(t)
	ok, bad := doctorRun(t)
	if len(bad) != 1 || !strings.Contains(bad[0], "3 key(s) of the instance mai") {
		t.Fatalf("doctor: ok %v, bad %v", ok, bad)
	}
	for _, a := range []string{"owner@mai", "agent:cursor@mai", "agent:claude@mai"} {
		if !strings.Contains(bad[0], a) {
			t.Fatalf("doctor does not name %s: %v", a, bad)
		}
	}
	out, errs := captureLifecycle(t)
	if !lcMoveKeys("mai", configPort(t), loadTestManifest(t, home)) {
		t.Fatalf("the move failed: %s", errs.String())
	}
	noSecret(t, "mai upgrade", out.String()+errs.String())
	got := fileKeys(t, k)
	want := map[string]string{"owner@mai": testOwnerKey, "agent:cursor@mai": legacyCursorKey, "agent:claude@mai": legacyClaudeKey}
	for a, v := range want {
		if got[a] != v {
			t.Errorf("the key file has no correct key for %s", a)
		}
	}
	if n := len(k.legacy.Accounts()); n != 0 {
		t.Fatalf("%d keychain item(s) stay: %v", n, k.legacy.Accounts())
	}
	if items := loadTestManifest(t, home).Of(manifest.KeychainKey); len(items) != 0 {
		t.Fatalf("the manifest records %+v", items)
	}
	ok, bad = doctorRun(t)
	if len(bad) != 0 || !strings.Contains(strings.Join(ok, "\n"), "holds 3 key(s) of the instance mai") {
		t.Fatalf("doctor after the move: ok %v, bad %v", ok, bad)
	}
}

func TestUninstallRemovesEachItemOfThe311Manifest(t *testing.T) {
	_, k, home := install311(t)
	dirs, _ := paths.Default()
	kf, _ := openKeys()
	var out bytes.Buffer
	u := &uninstall.Uninstaller{Engine: noImageEngine{}, Keys: kf, Dirs: dirs, Yes: true, Out: &out}
	if code := u.Run(context.Background()); code != 0 {
		t.Fatalf("mai uninstall: exit %d\n%s", code, out.String())
	}
	noSecret(t, "mai uninstall", out.String())
	for _, a := range []string{"owner@mai", "agent:cursor@mai", "agent:claude@mai"} {
		if !strings.Contains(out.String(), "gone     keychain dev.mai / "+a+"\n") {
			t.Errorf("the output has no line for %s:\n%s", a, out.String())
		}
	}
	if n := len(k.legacy.Accounts()); n != 0 {
		t.Fatalf("%d keychain item(s) stay: %v", n, k.legacy.Accounts())
	}
	for _, d := range []string{"config", "state", "cache"} {
		if _, err := os.Stat(filepath.Join(home, "maihome", d)); !os.IsNotExist(err) {
			t.Fatalf("the folder %s stays", d)
		}
	}
	_ = keys.FileName
}
