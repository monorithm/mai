package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"

	"github.com/moby/moby/client"

	"github.com/monorithm/mai/cli/v3/internal/paths"
	"github.com/monorithm/mai/cli/v3/internal/uninstall"
)

func init() {
	register("uninstall", "remove mai: the container, the data, the keys, and the CLI", runUninstall)
}

// uninstallEngine connects to the engine. It uses DOCKER_HOST, else the first socket of
// Docker Desktop, Docker, or Podman that exists.
func uninstallEngine() (*client.Client, error) {
	opts := []client.Opt{client.FromEnv, client.WithAPIVersionNegotiation()}
	if os.Getenv("DOCKER_HOST") == "" && runtime.GOOS != "windows" {
		home, _ := os.UserHomeDir()
		candidates := []string{
			filepath.Join(home, ".docker", "run", "docker.sock"),
			"/var/run/docker.sock",
			filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "podman", "podman.sock"),
			filepath.Join(home, ".local", "share", "containers", "podman", "machine", "podman.sock"),
		}
		for _, s := range candidates {
			if fi, err := os.Stat(s); err == nil && fi.Mode()&os.ModeSocket != 0 {
				opts = append(opts, client.WithHost("unix://"+s))
				break
			}
		}
	}
	return client.New(opts...)
}

func uninstallBinary() *uninstall.Binary {
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return &uninstall.Binary{
		Path: exe,
		GOOS: runtime.GOOS,
		Run: func(name string, args ...string) error {
			c := exec.Command(name, args...)
			c.Stdout, c.Stderr = os.Stdout, os.Stderr
			return c.Run()
		},
		Start: func(name string, args ...string) error {
			return exec.Command(name, args...).Start()
		},
	}
}

func runUninstall(args []string) int {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "agree with no typed word, for a script")
	backup := fs.String("backup", "", "write a backup archive to `PATH` before the remove")
	legacy := fs.Bool("legacy", false, "remove the install of mai 2.x")
	instance := fs.String("instance", "", "the `NAME` of the instance (default: the instance of config.json)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "mai uninstall: unknown argument %q\n", fs.Arg(0))
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	eng, err := uninstallEngine()
	if err != nil {
		fmt.Fprintln(os.Stderr, "mai uninstall: no connection to the engine:", err)
		return 1
	}
	defer eng.Close()
	if *legacy {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintln(os.Stderr, "mai uninstall:", err)
			return 1
		}
		l := &uninstall.Legacy{
			Engine:   eng,
			Home:     home,
			AppFiles: uninstall.LegacyAppFiles(home, runtime.GOOS),
			Yes:      *yes,
			In:       os.Stdin,
			Out:      os.Stdout,
		}
		return l.Run(ctx)
	}
	dirs, err := paths.Default()
	if err != nil {
		fmt.Fprintln(os.Stderr, "mai uninstall:", err)
		return 1
	}
	kf, err := openKeys()
	if err != nil {
		fmt.Fprintln(os.Stderr, "mai uninstall:", err)
		return 1
	}
	u := &uninstall.Uninstaller{
		Engine:   eng,
		Keys:     kf,
		Dirs:     dirs,
		Instance: *instance,
		Yes:      *yes,
		Backup:   *backup,
		In:       os.Stdin,
		Out:      os.Stdout,
		Binary:   uninstallBinary(),
	}
	return u.Run(ctx)
}
