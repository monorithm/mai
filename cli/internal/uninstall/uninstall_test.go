package uninstall

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/monorithm/mai/cli/internal/keychain"
	"github.com/monorithm/mai/cli/internal/manifest"
	"github.com/monorithm/mai/cli/internal/paths"
)

func managed(instance string) map[string]string {
	return map[string]string{LabelManagedBy: ManagedByValue, LabelInstance: instance}
}

type env struct {
	eng  *fakeEngine
	kc   *keychain.Memory
	dirs paths.Dirs
	bin  string
	app  string
}

func setup(t *testing.T) *env {
	t.Helper()
	home := t.TempDir()
	t.Setenv("MAI_HOME", home)
	dirs, err := paths.Default()
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{dirs.Config, dirs.State, dirs.Cache, filepath.Join(dirs.State, "notices")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dirs.Config, ConfigFile), []byte(`{"instance":"mai","port":8765}`), 0o600); err != nil {
		t.Fatal(err)
	}
	e := &env{eng: newFakeEngine(), kc: keychain.NewMemory(), dirs: dirs}
	e.bin = filepath.Join(home, "bin", "mai")
	_ = os.MkdirAll(filepath.Dir(e.bin), 0o700)
	_ = os.WriteFile(e.bin, []byte("binary"), 0o700)
	e.app = filepath.Join(home, "app.json")

	e.eng.containers = []fakeContainer{
		{ID: "c1", Name: "mai", ImageID: "sha256:img3", Labels: managed("mai")},
		{ID: "c9", Name: "other", ImageID: "sha256:shared"},
	}
	e.eng.volumes["mai3-data"] = managed("mai")
	e.eng.volumes["mai3-spool"] = managed("mai")
	e.eng.volumes["unrelated"] = nil
	e.eng.images = []fakeImage{
		{ID: "sha256:img3", Tags: []string{"ghcr.io/monorithm/mai:3.0.0"}},
		{ID: "sha256:shared", Tags: []string{"busybox:latest"}},
	}
	_ = e.kc.Set(keychain.Service, keychain.OwnerAccount("mai"), "mai1:owner:x")
	_ = e.kc.Set(keychain.Service, keychain.AgentAccount("mai", "cursor"), "mai1:cursor:x")

	man, err := manifest.Load(filepath.Join(dirs.State, manifest.FileName), "mai")
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range []manifest.Item{
		{Type: manifest.Container, Name: "mai", ID: "c1"},
		{Type: manifest.Volume, Name: "mai3-data"},
		{Type: manifest.Volume, Name: "mai3-spool"},
		{Type: manifest.Image, Name: "ghcr.io/monorithm/mai:3.0.0", ID: "sha256:img3"},
		{Type: manifest.Image, Name: "busybox:latest", ID: "sha256:shared"},
		{Type: manifest.KeychainKey, Name: keychain.OwnerAccount("mai")},
		{Type: manifest.KeychainKey, Name: keychain.AgentAccount("mai", "cursor")},
		{Type: manifest.AppEntry, Name: "cursor", Path: e.app, Pointer: "/mcpServers/mai"},
	} {
		if err := man.Add(it); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

func (e *env) uninstaller(out *bytes.Buffer) *Uninstaller {
	return &Uninstaller{
		Engine:   e.eng,
		Keychain: e.kc,
		Dirs:     e.dirs,
		Yes:      true,
		Out:      out,
		BaseURL:  "http://127.0.0.1:1",
		Binary:   &Binary{Path: e.bin, GOOS: "linux"},
	}
}

func stateOf(rs []Result, prefix string) string {
	for _, r := range rs {
		if strings.HasPrefix(r.Item, prefix) {
			return r.State
		}
	}
	return ""
}

func TestFullUninstallRemovesEachItem(t *testing.T) {
	e := setup(t)
	var seen []manifest.Item
	RemoveAppEntry = func(it manifest.Item) error { seen = append(seen, it); return nil }
	t.Cleanup(func() { RemoveAppEntry = nil })

	var out bytes.Buffer
	code := e.uninstaller(&out).Run(context.Background())
	if code != 0 {
		t.Fatalf("exit %d, output:\n%s", code, out.String())
	}
	if e.eng.findContainer("mai") >= 0 {
		t.Error("the container mai exists")
	}
	if e.eng.findContainer("other") < 0 {
		t.Error("the uninstall removed a container that is not of mai")
	}
	for _, v := range []string{"mai3-data", "mai3-spool"} {
		if _, ok := e.eng.volumes[v]; ok {
			t.Errorf("the volume %s exists", v)
		}
	}
	if _, ok := e.eng.volumes["unrelated"]; !ok {
		t.Error("the uninstall removed a volume that is not of mai")
	}
	if e.eng.findImage("sha256:img3") >= 0 {
		t.Error("the image of mai exists")
	}
	if e.eng.findImage("sha256:shared") < 0 {
		t.Error("the uninstall removed an image that a different container uses")
	}
	if len(seen) != 1 || seen[0].Name != "cursor" {
		t.Errorf("app entries removed: %v", seen)
	}
	for _, acct := range []string{keychain.OwnerAccount("mai"), keychain.AgentAccount("mai", "cursor")} {
		if _, err := e.kc.Get(keychain.Service, acct); !errors.Is(err, keychain.ErrNotFound) {
			t.Errorf("the keychain item %s exists", acct)
		}
	}
	for _, d := range []string{e.dirs.Config, e.dirs.State, e.dirs.Cache, e.bin} {
		if _, err := os.Stat(d); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s exists", d)
		}
	}
	text := out.String()
	for _, want := range []string{"container  mai", "volume     mai3-data", "Size of the mail data: 4.0 KiB", "gone     container mai", "kept     image sha256:shared", "gone     binary "} {
		if !strings.Contains(text, want) {
			t.Errorf("the output has no %q:\n%s", want, text)
		}
	}
}

func TestUninstallReportsEachFailure(t *testing.T) {
	e := setup(t)
	e.eng.stuck["mai3-spool"] = true
	e.eng.containers = append(e.eng.containers, fakeContainer{ID: "c5", Name: "stray", Labels: managed("mai")})
	var out bytes.Buffer
	u := e.uninstaller(&out)
	code := u.Run(context.Background())
	if code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, out.String())
	}
	text := out.String()
	for _, want := range []string{
		"failure  volume mai3-spool: test: the volume is in use",
		"failure  container /stray: it has the labels of mai and still exists",
		"failure  volume mai3-spool: it has the labels of mai and still exists",
		"skipped  app entry cursor",
		"WARNING: this CLI cannot remove the entry cursor",
		"gone     volume mai3-data",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the output has no %q:\n%s", want, text)
		}
	}
}

func TestUninstallKeepsAContainerWithNoLabelOfTheCLI(t *testing.T) {
	e := setup(t)
	e.eng.containers[0] = fakeContainer{ID: "c1", Name: "mai", Labels: map[string]string{LabelCompose: LegacyProject}}
	var out bytes.Buffer
	if code := e.uninstaller(&out).Run(context.Background()); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out.String())
	}
	if e.eng.findContainer("mai") < 0 {
		t.Fatal("the uninstall removed the 2.x container")
	}
	if !strings.Contains(out.String(), "kept     container mai: it has no label of the CLI") {
		t.Errorf("output:\n%s", out.String())
	}
}

func TestUninstallWantsTheTypedWord(t *testing.T) {
	e := setup(t)
	var out bytes.Buffer
	u := e.uninstaller(&out)
	u.Yes = false
	u.In = strings.NewReader("yes\n")
	if code := u.Run(context.Background()); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if len(e.eng.removed) != 0 {
		t.Fatalf("items removed with no agreement: %v", e.eng.removed)
	}
	if _, err := os.Stat(e.dirs.Config); err != nil {
		t.Fatal("the configuration folder is gone with no agreement")
	}
	u.In = strings.NewReader("uninstall\n")
	out.Reset()
	if code := u.Run(context.Background()); code != 0 {
		t.Fatalf("exit %d after the typed word:\n%s", code, out.String())
	}
}

func TestUninstallStopsWhenTheBackupFails(t *testing.T) {
	e := setup(t)
	var out bytes.Buffer
	u := e.uninstaller(&out)
	u.Backup = filepath.Join(t.TempDir(), "backup.tar")
	if code := u.Run(context.Background()); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if len(e.eng.removed) != 0 {
		t.Fatalf("items removed after a failed backup: %v", e.eng.removed)
	}
}

func TestEndLeasesDeletesEachLease(t *testing.T) {
	var mu sync.Mutex
	var deleted []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer mai1:owner:k" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/sessions":
			_, _ = w.Write([]byte(`{"sessions":[{"login":"cursor.s1","agent":"cursor","leased":true},{"login":"cursor.s2","agent":"cursor","leased":false},{"login":"claude.s1","agent":"claude","leased":true}]}`))
		case r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/lease"):
			mu.Lock()
			deleted = append(deleted, r.URL.Path)
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	kc := keychain.NewMemory()
	_ = kc.Set("svc", keychain.OwnerAccount("mai"), "mai1:owner:k")
	line := EndLeases(context.Background(), srv.Client(), srv.URL, kc, "svc", "mai")
	if line != "Leases: 2 ended." {
		t.Errorf("line %q", line)
	}
	if len(deleted) != 2 || deleted[0] != "/api/sessions/cursor.s1/lease" {
		t.Errorf("deleted %v", deleted)
	}
	if strings.Contains(line, "mai1:") {
		t.Error("the line shows the key")
	}
}

func TestEndLeasesNeverFails(t *testing.T) {
	kc := keychain.NewMemory()
	if line := EndLeases(context.Background(), nil, "http://127.0.0.1:1", kc, "svc", "mai"); !strings.Contains(line, "not ended") {
		t.Errorf("line %q", line)
	}
	_ = kc.Set("svc", keychain.OwnerAccount("mai"), "mai1:owner:k")
	if line := EndLeases(context.Background(), nil, "http://127.0.0.1:1", kc, "svc", "mai"); !strings.Contains(line, "not ended") {
		t.Errorf("line %q", line)
	}
}

func TestBinaryMethod(t *testing.T) {
	cases := []struct {
		path, goos, want string
	}{
		{"/opt/homebrew/Cellar/mai/3.0.0/bin/mai", "darwin", "brew"},
		{"/home/linuxbrew/.linuxbrew/Cellar/mai/3.0.0/bin/mai", "linux", "brew"},
		{`C:\Users\u\scoop\apps\mai\current\mai.exe`, "windows", "scoop"},
		{`C:\Users\u\bin\mai.exe`, "windows", "after-exit"},
		{"/usr/local/bin/mai", "linux", "file"},
	}
	for _, c := range cases {
		if got := (&Binary{Path: filepath.FromSlash(c.path), GOOS: c.goos}).Method(); got != c.want {
			t.Errorf("%s on %s: %s, want %s", c.path, c.goos, got, c.want)
		}
	}
}

func TestBinaryRunsThePackageManager(t *testing.T) {
	var ran []string
	b := &Binary{Path: "/opt/homebrew/Cellar/mai/3.0.0/bin/mai", GOOS: "darwin", Run: func(name string, args ...string) error {
		ran = append([]string{name}, args...)
		return nil
	}}
	if r := b.Remove(); r.State != Gone {
		t.Fatalf("result %+v", r)
	}
	if strings.Join(ran, " ") != "brew uninstall mai" {
		t.Errorf("ran %v", ran)
	}
	var started []string
	w := &Binary{Path: `C:\bin\mai.exe`, GOOS: "windows", Start: func(name string, args ...string) error {
		started = append([]string{name}, args...)
		return nil
	}}
	if r := w.Remove(); r.State != Gone {
		t.Fatalf("result %+v", r)
	}
	if len(started) != 3 || started[0] != "cmd" || !strings.Contains(started[2], `del "C:\bin\mai.exe"`) {
		t.Errorf("started %v", started)
	}
}

func TestIsNotFoundReadsTheAnswerOfTheEngine(t *testing.T) {
	for _, msg := range []string{
		"Error response from daemon: No such container: mai",
		"Error response from daemon: get mai3-data: no such volume",
		"Error response from daemon: No such image: mai:wf",
	} {
		if !IsNotFound(errors.New(msg)) {
			t.Errorf("%q is not a missing object", msg)
		}
	}
	if IsNotFound(errors.New("Error response from daemon: conflict: unable to remove")) {
		t.Error("a conflict is a missing object")
	}
	if !IsNotFound(NotFoundError{What: "x"}) || IsNotFound(nil) {
		t.Error("the error class is not used")
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	c, err := LoadConfig(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if c.Instance != "mai" || c.Port != 8765 {
		t.Errorf("config %+v", c)
	}
}
