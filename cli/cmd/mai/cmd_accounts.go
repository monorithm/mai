package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/monorithm/mai/cli/internal/api"
	"github.com/monorithm/mai/cli/internal/keychain"
	"github.com/monorithm/mai/cli/internal/manifest"
	"github.com/monorithm/mai/cli/internal/paths"
)

// accountsKeychain gives the keychain of the commands of agents, sessions, and the UI. The
// tests replace it with a store in memory.
var accountsKeychain = func() keychain.Store { return keychain.System() }

func init() {
	register("agent", "Add, list, rotate, or remove an agent account", func(args []string) int {
		return runAgent(args, os.Stdout, os.Stderr)
	})
	register("session", "List the sessions and their leases, or end the lease of a session", func(args []string) int {
		return runSession(args, os.Stdout, os.Stderr)
	})
	register("approvals", "Show the approved and refused leases of the last 30 days", func(args []string) int {
		return runApprovals(args, os.Stdout, os.Stderr)
	})
}

// ownerClient opens the client of the owner API and the manifest of the instance.
func ownerClient() (*api.Client, api.Config, *manifest.Manifest, error) {
	c, cfg, err := api.Open(accountsKeychain())
	if err != nil {
		return nil, cfg, nil, err
	}
	d, err := paths.Default()
	if err != nil {
		return nil, cfg, nil, err
	}
	m, err := manifest.Load(filepath.Join(d.State, manifest.FileName), cfg.Instance)
	if err != nil {
		return nil, cfg, nil, err
	}
	return c, cfg, m, nil
}

func fail(errOut io.Writer, err error) int {
	fmt.Fprintln(errOut, "mai:", err)
	return 1
}

func runAgent(args []string, out, errOut io.Writer) int {
	const use = "usage: mai agent add NAME | list | rotate NAME | remove NAME"
	if len(args) == 0 {
		fmt.Fprintln(errOut, use)
		return 2
	}
	sub, rest := args[0], args[1:]
	if (sub == "list" && len(rest) != 0) || (sub != "list" && len(rest) != 1) {
		fmt.Fprintln(errOut, use)
		return 2
	}
	c, cfg, m, err := ownerClient()
	if err != nil {
		return fail(errOut, err)
	}
	ctx := context.Background()
	store := accountsKeychain()
	switch sub {
	case "add":
		name := rest[0]
		a, err := c.AddAgent(ctx, name)
		if err != nil {
			return fail(errOut, err)
		}
		account := keychain.AgentAccount(cfg.Instance, a.Name)
		if err := store.Set(keychain.Service, account, a.AgentKey); err != nil {
			_ = c.RemoveAgent(ctx, a.Name)
			return fail(errOut, fmt.Errorf("keychain: %w; the agent is removed again", err))
		}
		if err := m.Add(manifest.Item{Type: manifest.KeychainKey, Name: account}); err != nil {
			return fail(errOut, err)
		}
		fmt.Fprintln(out, a.Name)
		return 0
	case "rotate":
		name := rest[0]
		key, err := c.RotateAgentKey(ctx, name)
		if err != nil {
			return fail(errOut, err)
		}
		account := keychain.AgentAccount(cfg.Instance, name)
		if err := store.Set(keychain.Service, account, key); err != nil {
			return fail(errOut, fmt.Errorf("keychain: %w; run mai agent rotate %s again", err, name))
		}
		if err := m.Add(manifest.Item{Type: manifest.KeychainKey, Name: account}); err != nil {
			return fail(errOut, err)
		}
		fmt.Fprintln(out, name)
		return 0
	case "list":
		agents, err := c.ListAgents(ctx)
		if err != nil {
			return fail(errOut, err)
		}
		w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tSESSIONS\tLEASES")
		for _, a := range agents {
			fmt.Fprintf(w, "%s\t%d\t%d\n", a.Name, a.Sessions, a.Leases)
		}
		_ = w.Flush()
		return 0
	case "remove":
		name := rest[0]
		if err := c.RemoveAgent(ctx, name); err != nil {
			return fail(errOut, err)
		}
		account := keychain.AgentAccount(cfg.Instance, name)
		if err := store.Delete(keychain.Service, account); err != nil && !errors.Is(err, keychain.ErrNotFound) {
			return fail(errOut, fmt.Errorf("keychain: %w", err))
		}
		if err := m.Remove(manifest.Item{Type: manifest.KeychainKey, Name: account}); err != nil {
			return fail(errOut, err)
		}
		fmt.Fprintln(out, name)
		return 0
	}
	fmt.Fprintln(errOut, use)
	return 2
}

func runSession(args []string, out, errOut io.Writer) int {
	const use = "usage: mai session list | end LOGIN"
	if len(args) == 0 || (args[0] == "list" && len(args) != 1) || (args[0] == "end" && len(args) != 2) {
		fmt.Fprintln(errOut, use)
		return 2
	}
	c, _, _, err := ownerClient()
	if err != nil {
		return fail(errOut, err)
	}
	ctx := context.Background()
	switch args[0] {
	case "list":
		sessions, err := c.ListSessions(ctx)
		if err != nil {
			return fail(errOut, err)
		}
		leases, err := c.ListLeases(ctx)
		if err != nil {
			return fail(errOut, err)
		}
		bySession := map[string]api.Lease{}
		for _, l := range leases {
			bySession[l.Session] = l
		}
		w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "LOGIN\tAGENT\tLEASED\tAPP\tFOLDER\tPID\tLAST SEEN")
		for _, s := range sessions {
			l, ok := bySession[s.Login]
			if !ok {
				fmt.Fprintf(w, "%s\t%s\t%s\t-\t-\t-\t-\n", s.Login, s.Agent, yesNo(s.Leased))
				continue
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\t%s\n", s.Login, s.Agent, yesNo(s.Leased), l.App, l.Folder, l.PID, l.LastSeenAt)
		}
		_ = w.Flush()
		return 0
	case "end":
		if err := c.EndSession(ctx, args[1]); err != nil {
			return fail(errOut, err)
		}
		fmt.Fprintln(out, args[1])
		return 0
	}
	fmt.Fprintln(errOut, use)
	return 2
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// ApprovalsFile is the name of the local list of approvals in the state folder. The bridge
// adds one record for each presence check of a new lease.
const ApprovalsFile = "approvals.json"

// approval is one record of approvals.json.
type approval struct {
	Time   time.Time `json:"time"`
	Agent  string    `json:"agent"`
	App    string    `json:"app"`
	Folder string    `json:"folder"`
	PID    int       `json:"pid"`
	Result string    `json:"result"` // "approved" or "refused"
}

type approvalsDoc struct {
	Approvals []approval `json:"approvals"`
}

func runApprovals(args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("approvals", flag.ContinueOnError)
	fs.SetOutput(errOut)
	days := fs.Int("days", 30, "show the records of this number of days")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return 2
	}
	d, err := paths.Default()
	if err != nil {
		return fail(errOut, err)
	}
	b, err := os.ReadFile(filepath.Join(d.State, ApprovalsFile))
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(out, "no approvals")
		return 0
	}
	if err != nil {
		return fail(errOut, err)
	}
	var doc approvalsDoc
	if err := json.Unmarshal(b, &doc); err != nil {
		return fail(errOut, fmt.Errorf("%s: %w", ApprovalsFile, err))
	}
	since := time.Now().Add(-time.Duration(*days) * 24 * time.Hour)
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "TIME\tRESULT\tAGENT\tAPP\tFOLDER\tPID")
	for _, a := range doc.Approvals {
		if a.Time.Before(since) {
			continue
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\n", a.Time.Local().Format(time.RFC3339), a.Result, a.Agent, a.App, a.Folder, a.PID)
	}
	_ = w.Flush()
	return 0
}
