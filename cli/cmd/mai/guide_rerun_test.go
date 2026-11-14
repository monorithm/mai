package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/procchain"
)

// chainOf makes the chain of parent processes give the app with the executable exe.
func chainOf(exe string) func() ([]procchain.Proc, error) {
	return func() ([]procchain.Proc, error) {
		return []procchain.Proc{{PID: 30, PPID: 20, Exe: "/opt/mai/bin/mai"}, {PID: 20, PPID: 10, Exe: "/bin/zsh"},
			{PID: 10, PPID: 1, Exe: exe}}, nil
	}
}

// The command to run the guide again keeps the app and the agent of the run. A rerun of
// "mai guide" with no flags can give the steps of a different app or agent.
func TestGuideGivesTheCommandToRunItAgainWithTheFlagsOfTheRun(t *testing.T) {
	g := newGuideTest(t)
	g.serve(t)
	g.addAgent(t)
	guideChain = chainOf("/Applications/Cursor.app/Contents/MacOS/Cursor")
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"--agent", "cursor-b"}, "mai guide --app cursor --agent cursor-b"},
		{[]string{"--app", "codex", "--agent", "lane"}, "mai guide --app codex --agent lane"},
		{[]string{"--app", "cursor"}, "mai guide --app cursor"},
		{nil, "mai guide --app cursor"},
	}
	for _, c := range cases {
		r := g.report(t, c.args...)
		if r["rerun"] != c.want {
			t.Errorf("mai guide %v: rerun = %v, want %q", c.args, r["rerun"], c.want)
		}
		_, out, _ := g.run(t, c.args...)
		if !strings.Contains(out, "Run `"+c.want+"` again after each step.") {
			t.Errorf("mai guide %v: the text does not tell %q:\n%s", c.args, c.want, out)
		}
		if strings.Contains(out, "Run `mai guide` again") {
			t.Errorf("mai guide %v: the text tells a rerun with no flags:\n%s", c.args, out)
		}
	}
	guideChain = chainOf("/bin/zsh")
	if r := g.report(t, "--agent", "lane"); r["rerun"] != "mai guide --app APP --agent lane" {
		t.Errorf("no app: rerun = %v", r["rerun"])
	}
	// The step of a state that the user corrects also gives the full command.
	if err := os.WriteFile(filepath.Join(g.home, "maihome", "config", "config.json"), []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := g.report(t, "--app", "codex", "--agent", "lane")
	if text := r["steps"].([]any)[0].(map[string]any)["text"].(string); !strings.Contains(text, "`mai guide --app codex --agent lane`") {
		t.Errorf("config-error text = %q", text)
	}
}

// When the agent connects a different app, the check with whoami occurs in that app. The
// user does it there, because whoami in the app of the agent gives the session of a
// different agent.
func TestGuideForADifferentAppGivesTheCheckToTheUser(t *testing.T) {
	g := newGuideTest(t)
	g.serve(t)
	guideChain = chainOf("/Users/a/.local/bin/claude")
	r := g.report(t, "--app", "codex")
	steps := r["steps"].([]any)
	check := steps[len(steps)-1].(map[string]any)
	text := check["text"].(string)
	if check["id"] != "check" || check["by"] != "user" || !strings.Contains(text, "Codex") ||
		!strings.Contains(text, "Claude Code") || !strings.Contains(text, "`whoami`") || !strings.Contains(text, "codex.s1") {
		t.Fatalf("check step = %v", check)
	}
	r = g.report(t, "--app", "claude")
	steps = r["steps"].([]any)
	if check := steps[len(steps)-1].(map[string]any); check["by"] != "agent" {
		t.Fatalf("check step for the app of the agent = %v", check)
	}
	// With no app in the chain, the guide cannot know the app of the agent.
	guideChain = chainOf("/bin/zsh")
	r = g.report(t, "--app", "codex")
	steps = r["steps"].([]any)
	if text := steps[len(steps)-1].(map[string]any)["text"].(string); !strings.Contains(text, "If you are not in Codex") {
		t.Fatalf("check text with no app in the chain = %q", text)
	}
}
