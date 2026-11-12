package hook

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var three = Notice{Session: "cursor.s2", NewMail: 3, UpdatedAt: "2026-11-12T17:00:00Z"}

const text3 = "mai: session cursor.s2 has 3 new mail messages. Use the mai tools to read the mail."

func TestRenderGivesTheShapeOfEachFormat(t *testing.T) {
	local := ParsePayload([]byte(`{"hook_event_name":"Stop"}`))
	cases := []struct {
		format, event string
		payload       Payload
		want          string
	}{
		{"plain", "", Payload{}, text3},
		{"claude", "UserPromptSubmit", Payload{}, `{"hookSpecificOutput":{"additionalContext":"` + text3 + `","hookEventName":"UserPromptSubmit"}}`},
		{"claude", "Stop", Payload{}, `{"hookSpecificOutput":{"additionalContext":"` + text3 + `","hookEventName":"Stop"}}`},
		{"codex", "SessionStart", Payload{}, `{"hookSpecificOutput":{"additionalContext":"` + text3 + `","hookEventName":"SessionStart"}}`},
		{"codex", "Stop", Payload{}, `{"decision":"block","reason":"` + text3 + `"}`},
		{"cursor", "beforeSubmitPrompt", Payload{}, `{"additional_context":"` + text3 + `"}`},
		{"cursor", "stop", Payload{}, `{"followup_message":"` + text3 + `"}`},
		{"copilot", "SessionStart", Payload{}, `{"additionalContext":"` + text3 + `"}`},
		{"copilot", "agentStop", Payload{}, `{"decision":"block","reason":"` + text3 + `"}`},
		{"copilot", "Stop", local, `{"hookSpecificOutput":{"decision":"block","hookEventName":"Stop","reason":"` + text3 + `"}}`},
		{"copilot", "SessionStart", local, `{"hookSpecificOutput":{"additionalContext":"` + text3 + `","hookEventName":"SessionStart"}}`},
		{"antigravity", "PreInvocation", Payload{}, `{"injectSteps":[{"systemMessage":{"systemMessage":"` + text3 + `"}}]}`},
	}
	for _, c := range cases {
		if got := Render(c.format, c.event, c.payload, three); got != c.want {
			t.Errorf("%s %s:\n got %s\nwant %s", c.format, c.event, got, c.want)
		}
	}
}

func TestRenderGivesNothingWhenThereIsNoOutput(t *testing.T) {
	if got := Render("claude", "Stop", Payload{}, Notice{Session: "a.s1"}); got != "" {
		t.Errorf("no new mail: got %q", got)
	}
	if got := Render("claude", "Unknown", Payload{}, three); got != "" {
		t.Errorf("unknown event: got %q", got)
	}
	if got := Render("other", "Stop", Payload{}, three); got != "" {
		t.Errorf("unknown format: got %q", got)
	}
	active := ParsePayload([]byte(`{"stop_hook_active":true}`))
	if got := Render("claude", "Stop", active, three); got != "" {
		t.Errorf("stop hook active: got %q", got)
	}
	loop := ParsePayload([]byte(`{"loop_count":1,"status":"completed"}`))
	if got := Render("cursor", "stop", loop, three); got != "" {
		t.Errorf("cursor loop: got %q", got)
	}
}

func TestTextSaysOneMessage(t *testing.T) {
	got := Text(Notice{Session: "codex.s1", NewMail: 1})
	if !strings.Contains(got, "1 new mail message.") {
		t.Errorf("Text = %q", got)
	}
}

func writeNotice(t *testing.T, state string, pid int, agent, body string) {
	t.Helper()
	p := NoticePath(state, pid, agent)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRunFindsTheNoticeOfAnAncestor(t *testing.T) {
	state := t.TempDir()
	writeNotice(t, state, 200, "cursor", `{"session":"cursor.s2","new_mail":3,"updated_at":"2026-11-12T17:00:00Z"}`)
	writeNotice(t, state, 300, "codex", `{"session":"codex.s1","new_mail":5,"updated_at":"2026-11-12T17:00:00Z"}`)
	var out bytes.Buffer
	Run(strings.NewReader(`{"prompt":"x"}`), &out, "plain", "", "cursor", state, []int{100, 200, 300})
	if out.String() != text3+"\n" {
		t.Errorf("with agent: got %q", out.String())
	}
	out.Reset()
	Run(strings.NewReader(""), &out, "plain", "", "", state, []int{100, 300, 200})
	if !strings.Contains(out.String(), "codex.s1 has 5") {
		t.Errorf("with no agent: got %q", out.String())
	}
}

func TestRunPrintsNothingOnAnError(t *testing.T) {
	state := t.TempDir()
	var out bytes.Buffer
	Run(strings.NewReader("not json"), &out, "claude", "Stop", "cursor", state, []int{1, 2})
	writeNotice(t, state, 2, "cursor", "{bad")
	Run(nil, &out, "claude", "Stop", "cursor", state, []int{1, 2})
	if out.Len() != 0 {
		t.Errorf("got %q, want nothing", out.String())
	}
}
