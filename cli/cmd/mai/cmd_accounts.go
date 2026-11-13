package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/monorithm/mai/cli/v3/internal/api"
	"github.com/monorithm/mai/cli/v3/internal/keychain"
	"github.com/monorithm/mai/cli/v3/internal/keys"
	"github.com/monorithm/mai/cli/v3/internal/manifest"
	"github.com/monorithm/mai/cli/v3/internal/paths"
)

// openKeys opens the key file of the CLI, with the legacy keychain. The tests replace it to
// use a legacy keychain in memory.
var openKeys = keys.Default

func init() {
	register("agent", "Add, list, rotate, or remove an agent account", func(args []string) int {
		return runAgent(args, os.Stdout, os.Stderr)
	})
	register("session", "Make, list, rotate, revoke, or end the named sessions of an agent", func(args []string) int {
		return runSession(args, os.Stdin, os.Stdout, os.Stderr)
	})
}

// ownerClient opens the client of the owner API and the manifest of the instance.
func ownerClient() (*api.Client, api.Config, *manifest.Manifest, error) {
	kf, err := openKeys()
	if err != nil {
		return nil, api.Config{}, nil, err
	}
	c, cfg, err := api.Open(kf)
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
	const use = "usage: mai agent add NAME... | list | rotate NAME | remove NAME..."
	if len(args) == 0 {
		fmt.Fprintln(errOut, use)
		return 2
	}
	sub, rest := args[0], args[1:]
	if (sub == "list" && len(rest) != 0) || (sub == "rotate" && len(rest) != 1) || (sub != "list" && len(rest) == 0) {
		fmt.Fprintln(errOut, use)
		return 2
	}
	c, cfg, m, err := ownerClient()
	if err != nil {
		return fail(errOut, err)
	}
	ctx := context.Background()
	kf, err := openKeys()
	if err != nil {
		return fail(errOut, err)
	}
	switch sub {
	case "add":
		for _, name := range rest {
			a, err := c.AddAgent(ctx, name)
			if err != nil {
				return fail(errOut, err)
			}
			account := keys.AgentAccount(cfg.Instance, a.Name)
			if err := kf.Set(account, a.AgentKey); err != nil {
				_ = c.RemoveAgent(ctx, a.Name)
				return fail(errOut, fmt.Errorf("keep the agent key: %w; the agent %s is removed again", err, a.Name))
			}
			// The agent is new on the server, and the server accepts only its new key. Thus a
			// legacy keychain item of the agent, for example of a server whose data is gone,
			// holds a key that is not valid, and it goes.
			if err := removeLegacyItem(kf, m, account, errOut); err != nil {
				return fail(errOut, err)
			}
			fmt.Fprintln(out, a.Name)
		}
		return 0
	case "rotate":
		name := rest[0]
		key, err := c.RotateAgentKey(ctx, name)
		if err != nil {
			return fail(errOut, err)
		}
		account := keys.AgentAccount(cfg.Instance, name)
		if err := kf.Set(account, key); err != nil {
			return fail(errOut, fmt.Errorf("keep the agent key: %w; run mai agent rotate %s again", err, name))
		}
		// The server now accepts only the new key. Thus a legacy keychain item of the agent
		// holds a key that is not valid, and it goes.
		if err := removeLegacyItem(kf, m, account, errOut); err != nil {
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
		for _, name := range rest {
			if err := c.RemoveAgent(ctx, name); err != nil {
				return fail(errOut, err)
			}
			account := keys.AgentAccount(cfg.Instance, name)
			if err := kf.Delete(account); err != nil && !errors.Is(err, keys.ErrNotFound) {
				return fail(errOut, fmt.Errorf("remove the agent key: %w", err))
			}
			// A legacy keychain item of the agent also goes.
			if err := removeLegacyItem(kf, m, account, errOut); err != nil {
				return fail(errOut, err)
			}
			fmt.Fprintln(out, name)
		}
		return 0
	}
	fmt.Fprintln(errOut, use)
	return 2
}

// removeLegacyItem removes the legacy keychain item of the account, and then its record in
// the manifest. When the item stays, the record also stays, thus "mai uninstall" can remove
// the item.
func removeLegacyItem(kf *keys.File, m *manifest.Manifest, account string, errOut io.Writer) error {
	switch err := kf.DeleteLegacy(account); {
	case errors.Is(err, keychain.ErrUnavailable):
	case err != nil:
		fmt.Fprintf(errOut, "mai: the keychain item %s stays: %v\n", account, err)
	default:
		return m.Remove(manifest.Item{Type: manifest.KeychainKey, Name: account})
	}
	return nil
}

// sessionUse is the usage text of mai session.
const sessionUse = `usage: mai session new AGENT --label LABEL
       mai session new AGENT --count N
       mai session list [--agent AGENT] [--json]
       mai session rotate LOGIN
       mai session revoke LOGIN [--yes]
       mai session end LOGIN`

// labelRule is the rule of a session label on the server: AGENT.LABEL is the login.
var labelRule = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// poolLabelRule is the rule of a label of the pool, for example s1. Only the pool and
// "mai session new AGENT --count N" make a session with such a label. The server refuses
// it with BAD_LABEL.
var poolLabelRule = regexp.MustCompile(`^s[0-9]+$`)

// parseAll parses the flags of fs before, between, and after the other arguments, and
// returns the other arguments in sequence.
func parseAll(fs *flag.FlagSet, args []string) ([]string, error) {
	var rest []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return rest, nil
		}
		rest = append(rest, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// sessionLogin gives the login of a session argument. As in 2.x, the command accepts the
// address of the session, for example cursor.lane-a@mai.internal.
func sessionLogin(arg string) string {
	login, _, _ := strings.Cut(arg, "@")
	return login
}

func runSession(args []string, in io.Reader, out, errOut io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(errOut, sessionUse)
		return 2
	}
	sub, rest := args[0], args[1:]
	fs := flag.NewFlagSet("session "+sub, flag.ContinueOnError)
	fs.SetOutput(errOut)
	var (
		label, agent string
		count        int
		asJSON, yes  bool
	)
	switch sub {
	case "new":
		fs.StringVar(&label, "label", "", "make the named session AGENT.`LABEL` (a label s<N> is only for the pool)")
		fs.IntVar(&count, "count", 0, "make the sessions AGENT.s1 to AGENT.s`N` that do not exist")
	case "list":
		fs.StringVar(&agent, "agent", "", "show only the sessions of the agent `NAME`")
		fs.BoolVar(&asJSON, "json", false, "show the sessions as JSON")
	case "revoke":
		fs.BoolVar(&yes, "yes", false, "agree with no typed word, for a script")
	case "rotate", "end":
	default:
		fmt.Fprintln(errOut, sessionUse)
		return 2
	}
	pos, err := parseAll(fs, rest)
	if err != nil {
		fmt.Fprintln(errOut, sessionUse)
		return 2
	}
	want := 1
	if sub == "list" {
		want = 0
	}
	if len(pos) != want {
		fmt.Fprintln(errOut, sessionUse)
		return 2
	}
	if sub == "new" {
		if (label == "") == (count == 0) {
			fmt.Fprintln(errOut, "mai session new: give --label LABEL or --count N")
			fmt.Fprintln(errOut, sessionUse)
			return 2
		}
		if label != "" && !labelRule.MatchString(label) {
			fmt.Fprintf(errOut, "mai session new: the label %q does not agree with the rule [a-z0-9][a-z0-9-]{0,31}\n", label)
			return 2
		}
		if poolLabelRule.MatchString(label) {
			fmt.Fprintf(errOut, "mai session new: BAD_LABEL: the label %q is a label of the pool. "+
				"To make pool sessions, use mai session new AGENT --count N.\n", label)
			return 2
		}
		if label == "" && count < 1 {
			fmt.Fprintln(errOut, "mai session new: --count must be 1 or more")
			return 2
		}
	}
	c, _, _, err := ownerClient()
	if err != nil {
		return fail(errOut, err)
	}
	ctx := context.Background()
	switch sub {
	case "new":
		return sessionNew(ctx, c, pos[0], label, count, out, errOut)
	case "list":
		return sessionList(ctx, c, agent, asJSON, out, errOut)
	case "rotate":
		login := sessionLogin(pos[0])
		if _, err := c.RotateSession(ctx, login); err != nil {
			return fail(errOut, err)
		}
		fmt.Fprintln(out, login)
		return 0
	case "revoke":
		login := sessionLogin(pos[0])
		if !yes {
			fmt.Fprintf(out, "The revoke of the session %s is permanent. Its lease ends, and no chat can log in to it again.\n", login)
		}
		if !confirmWord(in, out, "revoke", yes) {
			fmt.Fprintln(errOut, "mai session revoke: stopped. The session did not change.")
			return 1
		}
		if _, err := c.RevokeSession(ctx, login); err != nil {
			return fail(errOut, err)
		}
		fmt.Fprintln(out, login)
		return 0
	case "end":
		login := sessionLogin(pos[0])
		if err := c.EndSession(ctx, login); err != nil {
			return fail(errOut, err)
		}
		fmt.Fprintln(out, login)
		return 0
	}
	fmt.Fprintln(errOut, sessionUse)
	return 2
}

// sessionNew makes one named session, or the pool sessions AGENT.s1 to AGENT.sN, and prints
// only the login of each new session.
func sessionNew(ctx context.Context, c *api.Client, agent, label string, count int, out, errOut io.Writer) int {
	if label != "" {
		s, err := c.NewSession(ctx, agent, label)
		if err != nil {
			return fail(errOut, sessionHint(err, agent))
		}
		fmt.Fprintln(out, s.Login)
		return 0
	}
	made, err := c.NewSessions(ctx, agent, count)
	if err != nil {
		return fail(errOut, sessionHint(err, agent))
	}
	if len(made) == 0 {
		fmt.Fprintf(errOut, "mai session new: the sessions %s.s1 to %s.s%d exist. No session is new.\n", agent, agent, count)
		return 0
	}
	for _, s := range made {
		fmt.Fprintln(out, s.Login)
	}
	return 0
}

// sessionHint adds the next step to an error of the server.
func sessionHint(err error, agent string) error {
	if api.Code(err) == "AGENT_UNKNOWN" {
		return fmt.Errorf("%w; run mai agent add %s first", err, agent)
	}
	return err
}

func sessionList(ctx context.Context, c *api.Client, agent string, asJSON bool, out, errOut io.Writer) int {
	all, err := c.ListSessions(ctx)
	if err != nil {
		return fail(errOut, err)
	}
	sessions := make([]api.Session, 0, len(all))
	for _, s := range all {
		if agent == "" || s.Agent == agent {
			sessions = append(sessions, s)
		}
	}
	if asJSON {
		b, err := json.MarshalIndent(sessions, "", "  ")
		if err != nil {
			return fail(errOut, err)
		}
		fmt.Fprintln(out, string(b))
		return 0
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "LOGIN\tAGENT\tLABEL\tSTATE\tLEASED\tAPP\tFOLDER\tPID\tLAST SEEN")
	for _, s := range sessions {
		app, folder, pid, seen := "-", "-", "-", "-"
		if s.Leased {
			app, folder, pid, seen = dash(s.App), dash(s.Folder), strconv.Itoa(s.PID), dash(s.LastSeenAt)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			s.Login, s.Agent, dash(s.Label), dash(s.Status), yesNo(s.Leased), app, folder, pid, seen)
	}
	_ = w.Flush()
	return 0
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// confirmWord wants the typed word, unless yes is true.
func confirmWord(in io.Reader, out io.Writer, word string, yes bool) bool {
	if yes {
		return true
	}
	if in == nil {
		return false
	}
	fmt.Fprintf(out, "Type %s to agree: ", word)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false
	}
	return strings.TrimSpace(line) == word
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
