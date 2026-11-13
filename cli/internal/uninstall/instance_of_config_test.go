package uninstall

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/keys"
	"github.com/monorithm/mai/cli/v3/internal/manifest"
)

// "mai uninstall --instance mai-dev" ends the leases of mai-dev with the owner key of mai-dev.
// config.json names the instance mai and the port of the server of mai. The uninstall must not
// send the owner key of mai-dev to that server.
func TestUninstallOfAnInstanceSendsNoKeyToTheServerOfTheInstanceOfConfig(t *testing.T) {
	e := setup(t)
	addInstance(t, e, "mai-dev")
	var mu sync.Mutex
	var bearers []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		bearers = append(bearers, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		mu.Unlock()
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(s.Close)
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(s.URL, "http://"))
	// The server of mai listens on the port of config.json.
	if err := os.WriteFile(filepath.Join(e.dirs.Config, ConfigFile), []byte(`{"instance":"mai","port":`+port+`}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	u := e.uninstaller(&out)
	u.Instance = "mai-dev"
	u.BaseURL = "" // the uninstall finds the server
	_ = u.Run(context.Background())
	mu.Lock()
	defer mu.Unlock()
	for _, b := range bearers {
		if b == "mai1:owner:mai-dev" {
			t.Fatalf("mai uninstall --instance mai-dev sent the owner key of mai-dev to the server of mai on the port of config.json:\n%s", out.String())
		}
	}
}

// ownerServer is a fake server of one instance. It records the bearer of each request, and
// its owner API lists no leased session.
type ownerServer struct {
	mu      sync.Mutex
	bearers []string
	port    int
}

func newOwnerServer(t *testing.T) *ownerServer {
	t.Helper()
	o := &ownerServer{}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		o.mu.Lock()
		o.bearers = append(o.bearers, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		o.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sessions":[]}`))
	}))
	t.Cleanup(s.Close)
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(s.URL, "http://"))
	o.port, _ = strconv.Atoi(port)
	return o
}

func (o *ownerServer) seen() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.bearers...)
}

// The uninstall of mai-dev ends the leases on the host port of the container of mai-dev. The
// server of mai on the port of config.json gets no request.
func TestUninstallEndsTheLeasesOnThePortOfTheContainerOfTheInstance(t *testing.T) {
	e := setup(t)
	addInstance(t, e, "mai-dev")
	dev, mai := newOwnerServer(t), newOwnerServer(t)
	i := e.eng.findContainer("mai-dev")
	e.eng.containers[i].Port, e.eng.containers[i].Running = dev.port, true
	if err := os.WriteFile(filepath.Join(e.dirs.Config, ConfigFile), []byte(`{"instance":"mai","port":`+strconv.Itoa(mai.port)+`}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	u := e.uninstaller(&out)
	u.Instance, u.BaseURL = "mai-dev", ""
	_ = u.Run(context.Background())
	if got := dev.seen(); len(got) != 1 || got[0] != "mai1:owner:mai-dev" {
		t.Errorf("the server of mai-dev got the bearers %q", got)
	}
	if got := mai.seen(); len(got) != 0 {
		t.Errorf("the server of mai got %d request(s)", len(got))
	}
	if !strings.Contains(out.String(), "Leases: 0 ended.") {
		t.Errorf("the output has no line for the leases:\n%s", out.String())
	}

	// When the container does not run, the port of its server is not known.
	e = setup(t)
	addInstance(t, e, "mai-dev")
	out.Reset()
	u = e.uninstaller(&out)
	u.Instance, u.BaseURL = "mai-dev", ""
	_ = u.Run(context.Background())
	if want := "Leases: not ended, because the container mai-dev does not run."; !strings.Contains(out.String(), want) {
		t.Errorf("the output has no %q:\n%s", want, out.String())
	}
}

// The size of the mail data counts only the volumes and the data folder of the instance. The
// data folder of config.json is of a different instance here.
func TestTheSizeOfTheMailDataCountsNoDataFolderOfADifferentInstance(t *testing.T) {
	e := setup(t)
	addInstance(t, e, "mai-dev")
	data := filepath.Join(t.TempDir(), "data-of-mai")
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "mail"), make([]byte, 1<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.dirs.Config, ConfigFile), []byte(`{"instance":"mai","port":8765,"data_dir":"`+data+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	u := e.uninstaller(&out)
	u.Instance = "mai-dev"
	p, err := u.MakePlan()
	if err != nil {
		t.Fatal(err)
	}
	u.Show(context.Background(), p)
	// The fake engine gives each volume 2048 bytes: the two volumes of mai-dev.
	if !strings.Contains(out.String(), "Size of the mail data: 4.0 KiB\n") {
		t.Errorf("the size of the mail data of mai-dev counts a different folder:\n%s", out.String())
	}
}

// manifestOf31WithTwoInstances writes the manifest that mai 3.1 wrote for the instance mai and
// for mai-dev with a host data folder: the file has the instance mai, and no record has an
// instance. mai 3.1 wrote the records of mai-dev with "mai start --instance mai-dev
// --data-dir PATH". It gives the data folder of mai-dev.
func manifestOf31WithTwoInstances(t *testing.T, e *env) string {
	t.Helper()
	data := filepath.Join(t.TempDir(), "mai-dev-data")
	if err := os.MkdirAll(filepath.Join(data, "pg"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "pg", "PG_VERSION"), []byte("16\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.eng.containers = append(e.eng.containers, fakeContainer{ID: "c-dev", Name: "mai-dev", ImageID: "sha256:img3", Labels: managed("mai-dev")})
	e.eng.volumes[SpoolVolume("mai-dev")] = managed("mai-dev")
	if err := e.keys.Set(keys.OwnerAccount("mai-dev"), "mai1:owner:mai-dev"); err != nil {
		t.Fatal(err)
	}
	man := `{
  "instance": "mai",
  "items": [
    {"type": "folder", "name": "config", "path": "` + e.dirs.Config + `"},
    {"type": "folder", "name": "state", "path": "` + e.dirs.State + `"},
    {"type": "volume", "name": "mai3-data"},
    {"type": "volume", "name": "mai3-spool"},
    {"type": "container", "name": "mai", "id": "c1"},
    {"type": "volume", "name": "mai-dev3-spool"},
    {"type": "folder", "name": "data", "path": "` + data + `"},
    {"type": "container", "name": "mai-dev", "id": "c-dev"}
  ]
}`
	if err := os.WriteFile(filepath.Join(e.dirs.State, manifest.FileName), []byte(man), 0o600); err != nil {
		t.Fatal(err)
	}
	// "mai start --instance mai-dev" of 3.1 also wrote config.json.
	if err := os.WriteFile(filepath.Join(e.dirs.Config, ConfigFile), []byte(`{"instance":"mai-dev","port":18766,"data_dir":"`+data+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return data
}

// In a manifest of mai 3.1, "mai uninstall --instance mai" must not remove the data folder of
// mai-dev, because the container of mai-dev keeps its mail in it.
func TestUninstallOfAnInstanceKeepsTheDataFolderOfADifferentInstanceInAManifestOf31(t *testing.T) {
	e := setup(t)
	data := manifestOf31WithTwoInstances(t, e)
	var out bytes.Buffer
	u := e.uninstaller(&out)
	u.Instance = "mai"
	_ = u.Run(context.Background())
	if _, err := os.Stat(filepath.Join(data, "pg", "PG_VERSION")); err != nil {
		t.Fatalf("mai uninstall --instance mai removed the data folder %s of mai-dev: %v\n%s", data, err, out.String())
	}
}

// In a manifest of mai 3.1, "mai uninstall --instance mai-dev" removes the data folder of
// mai-dev, or reports it as an item that stays.
func TestUninstallOfAnInstanceRemovesOrReportsItsDataFolderInAManifestOf31(t *testing.T) {
	e := setup(t)
	data := manifestOf31WithTwoInstances(t, e)
	var out bytes.Buffer
	u := e.uninstaller(&out)
	u.Instance = "mai-dev"
	_ = u.Run(context.Background())
	if _, err := os.Stat(data); err == nil && !strings.Contains(out.String(), data) {
		t.Fatalf("mai uninstall --instance mai-dev keeps its data folder %s and does not report it:\n%s", data, out.String())
	}
}
