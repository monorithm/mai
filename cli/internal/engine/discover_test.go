package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

type fakeEnv struct {
	vars  map[string]string
	files map[string]string
}

func (f fakeEnv) env(goos string) Env {
	return Env{
		GOOS:   goos,
		Home:   "/home/u",
		Getenv: func(k string) string { return f.vars[k] },
		ReadFile: func(p string) ([]byte, error) {
			s, ok := f.files[p]
			if !ok {
				return nil, os.ErrNotExist
			}
			return []byte(s), nil
		},
		Exists: func(p string) bool { _, ok := f.files[p]; return ok },
	}
}

func hosts(c []Candidate) []string {
	var out []string
	for _, x := range c {
		out = append(out, x.Host)
	}
	return out
}

func TestDiscoverDockerHostFirst(t *testing.T) {
	f := fakeEnv{vars: map[string]string{"DOCKER_HOST": "tcp://127.0.0.1:2375"}, files: map[string]string{}}
	c := Discover(f.env("linux"))
	if c[0].Host != "tcp://127.0.0.1:2375" || c[0].Source != "DOCKER_HOST" {
		t.Fatalf("first candidate is %+v", c[0])
	}
}

func TestDiscoverCurrentContext(t *testing.T) {
	sum := sha256.Sum256([]byte("colima"))
	meta := filepath.Join("/home/u", ".docker", "contexts", "meta", hex.EncodeToString(sum[:]), "meta.json")
	f := fakeEnv{vars: map[string]string{}, files: map[string]string{
		filepath.Join("/home/u", ".docker", "config.json"): `{"currentContext":"colima"}`,
		meta: `{"Name":"colima","Endpoints":{"docker":{"Host":"unix:///home/u/.colima/docker.sock"}}}`,
	}}
	c := Discover(f.env("darwin"))
	if c[0].Host != "unix:///home/u/.colima/docker.sock" || c[0].Source != "docker context colima" {
		t.Fatalf("first candidate is %+v", c[0])
	}
}

func TestDiscoverDefaultContextIsSkipped(t *testing.T) {
	f := fakeEnv{vars: map[string]string{}, files: map[string]string{
		filepath.Join("/home/u", ".docker", "config.json"): `{"currentContext":"default"}`,
	}}
	c := Discover(f.env("linux"))
	if len(c) != 1 || c[0].Host != "unix:///var/run/docker.sock" {
		t.Fatalf("candidates are %v", hosts(c))
	}
}

func TestDiscoverDarwinSockets(t *testing.T) {
	desk := filepath.Join("/home/u", ".docker", "run", "docker.sock")
	pod := filepath.Join("/home/u", ".local", "share", "containers", "podman", "machine", "podman.sock")
	f := fakeEnv{vars: map[string]string{}, files: map[string]string{desk: "", pod: ""}}
	c := Discover(f.env("darwin"))
	got := hosts(c)
	if len(got) != 2 || got[0] != "unix://"+desk || got[1] != "unix://"+pod {
		t.Fatalf("candidates are %v", got)
	}
	if c[1].Kind != KindPodman {
		t.Fatalf("kind is %v", c[1].Kind)
	}
}

func TestDiscoverDarwinNothingGivesDesktopDefault(t *testing.T) {
	f := fakeEnv{vars: map[string]string{}, files: map[string]string{}}
	c := Discover(f.env("darwin"))
	if len(c) != 1 || c[0].Host != "unix://"+filepath.Join("/home/u", ".docker", "run", "docker.sock") {
		t.Fatalf("candidates are %v", hosts(c))
	}
}

func TestDiscoverLinuxRootlessAndPodman(t *testing.T) {
	f := fakeEnv{vars: map[string]string{"XDG_RUNTIME_DIR": "/run/user/1000"}, files: map[string]string{
		filepath.Join("/run/user/1000", "podman", "podman.sock"): "",
	}}
	c := Discover(f.env("linux"))
	got := hosts(c)
	if len(got) != 2 || got[0] != "unix://"+filepath.Join("/run/user/1000", "podman", "podman.sock") || got[1] != "unix:///var/run/docker.sock" {
		t.Fatalf("candidates are %v", got)
	}
	if c[0].Kind != KindPodman {
		t.Fatalf("kind is %v", c[0].Kind)
	}
}

func TestDiscoverWindowsPipes(t *testing.T) {
	f := fakeEnv{vars: map[string]string{}, files: map[string]string{}}
	got := hosts(Discover(f.env("windows")))
	if len(got) != 2 || got[0] != "npipe:////./pipe/docker_engine" || got[1] != "npipe:////./pipe/podman-machine-default" {
		t.Fatalf("candidates are %v", got)
	}
}

func TestInstallHintOneLine(t *testing.T) {
	for _, g := range []string{"darwin", "linux", "windows"} {
		h := InstallHint(g)
		if h == "" {
			t.Fatalf("no hint for %s", g)
		}
		for _, r := range h {
			if r == '\n' {
				t.Fatalf("hint for %s has more than one line", g)
			}
		}
	}
}
