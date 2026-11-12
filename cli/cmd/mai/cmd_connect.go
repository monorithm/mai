package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/monorithm/mai/cli/internal/api"
	"github.com/monorithm/mai/cli/internal/connect"
	"github.com/monorithm/mai/cli/internal/manifest"
	"github.com/monorithm/mai/cli/internal/paths"
)

func init() {
	register("connect", "Write the MCP entry and the hooks of mai in the files of an app", func(args []string) int {
		return runConnect(args, os.Stdin, os.Stdout, os.Stderr)
	})
	register("disconnect", "Remove the entries that mai connect wrote for an app", func(args []string) int {
		return runDisconnect(args, os.Stdout, os.Stderr)
	})
}

// appArgs gives the app name and the flags, with the name before or after the flags.
func appArgs(fs *flag.FlagSet, args []string) (string, error) {
	app := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		app, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	if app == "" && fs.NArg() > 0 {
		app = fs.Arg(0)
		if fs.NArg() > 1 {
			return "", errors.New("too many arguments")
		}
	} else if fs.NArg() > 0 {
		return "", errors.New("too many arguments")
	}
	if !connect.Known(app) {
		return "", fmt.Errorf("unknown app %q; use one of %s", app, strings.Join(connect.Apps, ", "))
	}
	return app, nil
}

// appManifest opens the manifest of the instance of config.json.
func appManifest() (*manifest.Manifest, error) {
	d, err := paths.Default()
	if err != nil {
		return nil, err
	}
	cfg, err := api.LoadConfig(d.Config)
	if err != nil {
		return nil, err
	}
	return manifest.Load(filepath.Join(d.State, manifest.FileName), cfg.Instance)
}

func runConnect(args []string, in io.Reader, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("connect", flag.ContinueOnError)
	fs.SetOutput(errOut)
	agent := fs.String("agent", "", "the name of the agent account (necessary)")
	yes := fs.Bool("yes", false, "write the changes with no question")
	app, err := appArgs(fs, args)
	if err != nil || *agent == "" {
		if err != nil {
			fmt.Fprintln(errOut, "mai:", err)
		}
		fmt.Fprintln(errOut, "usage: mai connect APP --agent NAME [--yes]")
		return 2
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fail(errOut, err)
	}
	m, err := appManifest()
	if err != nil {
		return fail(errOut, err)
	}
	p, err := connect.Prepare(home, app, *agent)
	if err != nil {
		return fail(errOut, err)
	}
	if !p.Changed() {
		if err := p.Apply(m); err != nil {
			return fail(errOut, err)
		}
		fmt.Fprintf(out, "%s has the entries of mai already\n", app)
		return 0
	}
	fmt.Fprint(out, p.Diff())
	if !*yes {
		fmt.Fprint(out, "Write these changes? [y/N] ")
		line, _ := bufio.NewReader(in).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			fmt.Fprintln(out, "no change")
			return 1
		}
	}
	if err := p.Apply(m); err != nil {
		return fail(errOut, err)
	}
	fmt.Fprintf(out, "connected %s as agent %s; restart %s\n", app, *agent, app)
	return 0
}

func runDisconnect(args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("disconnect", flag.ContinueOnError)
	fs.SetOutput(errOut)
	force := fs.Bool("force", false, "remove also the entries that changed after mai connect")
	app, err := appArgs(fs, args)
	if err != nil {
		fmt.Fprintln(errOut, "mai:", err)
		fmt.Fprintln(errOut, "usage: mai disconnect APP [--force]")
		return 2
	}
	m, err := appManifest()
	if err != nil {
		return fail(errOut, err)
	}
	if *force {
		for _, it := range m.Of(manifest.AppEntry) {
			if it.Name != app {
				continue
			}
			if err := connect.ForceRemoveEntry(it); err != nil {
				return fail(errOut, err)
			}
			if err := m.Remove(it); err != nil {
				return fail(errOut, err)
			}
		}
	}
	if err := connect.Disconnect(app, m); err != nil {
		for _, e := range unwrapAll(err) {
			var ce *connect.ErrChanged
			if errors.As(e, &ce) {
				fmt.Fprintln(errOut, "mai:", ce.Error())
				fmt.Fprint(errOut, ce.Diff)
				continue
			}
			fmt.Fprintln(errOut, "mai:", e)
		}
		fmt.Fprintf(errOut, "mai: some entries stay; remove them by hand, or run mai disconnect %s --force\n", app)
		return 1
	}
	fmt.Fprintf(out, "disconnected %s; restart %s\n", app, app)
	return 0
}

func unwrapAll(err error) []error {
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		return j.Unwrap()
	}
	return []error{err}
}
