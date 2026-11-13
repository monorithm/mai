package hook

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// two is the notice of a bridge with the pool session and two named sessions. The named
// session cursor.lane-b has no new mail.
var two = Notice{
	Session: "cursor.s2", NewMail: 3, UpdatedAt: "2026-11-12T17:00:00Z",
	Named: []Notice{
		{Session: "cursor.lane-a", NewMail: 1, UpdatedAt: "2026-11-12T17:00:01Z"},
		{Session: "cursor.lane-b", NewMail: 0, UpdatedAt: "2026-11-12T17:00:02Z"},
	},
}

const textLaneA = "mai: session cursor.lane-a has 1 new mail message. Use the mai tools to read the mail."

func TestRenderGivesOneLineForEachSessionWithNewMail(t *testing.T) {
	both := text3 + `\n` + textLaneA
	local := ParsePayload([]byte(`{"hook_event_name":"Stop"}`))
	cases := []struct {
		format, event string
		payload       Payload
		want          string
	}{
		{"plain", "", Payload{}, text3 + "\n" + textLaneA},
		{"claude", "UserPromptSubmit", Payload{}, `{"hookSpecificOutput":{"additionalContext":"` + both + `","hookEventName":"UserPromptSubmit"}}`},
		{"codex", "Stop", Payload{}, `{"decision":"block","reason":"` + both + `"}`},
		{"cursor", "stop", Payload{}, `{"followup_message":"` + both + `"}`},
		{"copilot", "SessionStart", Payload{}, `{"additionalContext":"` + both + `"}`},
		{"copilot", "Stop", local, `{"hookSpecificOutput":{"decision":"block","hookEventName":"Stop","reason":"` + both + `"}}`},
		{"antigravity", "PreInvocation", Payload{}, `{"injectSteps":[{"systemMessage":{"systemMessage":"` + both + `"}}]}`},
	}
	for _, c := range cases {
		if got := Render(c.format, c.event, c.payload, two); got != c.want {
			t.Errorf("%s %s:\n got %s\nwant %s", c.format, c.event, got, c.want)
		}
	}
	onlyNamed := two
	onlyNamed.NewMail = 0
	if got := Render("plain", "", Payload{}, onlyNamed); got != textLaneA {
		t.Errorf("pool with no new mail: got %q", got)
	}
	none := Notice{Session: "cursor.s2", Named: []Notice{{Session: "cursor.lane-a"}}}
	if got := Render("claude", "Stop", Payload{}, none); got != "" {
		t.Errorf("no session with new mail: got %q", got)
	}
}

func TestRunShowsEachSessionOfTheBridge(t *testing.T) {
	state := t.TempDir()
	writeNotice(t, state, 200, "cursor", `{"session":"cursor.s2","new_mail":3,"updated_at":"2026-11-12T17:00:00Z",`+
		`"named":[{"session":"cursor.lane-a","new_mail":1,"updated_at":"2026-11-12T17:00:01Z"},`+
		`{"session":"cursor.lane-b","new_mail":0,"updated_at":"2026-11-12T17:00:02Z"}]}`)
	writeNotice(t, state, 200, "codex", `{"session":"codex.s1","new_mail":5,"updated_at":"2026-11-12T17:00:00Z"}`)
	var out bytes.Buffer
	Run(strings.NewReader(`{}`), &out, "plain", "", "cursor", state, []int{100, 200})
	if out.String() != text3+"\n"+textLaneA+"\n" {
		t.Errorf("plain: got %q", out.String())
	}
	out.Reset()
	Run(strings.NewReader(`{}`), &out, "cursor", "beforeSubmitPrompt", "cursor", state, []int{100, 200})
	var got map[string]string
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("cursor: %q is not JSON: %v", out.String(), err)
	}
	if got["additional_context"] != text3+"\n"+textLaneA {
		t.Errorf("cursor: got %q", got["additional_context"])
	}
	if strings.Count(out.String(), "\n") != 1 {
		t.Errorf("cursor: the output is not one JSON line: %q", out.String())
	}
}

func TestReadNoticeAcceptsALargeNoticeAndRefusesATooLargeFile(t *testing.T) {
	state := t.TempDir()
	n := Notice{Session: "cursor.s1", NewMail: 1}
	for i := 0; i < 200; i++ {
		n.Named = append(n.Named, Notice{Session: "cursor.lane-" + strings.Repeat("x", 20), NewMail: 1, UpdatedAt: "2026-11-12T17:00:00Z"})
	}
	data, _ := json.Marshal(n)
	if len(data) <= 4096 {
		t.Fatalf("the test notice has only %d bytes", len(data))
	}
	writeNotice(t, state, 7, "cursor", string(data))
	got, ok := FindNotice(state, "cursor", []int{7})
	if !ok || len(got.Named) != 200 {
		t.Fatalf("large notice: %v %d", ok, len(got.Named))
	}
	writeNotice(t, state, 8, "cursor", `{"session":"cursor.s1","new_mail":1,"pad":"`+strings.Repeat("x", maxNoticeSize)+`"}`)
	if _, ok := FindNotice(state, "cursor", []int{8}); ok {
		t.Fatal("a file larger than the limit gives a notice")
	}
}

// A bridge whose pool lease has no session writes a notice with no pool entry. The hook
// shows the named sessions of that notice.
func TestANoticeWithNoPoolEntryGivesOnlyTheNamedSessions(t *testing.T) {
	state := t.TempDir()
	writeNotice(t, state, 300, "cursor", `{"named":[{"session":"cursor.lane-a","new_mail":1,"updated_at":"2026-11-12T17:00:01Z"}]}`)
	n, ok := FindNotice(state, "cursor", []int{300})
	if !ok || n.Session != "" || len(n.All()) != 1 || n.All()[0].Session != "cursor.lane-a" {
		t.Fatalf("notice = %+v, %v", n, ok)
	}
	var out bytes.Buffer
	Run(strings.NewReader(`{}`), &out, "plain", "", "cursor", state, []int{300})
	if out.String() != textLaneA+"\n" {
		t.Errorf("plain: got %q", out.String())
	}
	writeNotice(t, state, 301, "cursor", `{"named":[]}`)
	if _, ok := FindNotice(state, "cursor", []int{301}); ok {
		t.Fatal("a notice with no session gives a notice")
	}
}

// The JSON of a notice with no pool session has only the list named.
func TestTheJSONOfANoticeWithNoPoolSessionHasNoPoolEntry(t *testing.T) {
	n := Notice{Named: []Notice{{Session: "cursor.lane-a", NewMail: 0, UpdatedAt: "2026-11-12T17:00:01Z"}}}
	data, err := json.Marshal(n)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"named":[{"session":"cursor.lane-a","new_mail":0,"updated_at":"2026-11-12T17:00:01Z"}]}`
	if string(data) != want {
		t.Fatalf("got %s\nwant %s", data, want)
	}
	data, _ = json.Marshal(two)
	if !strings.HasPrefix(string(data), `{"session":"cursor.s2","new_mail":3,`) {
		t.Fatalf("notice with a pool session = %s", data)
	}
}
