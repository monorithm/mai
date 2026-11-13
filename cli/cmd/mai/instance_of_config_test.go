package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
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
	"github.com/monorithm/mai/cli/v3/internal/engine"
	"github.com/monorithm/mai/cli/v3/internal/keys"
	"github.com/monorithm/mai/cli/v3/internal/manifest"
	"github.com/monorithm/mai/cli/v3/internal/paths"
)

// Two instances share one MAI_HOME. config.json names the instance mai-wf-b and its port. A
// fake server of mai-wf-b listens on that port and records the bearer of each request.
const (
	instA = "mai-wf-a"
	instB = "mai-wf-b"
	// Each key of mai-wf-a ends with "keyofa", thus the record of the server of mai-wf-b
	// shows a key of mai-wf-a.
	fileKeyOfA   = "mai1:owner:filekeyofa"
	legacyKeyOfA = "mai1:owner:legacykeyofa"
)

type bearerLog struct {
	mu   sync.Mutex
	seen []string
}

func (b *bearerLog) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	b.seen = append(b.seen, r.Method+" "+r.URL.Path+" "+strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	b.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(401)
	_, _ = io.WriteString(w, `{"error":{"code":"BAD_KEY","message":"bad key"}}`)
}

// keysOfA gives each request to the server of mai-wf-b that had a key of mai-wf-a.
func (b *bearerLog) keysOfA() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []string
	for _, s := range b.seen {
		if strings.HasSuffix(s, "keyofa") {
			out = append(out, s[:strings.LastIndexByte(s, ' ')])
		}
	}
	return out
}

// serverOfB starts the fake server of mai-wf-b and writes config.json with its port.
func serverOfB(t *testing.T) (*bearerLog, string) {
	t.Helper()
	log := &bearerLog{}
	s := httptest.NewServer(log)
	t.Cleanup(s.Close)
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(s.URL, "http://"))
	dir := filepath.Join(os.Getenv("MAI_HOME"), "config")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, config.FileName), []byte(`{"instance":"`+instB+`","port":`+port+`}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return log, port
}

// configPort gives the port of config.json of the test, the port of the fake server of the
// instance of the file. With no config.json, it gives 0. Then no check of the owner keys
// occurs, and no test sends a key to the default port, which a real server can use.
func configPort(t *testing.T) int {
	t.Helper()
	dirs, err := paths.Default()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(config.Path(dirs.Config))
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Port int `json:"port"`
	}
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	return c.Port
}

// "mai upgrade --instance mai-wf-a" does the check of the two owner keys of mai-wf-a on the
// server of mai-wf-a. It must not send these keys to the port of config.json, which is the port
// of mai-wf-b. Here mai-wf-a has no container, thus the port of its server is not known.
func TestTheMoveOfTheKeysOfAnInstanceSendsNoKeyToTheServerOfTheInstanceOfConfig(t *testing.T) {
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
	s, err := lcOpen(config.Overrides{Instance: instA}, false)
	if err != nil {
		t.Fatal(err)
	}
	_, errs := captureLifecycle(t)
	_ = lcMoveKeys(instA, s.serverPort(), m)
	if got := log.keysOfA(); len(got) > 0 {
		t.Errorf("mai upgrade --instance %s sent the owner keys of %s to the server of %s: %v\nstderr: %s",
			instA, instA, instB, got, errs.String())
	}
	// The step of the conflict must name the instance, else "mai exec" runs in the container
	// of the instance of config.json.
	if strings.Contains(errs.String(), "mai exec -- mai owner set-key --replace") || !strings.Contains(errs.String(), "mai upgrade --instance "+instA) {
		t.Errorf("the step for %s does not name the instance:\n%s", instA, errs.String())
	}
}

// "mai start --instance mai-wf-a" makes mai-wf-a the instance of config.json. The port of
// config.json must then be the port of mai-wf-a, not the port of mai-wf-b. Else the subsequent
// commands of mai-wf-a, for example "mai agent list" and the bridges, send the keys of
// mai-wf-a to the server of mai-wf-b.
func TestStartWithADifferentInstanceKeepsNoPortOfTheInstanceOfConfig(t *testing.T) {
	t.Setenv("MAI_HOME", t.TempDir())
	log, portB := serverOfB(t)
	k := useTestKeys(t)
	if err := k.file.Set(keys.OwnerAccount(instA), fileKeyOfA); err != nil {
		t.Fatal(err)
	}
	// The engine is not necessary to show the change of config.json: the start writes the
	// flags to config.json before it connects to the engine.
	old := lcConnectEngine
	lcConnectEngine = func(context.Context, io.Writer) (*engine.Engine, error) {
		return nil, errors.New("test: no engine")
	}
	t.Cleanup(func() { lcConnectEngine = old })
	captureLifecycle(t)
	_ = runStart([]string{"--instance", instA})
	cfg, err := config.Load(filepath.Join(os.Getenv("MAI_HOME"), "config"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Instance == instA && strconv.Itoa(cfg.Port) == portB {
		t.Errorf("after mai start --instance %s, config.json names %s with the port %s of %s", instA, instA, portB, instB)
	}
	var out, errOut strings.Builder
	_ = runAgent([]string{"list"}, &out, &errOut)
	if got := log.keysOfA(); len(got) > 0 {
		t.Errorf("after mai start --instance %s, mai agent list sent the owner key of %s to the server of %s: %v",
			instA, instA, instB, got)
	}
}

// config.json names mai-wf-b with its host data folder. The container of a different instance
// must not get that folder: "mai start --instance mai-wf-a --port N" for a new instance, and
// "mai upgrade --instance mai-wf-a", make the container of mai-wf-a from the spec of the
// session.
func TestTheSpecOfADifferentInstanceHasNoDataFolderOfTheInstanceOfConfig(t *testing.T) {
	t.Setenv("MAI_HOME", t.TempDir())
	dataB := filepath.Join(t.TempDir(), "data-of-b")
	dir := filepath.Join(os.Getenv("MAI_HOME"), "config")
	if err := config.Update(dir, config.Overrides{Instance: instB, Port: 18993, DataDir: dataB}); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"--instance", instA}, {"--instance", instA, "--port", "18992"}} {
		var o config.Overrides
		fs := lcFlags("start")
		lcInstanceFlags(fs, &o)
		if err := fs.Parse(args); err != nil {
			t.Fatal(err)
		}
		s, err := lcOpen(o, false)
		if err != nil {
			t.Fatal(err)
		}
		if sp := s.spec(); sp.DataDir == dataB {
			t.Errorf("with %v, the container of %s gets the data folder %s of %s", args, instA, dataB, instB)
		}
		if sp := s.spec(); len(args) == 2 && sp.Port == 18993 {
			t.Errorf("with %v, the container of %s gets the port 18993 of %s", args, instA, instB)
		}
	}
	// "mai start --instance mai-wf-a --port 18992" writes its flags to config.json first.
	if err := config.Update(dir, config.Overrides{Instance: instA, Port: 18992}); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DataDir == dataB {
		t.Errorf("after mai start --instance %s --port 18992, config.json gives %s the data folder %s of %s", instA, instA, dataB, instB)
	}
}
