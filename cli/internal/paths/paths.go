// Package paths gives the configuration, state, and cache folders of the CLI for each OS.
package paths

import (
	"os"
	"path/filepath"
	"runtime"
)

// Dirs are the folders of the CLI.
type Dirs struct {
	Config string
	State  string
	Cache  string
}

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// Default returns the folders for the current user. MAI_HOME, if set, puts all three under
// one folder; the tests use it.
func Default() (Dirs, error) {
	if h := os.Getenv("MAI_HOME"); h != "" {
		return Dirs{Config: filepath.Join(h, "config"), State: filepath.Join(h, "state"), Cache: filepath.Join(h, "cache")}, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Dirs{}, err
	}
	switch runtime.GOOS {
	case "darwin":
		base := filepath.Join(home, "Library", "Application Support", "mai")
		return Dirs{Config: base, State: filepath.Join(base, "state"), Cache: filepath.Join(home, "Library", "Caches", "mai")}, nil
	case "windows":
		app := env("AppData", filepath.Join(home, "AppData", "Roaming"))
		local := env("LocalAppData", filepath.Join(home, "AppData", "Local"))
		return Dirs{Config: filepath.Join(app, "mai"), State: filepath.Join(local, "mai", "state"), Cache: filepath.Join(local, "mai", "cache")}, nil
	default:
		return Dirs{
			Config: filepath.Join(env("XDG_CONFIG_HOME", filepath.Join(home, ".config")), "mai"),
			State:  filepath.Join(env("XDG_STATE_HOME", filepath.Join(home, ".local", "state")), "mai"),
			Cache:  filepath.Join(env("XDG_CACHE_HOME", filepath.Join(home, ".cache")), "mai"),
		}, nil
	}
}
