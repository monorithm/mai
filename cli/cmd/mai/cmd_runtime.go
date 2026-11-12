package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/monorithm/mai/cli/v3/internal/bridge"
	"github.com/monorithm/mai/cli/v3/internal/hook"
	"github.com/monorithm/mai/cli/v3/internal/keychain"
	"github.com/monorithm/mai/cli/v3/internal/paths"
	"github.com/monorithm/mai/cli/v3/internal/presence"
	"github.com/monorithm/mai/cli/v3/internal/procchain"
)

func init() {
	register("mcp", "bridge the MCP stdio of an app to the server (an app starts it)", runMCP)
	register("hook", "print the notice of new mail in the hook format of an app", runHook)
}

func runMCP(args []string) int {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	agent := fs.String("agent", "", "the name of the agent")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *agent == "" {
		fmt.Fprintln(os.Stderr, "mai mcp: give the agent with --agent NAME")
		return 2
	}
	dirs, err := paths.Default()
	if err != nil {
		fmt.Fprintf(os.Stderr, "mai mcp: no home folder: %v\n", err)
		return 1
	}
	cfg, err := bridge.LoadConfig(dirs.Config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mai mcp: %v\n", err)
		return 1
	}
	folder, _ := os.Getwd()
	app := ""
	if chain, err := procchain.Self(); err == nil {
		if top, ok := procchain.Top(chain); ok {
			app = top.Name()
		}
	}
	b := &bridge.Bridge{
		Agent:    *agent,
		Instance: cfg.Instance,
		Endpoint: cfg.Endpoint(),
		Store:    keychain.System(),
		Presence: presence.Check,
		StateDir: dirs.State,
		App:      app,
		AppPID:   os.Getppid(),
		PID:      os.Getpid(),
		Folder:   folder,
		Stdin:    os.Stdin,
		Stdout:   os.Stdout,
		Stderr:   os.Stderr,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return b.Run(ctx)
}

func runHook(args []string) int {
	// A hook never fails the app that runs it: each error gives no output and exit 0.
	defer func() { _ = recover() }()
	fs := flag.NewFlagSet("hook", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", hook.Plain, "the hook format of the app")
	event := fs.String("event", "", "the hook event name of the app")
	agent := fs.String("agent", "", "the name of the agent")
	if fs.Parse(args) != nil {
		_, _ = io.Copy(io.Discard, os.Stdin)
		return 0
	}
	dirs, err := paths.Default()
	if err != nil {
		_, _ = io.Copy(io.Discard, os.Stdin)
		return 0
	}
	var pids []int
	if chain, err := procchain.Self(); err == nil {
		for _, p := range chain[1:] {
			pids = append(pids, p.PID)
		}
	} else {
		pids = []int{os.Getppid()}
	}
	hook.Run(os.Stdin, os.Stdout, *format, *event, *agent, dirs.State, pids)
	return 0
}
