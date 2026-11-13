package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/moby/moby/client"

	"github.com/monorithm/mai/cli/v3/internal/config"
	"github.com/monorithm/mai/cli/v3/internal/engine"
)

// "mai doctor --instance mai-wf-a" must examine the port of the container of mai-wf-a, not the
// port of config.json, which names mai-wf-b.
func TestDoctorOfADifferentInstanceExaminesThePortOfItsContainer(t *testing.T) {
	t.Setenv("MAI_HOME", t.TempDir())
	useTestKeys(t)
	serverA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(serverA.Close)
	portA := serverA.Listener.Addr().(*net.TCPAddr).Port
	portB := freePort(t)
	dir := filepath.Join(os.Getenv("MAI_HOME"), "config")
	if err := config.Update(dir, config.Overrides{Instance: instB, Port: portB}); err != nil {
		t.Fatal(err)
	}
	f := &engineFake{ctrs: []fakeCtr{{ID: "ca", Name: instA, Image: "sha256:img", Instance: instA, Port: portA, Running: true}}}
	s := httptest.NewServer(f)
	t.Cleanup(s.Close)
	api, err := client.New(client.WithHost("tcp://"+strings.TrimPrefix(s.URL, "http://")), client.WithAPIVersion("1.47"))
	if err != nil {
		t.Fatal(err)
	}
	old := lcFindEngine
	lcFindEngine = func(context.Context) (*engine.Engine, error) { return &engine.Engine{API: api}, nil }
	t.Cleanup(func() { lcFindEngine = old })
	out, _ := captureLifecycle(t)
	_ = runDoctor([]string{"--instance", instA})
	text := out.String()
	if !strings.Contains(text, "127.0.0.1:"+strconv.Itoa(portA)+" is in use by mai") {
		t.Errorf("mai doctor --instance %s does not examine the port %d of its container:\n%s", instA, portA, text)
	}
	if strings.Contains(text, "127.0.0.1:"+strconv.Itoa(portB)) {
		t.Errorf("mai doctor --instance %s examines the port %d of %s:\n%s", instA, portB, instB, text)
	}
}
