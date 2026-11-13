package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/moby/moby/client"

	"github.com/monorithm/mai/cli/v3/internal/config"
	"github.com/monorithm/mai/cli/v3/internal/engine"
	"github.com/monorithm/mai/cli/v3/internal/keys"
)

// execFake adds the exec calls of the Engine API to engineFake. "mai health" gives exit 0,
// and "mai owner set-key" gives exit 3: the server has an owner key.
type execFake struct {
	*engineFake
	mu    sync.Mutex
	n     int
	execs map[string][]string
	stdin map[string]bool
}

func (f *execFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := apiVersionPrefix.ReplaceAllString(r.URL.Path, "")
	parts := strings.Split(strings.Trim(p, "/"), "/")
	switch {
	case r.Method == http.MethodPost && len(parts) == 3 && parts[0] == "containers" && parts[2] == "exec":
		var body struct {
			Cmd         []string
			AttachStdin bool
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.n++
		id := fmt.Sprintf("exec%d", f.n)
		f.execs[id] = body.Cmd
		f.stdin[id] = body.AttachStdin
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"Id": id})
	case r.Method == http.MethodPost && len(parts) == 3 && parts[0] == "exec" && parts[2] == "start":
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		_, _ = buf.WriteString("HTTP/1.1 101 UPGRADED\r\nContent-Type: application/vnd.docker.raw-stream\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
		_ = buf.Flush()
		f.mu.Lock()
		in := f.stdin[parts[1]]
		f.mu.Unlock()
		if in {
			// Read stdin to its end, thus the close sends no reset to the client.
			_, _ = io.Copy(io.Discard, buf)
		}
		_ = conn.Close()
	case r.Method == http.MethodGet && len(parts) == 3 && parts[0] == "exec" && parts[2] == "json":
		f.mu.Lock()
		cmd := f.execs[parts[1]]
		f.mu.Unlock()
		code := 0
		if len(cmd) >= 3 && cmd[1] == "owner" && cmd[2] == "set-key" {
			code = lcOwnerKeyExists
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ID": parts[1], "Running": false, "ExitCode": code})
	default:
		f.engineFake.ServeHTTP(w, r)
	}
}

func useExecFake(t *testing.T, f *execFake) {
	t.Helper()
	s := httptest.NewServer(f)
	t.Cleanup(s.Close)
	api, err := client.New(client.WithHost("tcp://"+strings.TrimPrefix(s.URL, "http://")), client.WithAPIVersion("1.47"))
	if err != nil {
		t.Fatal(err)
	}
	e := &engine.Engine{API: api}
	old, oldFind := lcConnectEngine, lcFindEngine
	lcConnectEngine = func(context.Context, io.Writer) (*engine.Engine, error) { return e, nil }
	lcFindEngine = func(context.Context) (*engine.Engine, error) { return e, nil }
	t.Cleanup(func() { lcConnectEngine, lcFindEngine = old, oldFind })
}

// portOfA is the host port of the container of mai-wf-a in the tests of this file. No server
// listens on it.
const portOfA = 18994

// configOfA writes config.json with the instance mai-wf-a and its port, and gives the fake
// engine with the running container of mai-wf-a on that port. The fake server of mai-wf-b
// listens on the port that the function gives.
func configOfA(t *testing.T) (*bearerLog, string, *execFake) {
	t.Helper()
	t.Setenv("MAI_HOME", t.TempDir())
	log, portB := serverOfB(t)
	dir := filepath.Join(os.Getenv("MAI_HOME"), "config")
	if err := os.WriteFile(filepath.Join(dir, config.FileName),
		[]byte(`{"instance":"`+instA+`","port":`+strconv.Itoa(portOfA)+`}`), 0o600); err != nil {
		t.Fatal(err)
	}
	k := useTestKeys(t)
	if err := k.file.Set(keys.OwnerAccount(instA), fileKeyOfA); err != nil {
		t.Fatal(err)
	}
	f := &execFake{engineFake: &engineFake{ctrs: []fakeCtr{
		{ID: "ca", Name: instA, Image: "sha256:img", Instance: instA, Port: portOfA, Running: true},
	}}, execs: map[string][]string{}, stdin: map[string]bool{}}
	useExecFake(t, f)
	return log, portB, f
}

// config.json names mai-wf-a, and the container of mai-wf-a runs on its port. "mai start
// --port N" does not make the container again, thus the container keeps its port. config.json
// must then keep the port of the container, or the start must refuse the flag. Else the
// commands of mai-wf-a, for example "mai agent list" and the bridges, send the keys of
// mai-wf-a to the server on the port N, here the server of mai-wf-b.
func TestStartWithAPortFlagKeepsThePortOfTheContainerOfTheInstanceOfConfig(t *testing.T) {
	log, portB, _ := configOfA(t)
	out, errs := captureLifecycle(t)
	code := runStart([]string{"--port", portB})
	cfg, err := config.Load(filepath.Join(os.Getenv("MAI_HOME"), "config"))
	if err != nil {
		t.Fatal(err)
	}
	if code == 0 && cfg.Port != portOfA {
		t.Errorf("mai start --port %s gave exit 0, and config.json gives %s the port %d. The container of %s has the port %d.\nstdout: %s\nstderr: %s",
			portB, instA, cfg.Port, instA, portOfA, out.String(), errs.String())
	}
	if code == 0 && !strings.Contains(errs.String(), "--port "+portB+" has no effect") {
		t.Errorf("mai start --port %s does not tell that the flag has no effect:\n%s", portB, errs.String())
	}
	var aOut, aErr strings.Builder
	_ = runAgent([]string{"list"}, &aOut, &aErr)
	if got := log.keysOfA(); len(got) > 0 {
		t.Errorf("after mai start --port %s, mai agent list sent the owner key of %s to the server of %s: %v", portB, instA, instB, got)
	}
}

// config.json names mai-wf-a, and the container of mai-wf-a keeps its mail in the volume
// mai-wf-a3-data. "mai start --data-dir PATH" does not make the container again, thus the
// container keeps the volume. config.json must then not get the folder, or the start must
// refuse the flag. Else the next "mai upgrade" makes the container of mai-wf-a with the empty
// folder, and the mail and the keys of the server stay in a volume that no container mounts.
func TestStartWithADataFolderFlagKeepsTheVolumeOfTheContainerOfTheInstanceOfConfig(t *testing.T) {
	_, _, f := configOfA(t)
	f.engineFake.images = map[string]string{"mai:test": "sha256:new"}
	f.engineFake.ctrs[0].Image = "sha256:old"
	dataDir := filepath.Join(t.TempDir(), "new-data")
	out, errs := captureLifecycle(t)
	code := runStart([]string{"--data-dir", dataDir})
	cfg, err := config.Load(filepath.Join(os.Getenv("MAI_HOME"), "config"))
	if err != nil {
		t.Fatal(err)
	}
	if code == 0 && cfg.DataDir != "" {
		t.Errorf("mai start --data-dir %s gave exit 0, and config.json gives %s that folder. The container of %s uses the volume %s.\nstdout: %s\nstderr: %s",
			dataDir, instA, instA, engine.DataVolume(instA), out.String(), errs.String())
	}
	if code == 0 && !strings.Contains(errs.String(), "--data-dir "+dataDir+" has no effect") {
		t.Errorf("mai start --data-dir %s does not tell that the flag has no effect:\n%s", dataDir, errs.String())
	}
	_ = runUpgrade([]string{"--image", "mai:test", "--no-pull"})
	if len(f.engineFake.created) != 1 {
		t.Fatalf("the upgrade made %d containers:\n%s", len(f.engineFake.created), errs.String())
	}
	if got := createdData(f.engineFake.created[0]); got != engine.DataVolume(instA) {
		t.Errorf("the upgrade makes the new container of %s with the data %s, not with the volume %s of the old container",
			instA, got, engine.DataVolume(instA))
	}
}
