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

	"github.com/monorithm/mai/cli/v3/internal/api"
	"github.com/monorithm/mai/cli/v3/internal/connect"
	"github.com/monorithm/mai/cli/v3/internal/manifest"
	"github.com/monorithm/mai/cli/v3/internal/paths"
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

// connectUse is the usage line of mai connect.
const connectUse = "usage: mai connect APP --agent NAME [--dry-run | --yes]"

// isTerminal tells if r is a terminal. A reader that is not a file, for example the input of
// a test, is not a terminal.
func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	return ok && terminal(f)
}

func runConnect(args []string, in io.Reader, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("connect", flag.ContinueOnError)
	fs.SetOutput(errOut)
	agent := fs.String("agent", "", "the name of the agent account (necessary)")
	yes := fs.Bool("yes", false, "write the changes with no question")
	dryRun := fs.Bool("dry-run", false, "show the changes and write nothing")
	app, err := appArgs(fs, args)
	if err == nil && *yes && *dryRun {
		err = errors.New("use --dry-run or --yes, not the two flags together")
	}
	if err != nil || *agent == "" {
		if err != nil {
			fmt.Fprintln(errOut, "mai:", err)
		}
		fmt.Fprintln(errOut, connectUse)
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
	p, err := connect.PrepareWith(home, app, *agent, m)
	if err != nil {
		return fail(errOut, err)
	}
	for _, n := range p.Notes() {
		fmt.Fprintln(out, n)
	}
	if !p.Changed() {
		// The apply only records the entries in the manifest. A dry run writes nothing.
		if !*dryRun {
			if err := p.Apply(m); err != nil {
				return fail(errOut, err)
			}
		}
		fmt.Fprintf(out, "%s has the entries of mai already\n", app)
		return 0
	}
	if bin, err := connect.BinaryPath(); err == nil {
		fmt.Fprintf(out, "The entries name the mai binary %s.\n", bin)
	}
	fmt.Fprint(out, p.Diff())
	if *dryRun {
		return 0
	}
	if !*yes {
		if !isTerminal(in) {
			fmt.Fprintf(errOut, "mai: an AI agent: run mai connect %s --agent %s --dry-run to show the change, "+
				"and run it with --yes only after the user agrees.\n", app, *agent)
		}
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
	disconnect := connect.Disconnect
	if *force {
		disconnect = connect.DisconnectForce
	}
	if err := disconnect(app, m); err != nil {
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
