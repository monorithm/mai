package main

import (
	"strings"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/procchain"
)

// nodeInTheTerminalOfCode is the chain of parent processes of an agent CLI that runs on
// Node.js in the terminal of Visual Studio Code, for example Claude Code from npm. The chain
// gives only the executable, thus the CLI cannot see the agent CLI, and it finds copilot.
func nodeInTheTerminalOfCode() ([]procchain.Proc, error) {
	return []procchain.Proc{
		{PID: 60, PPID: 50, Exe: "/opt/mai/bin/mai"},
		{PID: 50, PPID: 40, Exe: "/bin/zsh"},
		{PID: 40, PPID: 30, Exe: "/opt/homebrew/Cellar/node/24.1.0/bin/node"},
		{PID: 30, PPID: 20, Exe: "/bin/zsh"},
		{PID: 20, PPID: 10, Exe: "/Applications/Visual Studio Code.app/Contents/Frameworks/Code Helper.app/Contents/MacOS/Code Helper"},
		{PID: 10, PPID: 1, Exe: "/Applications/Visual Studio Code.app/Contents/MacOS/Code"},
	}, nil
}

// An app that the CLI found in the chain of parent processes can be incorrect. Thus the
// first step tells the agent to get a confirmation of the app from the user, before a step
// for the app.
func TestGuideGetsAConfirmationOfAnAppFromTheChainOfParentProcesses(t *testing.T) {
	g := newGuideTest(t)
	g.serve(t)
	guideChain = nodeInTheTerminalOfCode
	r := g.report(t)
	if field(r, "app", "name") != "copilot" || field(r, "app", "found_by") != "process" {
		t.Fatalf("app = %v", r["app"])
	}
	ids := stepIDs(r)
	if len(ids) < 2 || ids[0] != "confirm-app" {
		t.Fatalf("steps = %v, want confirm-app first", ids)
	}
	step := r["steps"].([]any)[0].(map[string]any)
	text := step["text"].(string)
	for _, s := range []string{"copilot", "Visual Studio Code with Copilot", "chain of parent processes", "confirmation",
		"`mai guide --app APP`"} {
		if !strings.Contains(text, s) {
			t.Errorf("the step confirm-app does not tell %q: %q", s, text)
		}
	}
	if step["by"] != "agent" || step["ask_user"] != true || step["command"] != "mai guide --app APP" || step["changes"] != "Nothing." {
		t.Fatalf("step = %v", step)
	}
	_, out, _ := g.run(t)
	blocks := guideStepBlocks(t, out)
	if !strings.Contains(blocks[0], "Get the approval of the user first.") {
		t.Errorf("the text of the step confirm-app has no approval line:\n%s", blocks[0])
	}
	if state := guideStatePart(t, out); strings.Contains(state, "run `") {
		t.Errorf("the part State has an order:\n%s", state)
	}
	// The agent flag stays in the command for a different app.
	r = g.report(t, "--agent", "lane")
	if cmd := r["steps"].([]any)[0].(map[string]any)["command"]; cmd != "mai guide --app APP --agent lane" {
		t.Errorf("command with --agent = %v", cmd)
	}
}

// A connected app from the chain of parent processes also gets the confirmation, and an app
// from the flag --app gets none.
func TestGuideGetsNoConfirmationOfAnAppFromTheFlag(t *testing.T) {
	g := newGuideTest(t)
	g.serve(t)
	g.addAgent(t)
	guideChain = chainOf("/Applications/Cursor.app/Contents/MacOS/Cursor")
	wantSteps(t, g.report(t), "confirm-app", "show-change", "connect", "restart-app", "check")
	wantSteps(t, g.report(t, "--app", "cursor"), "show-change", "connect", "restart-app", "check")
}
