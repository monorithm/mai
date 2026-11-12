// Package engine finds and operates the container engine (Docker Desktop, Docker Engine, or
// Podman) through the Engine API. It does not use the docker or podman command for the
// lifecycle of the container.
package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Kind tells which engine a candidate socket belongs to.
type Kind string

// Kinds of engines.
const (
	KindDocker Kind = "docker"
	KindPodman Kind = "podman"
)

// Candidate is one engine address to try.
type Candidate struct {
	Host   string // address for the Engine API, for example unix:///var/run/docker.sock
	Source string // where the address came from
	Kind   Kind
}

// Env is the environment that discovery reads. Tests give a fake one.
type Env struct {
	GOOS     string
	Home     string
	Getenv   func(string) string
	ReadFile func(string) ([]byte, error)
	Exists   func(string) bool
}

// SystemEnv returns the Env of this process.
func SystemEnv(goos string) Env {
	home, _ := os.UserHomeDir()
	return Env{
		GOOS:     goos,
		Home:     home,
		Getenv:   os.Getenv,
		ReadFile: os.ReadFile,
		Exists: func(p string) bool {
			_, err := os.Stat(p)
			return err == nil
		},
	}
}

func (e Env) dockerConfigDir() string {
	if d := e.Getenv("DOCKER_CONFIG"); d != "" {
		return d
	}
	return filepath.Join(e.Home, ".docker")
}

// currentContext returns the host of the current Docker context, or "" for the default
// context or for an error.
func (e Env) currentContext() (string, string) {
	name := e.Getenv("DOCKER_CONTEXT")
	dir := e.dockerConfigDir()
	if name == "" {
		b, err := e.ReadFile(filepath.Join(dir, "config.json"))
		if err != nil {
			return "", ""
		}
		var cfg struct {
			CurrentContext string `json:"currentContext"`
		}
		if json.Unmarshal(b, &cfg) != nil {
			return "", ""
		}
		name = cfg.CurrentContext
	}
	if name == "" || name == "default" {
		return "", ""
	}
	sum := sha256.Sum256([]byte(name))
	meta := filepath.Join(dir, "contexts", "meta", hex.EncodeToString(sum[:]), "meta.json")
	b, err := e.ReadFile(meta)
	if err != nil {
		return "", ""
	}
	var m struct {
		Endpoints map[string]struct {
			Host string `json:"Host"`
		} `json:"Endpoints"`
	}
	if json.Unmarshal(b, &m) != nil {
		return "", ""
	}
	return m.Endpoints["docker"].Host, name
}

func kindOf(host string) Kind {
	if strings.Contains(strings.ToLower(host), "podman") {
		return KindPodman
	}
	return KindDocker
}

// Discover returns the engine addresses to try, in sequence: DOCKER_HOST, the current
// Docker context, the default sockets of Docker Desktop and Docker Engine, then the default
// sockets of Podman. A socket file that does not exist is not in the list, but the
// default address of the OS is always in the list, so that the caller can start the engine.
func Discover(e Env) []Candidate {
	var out []Candidate
	seen := map[string]bool{}
	add := func(host, source string, kind Kind) {
		if host == "" || seen[host] {
			return
		}
		seen[host] = true
		out = append(out, Candidate{Host: host, Source: source, Kind: kind})
	}
	if h := e.Getenv("DOCKER_HOST"); h != "" {
		add(h, "DOCKER_HOST", kindOf(h))
	}
	if h, name := e.currentContext(); h != "" {
		add(h, "docker context "+name, kindOf(h))
	}
	sock := func(path, source string, kind Kind) {
		if e.Exists(path) {
			add("unix://"+path, source, kind)
		}
	}
	switch e.GOOS {
	case "windows":
		add("npipe:////./pipe/docker_engine", "Docker Desktop", KindDocker)
		add("npipe:////./pipe/podman-machine-default", "Podman", KindPodman)
	case "darwin":
		sock(filepath.Join(e.Home, ".docker", "run", "docker.sock"), "Docker Desktop", KindDocker)
		sock("/var/run/docker.sock", "Docker Desktop", KindDocker)
		sock(filepath.Join(e.Home, ".local", "share", "containers", "podman", "machine", "podman.sock"), "Podman", KindPodman)
		sock(filepath.Join(e.Home, ".local", "share", "containers", "podman", "machine", "podman-machine-default", "podman.sock"), "Podman", KindPodman)
		add("unix://"+filepath.Join(e.Home, ".docker", "run", "docker.sock"), "Docker Desktop", KindDocker)
	default:
		sock("/var/run/docker.sock", "Docker Engine", KindDocker)
		sock(filepath.Join(e.Home, ".docker", "desktop", "docker.sock"), "Docker Desktop", KindDocker)
		if x := e.Getenv("XDG_RUNTIME_DIR"); x != "" {
			sock(filepath.Join(x, "docker.sock"), "Docker Engine (rootless)", KindDocker)
			sock(filepath.Join(x, "podman", "podman.sock"), "Podman", KindPodman)
		}
		sock("/run/podman/podman.sock", "Podman", KindPodman)
		add("unix:///var/run/docker.sock", "Docker Engine", KindDocker)
	}
	return out
}

// InstallHint is the one install step for the OS when no engine is installed.
func InstallHint(goos string) string {
	switch goos {
	case "darwin":
		return "Install Docker Desktop: brew install --cask docker (or https://docs.docker.com/desktop/setup/install/mac-install/), then run mai start again."
	case "windows":
		return "Install Docker Desktop: winget install Docker.DockerDesktop, then run mai start again."
	default:
		return "Install Docker Engine (https://docs.docker.com/engine/install/) or Podman (https://podman.io/docs/installation), then run mai start again."
	}
}
