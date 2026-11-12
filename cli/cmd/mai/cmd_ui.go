package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"

	"github.com/monorithm/mai/cli/v3/internal/api"
	"github.com/monorithm/mai/cli/v3/internal/presence"
)

// uiOpenBrowser opens the URL in the default browser. The tests replace it.
var uiOpenBrowser = func(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	cmd.Stdout, cmd.Stderr = nil, nil
	return cmd.Start()
}

func init() {
	register("ui", "Turn the mail UI on or off, or open it in the browser", func(args []string) int {
		return runUI(args, os.Stdout, os.Stderr)
	})
}

func runUI(args []string, out, errOut io.Writer) int {
	const use = "usage: mai ui [on | off | open]"
	if len(args) > 1 {
		fmt.Fprintln(errOut, use)
		return 2
	}
	sub := "status"
	if len(args) == 1 {
		sub = args[0]
	}
	switch sub {
	case "status", "on", "off", "open":
	default:
		fmt.Fprintln(errOut, use)
		return 2
	}
	c, cfg, _, err := ownerClient()
	if err != nil {
		return fail(errOut, err)
	}
	ctx := context.Background()
	switch sub {
	case "status":
		on, err := c.UI(ctx)
		if err != nil {
			return fail(errOut, err)
		}
		fmt.Fprintln(out, "ui", onOff(on))
	case "on", "off":
		on, err := c.SetUI(ctx, sub == "on")
		if err != nil {
			return fail(errOut, err)
		}
		fmt.Fprintln(out, "ui", onOff(on))
	case "open":
		if err := presence.Check("open the mail UI of mai"); err != nil {
			return fail(errOut, err)
		}
		t, err := c.UITicket(ctx)
		if err != nil {
			if api.Code(err) == "UI_OFF" {
				return fail(errOut, fmt.Errorf("%w; run mai ui on first", err))
			}
			return fail(errOut, err)
		}
		// The ticket is in the fragment, thus the browser does not send it to the server in
		// the URL. The CLI never prints the ticket.
		if err := uiOpenBrowser(fmt.Sprintf("%s/ui/sign-in#t=%s", cfg.BaseURL(), t.Value)); err != nil {
			return fail(errOut, fmt.Errorf("open the browser: %w", err))
		}
		fmt.Fprintf(out, "opened %s/ui/sign-in\n", cfg.BaseURL())
	}
	return 0
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}
