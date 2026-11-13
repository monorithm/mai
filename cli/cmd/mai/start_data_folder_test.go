package main

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/config"
	"github.com/monorithm/mai/cli/v3/internal/manifest"
)

// freePort gives a port on 127.0.0.1 that no program uses at the time of the call.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// The container of mai-wf-b mounts its data folder. "mai start --instance mai-wf-a" with the
// same folder as --data-dir must not make a container. Else two servers and two PostgreSQL
// processes use one data folder. The start also must not record the folder for mai-wf-a, and
// must not change config.json.
func TestStartRefusesTheDataFolderThatAContainerOfADifferentInstanceMounts(t *testing.T) {
	t.Setenv("MAI_HOME", t.TempDir())
	useTestKeys(t)
	dataB := filepath.Join(t.TempDir(), "data-of-b")
	if err := os.MkdirAll(dataB, 0o700); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(os.Getenv("MAI_HOME"), "config")
	if err := config.Update(dir, config.Overrides{Instance: instB, Port: 18993, DataDir: dataB}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(config.Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	f := &engineFake{
		ctrs:   []fakeCtr{{ID: "cb", Name: instB, Image: "sha256:img", Instance: instB, Port: 18993, Data: dataB, Running: true}},
		images: map[string]string{"mai:test": "sha256:img"},
	}
	useEngineFake(t, f)
	_, errs := captureLifecycle(t)
	port := strconv.Itoa(freePort(t))
	for _, data := range []string{dataB, filepath.Join(dataB, "a")} {
		code := runStart([]string{"--instance", instA, "--port", port, "--data-dir", data, "--image", "mai:test", "--no-pull", "--skip-owner"})
		if code == 0 || !strings.Contains(errs.String(), "the container "+instB+" of the instance "+instB+" mounts the folder") {
			t.Errorf("mai start --instance %s --data-dir %s gave exit %d:\n%s", instA, data, code, errs.String())
		}
	}
	if f.called("POST", "/containers/create") || f.called("POST", "/volumes/create") {
		t.Errorf("the start made a volume or a container of %s", instA)
	}
	m, err := manifest.Load(filepath.Join(os.Getenv("MAI_HOME"), "state", manifest.FileName), instA)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range m.Of(manifest.Folder) {
		if it.Name == "data" {
			t.Errorf("the manifest records the data folder %s for %s", it.Path, it.Instance)
		}
	}
	if after, _ := os.ReadFile(config.Path(dir)); string(after) != string(before) {
		t.Errorf("a start that failed changed config.json:\n%s", after)
	}
}
