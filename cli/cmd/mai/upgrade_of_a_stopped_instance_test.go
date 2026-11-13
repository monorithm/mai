package main

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// The container of mai-wf-a is stopped, and a different server runs on the host port of that
// container. "mai start --instance NAME --port N" accepts the port N of a stopped container,
// because the port is free. The port of the stopped container of mai-wf-a is then not the port
// of the server of mai-wf-a. Thus "mai upgrade --instance mai-wf-a" must not do the check of
// the owner keys of mai-wf-a on that port.
func TestUpgradeOfAStoppedInstanceSendsNoKeyToTheServerOnItsPort(t *testing.T) {
	t.Setenv("MAI_HOME", t.TempDir())
	other, portOther := serverOfB(t)
	p, err := strconv.Atoi(portOther)
	if err != nil {
		t.Fatal(err)
	}
	// upgradeOfA uses a new MAI_HOME with a config.json of mai-wf-b and a second fake server.
	// The stopped container of mai-wf-a has the port of the first fake server.
	_, _, _ = upgradeOfA(t, fakeCtr{ID: "ca", Name: instA, Image: "sha256:old", Instance: instA, Port: p, Running: false})
	_, errs := captureLifecycle(t)
	_ = runUpgrade([]string{"--instance", instA, "--image", "mai:test", "--no-pull"})
	if got := other.keysOfA(); len(got) > 0 {
		t.Errorf("mai upgrade --instance %s sent the owner keys of %s to the server on the port %d of its stopped container, "+
			"which is a different server: %v\nstderr: %s", instA, instA, p, got, errs.String())
	}
}

// The same state for "mai status --instance mai-wf-a": the line "server" must not show the
// version of the different server on the port of the stopped container.
func TestStatusOfAStoppedInstanceShowsNoVersionOfADifferentServer(t *testing.T) {
	t.Setenv("MAI_HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"version":"9.9.9-of-a-different-server","contract":"3.2.0"}`)
	}))
	t.Cleanup(srv.Close)
	p := srv.Listener.Addr().(*net.TCPAddr).Port
	serverOfB(t)
	useTestKeys(t)
	useEngineFake(t, &engineFake{ctrs: []fakeCtr{{ID: "ca", Name: instA, Image: "sha256:old", Instance: instA, Port: p, Running: false}}})
	out, _ := captureLifecycle(t)
	_ = runStatus([]string{"--instance", instA})
	if strings.Contains(out.String(), "9.9.9-of-a-different-server") {
		t.Errorf("mai status --instance %s shows the version of the server on the port %d of its stopped container:\n%s", instA, p, out.String())
	}
}
