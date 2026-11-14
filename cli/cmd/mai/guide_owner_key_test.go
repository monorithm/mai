package main

import (
	"strings"
	"testing"

	"github.com/monorithm/mai/cli/v3/internal/keys"
)

// wantOwnerKeyStep makes sure that the guide gives only the step owner-key of the user, and
// no start and no agent add. For a server that runs, mai start keeps a new owner key that the
// server refuses, and mai agent add then fails with BAD_KEY.
func wantOwnerKeyStep(t *testing.T, r map[string]any, cause string) {
	t.Helper()
	wantSteps(t, r, "owner-key")
	wantNoStepThatFails(t, r, "mai start", "mai agent add", "mai connect")
	step := r["steps"].([]any)[0].(map[string]any)
	text := step["text"].(string)
	if step["by"] != "user" || step["ask_user"] != true || step["command"] != "mai exec -- mai owner set-key --replace" ||
		!strings.Contains(text, cause) || !strings.Contains(text, "Tell this to the user, and stop.") ||
		!strings.Contains(text, "only when the user tells you to do it") {
		t.Fatalf("step = %v", step)
	}
	if !strings.Contains(step["changes"].(string), "replaces its owner key") {
		t.Fatalf("the step does not tell that the command replaces the owner key of the server: %v", step)
	}
	if field(r, "agent", "exists") != nil || field(r, "agent", "reason") == nil {
		t.Fatalf("agent = %v, want exists null with a reason: the CLI did not get the list of the agents", r["agent"])
	}
}

func TestGuideWithARunningServerAndNoOwnerKeyGivesNoStartAndNoAgentAdd(t *testing.T) {
	for _, agentKey := range []bool{false, true} {
		g := newGuideTest(t)
		g.serve(t)
		g.owner.agents = []string{"cursor"}
		if err := g.keys.file.Delete(keys.OwnerAccount("mai")); err != nil {
			t.Fatal(err)
		}
		if agentKey {
			if err := g.keys.file.Set(keys.AgentAccount("mai", "cursor"), testCursorKey); err != nil {
				t.Fatal(err)
			}
		}
		r := g.report(t, "--app", "cursor")
		if field(r, "owner_key", "exists") != false || field(r, "owner_key", "accepted") != nil {
			t.Fatalf("owner key = %v", r["owner_key"])
		}
		wantOwnerKeyStep(t, r, "has no owner key")
		_, out, _ := g.run(t, "--app", "cursor")
		if strings.Contains(guideStatePart(t, out), "does not exist") {
			t.Errorf("the text tells that the agent account does not exist, but the CLI did not get the list of the agents:\n%s", out)
		}
	}
}

func TestGuideWithAnOwnerKeyThatTheServerRefusesGivesNoAgentAdd(t *testing.T) {
	g := newGuideTest(t)
	g.serve(t)
	g.owner.agents = []string{"cursor"}
	if err := g.keys.file.Set(keys.OwnerAccount("mai"), "mai1:owner:refusedsecret"); err != nil {
		t.Fatal(err)
	}
	r := g.report(t, "--app", "cursor")
	if field(r, "owner_key", "exists") != true || field(r, "owner_key", "accepted") != false {
		t.Fatalf("owner key = %v", r["owner_key"])
	}
	wantOwnerKeyStep(t, r, "refuses the owner key")
	_, out, _ := g.run(t, "--app", "cursor")
	if strings.Contains(out, "refusedsecret") {
		t.Fatalf("mai guide shows the owner key:\n%s", out)
	}
	if !strings.Contains(guideStatePart(t, out), "refuses") {
		t.Errorf("the part State does not tell that the server refuses the owner key:\n%s", out)
	}
}

// The guide tells that an agent account exists, or does not exist, only from the list of the
// owner API. With no list, the field exists is null, and a different field tells the cause.
func TestGuideGivesNoAgentStateThatItDidNotGetFromTheServer(t *testing.T) {
	g := newGuideTest(t)
	g.engOff = true
	r := g.report(t, "--app", "cursor")
	if field(r, "agent", "exists") != nil || field(r, "agent", "key") != false || field(r, "agent", "reason") == nil {
		t.Fatalf("engine off: agent = %v", r["agent"])
	}
	if field(r, "owner_key", "accepted") != nil {
		t.Fatalf("engine off: owner key = %v", r["owner_key"])
	}
	g.engOff = false
	g.serve(t)
	r = g.report(t, "--app", "cursor")
	if field(r, "agent", "exists") != false || field(r, "agent", "on_server") != false || field(r, "owner_key", "accepted") != true {
		t.Fatalf("server runs: agent = %v, owner key = %v", r["agent"], r["owner_key"])
	}
}
