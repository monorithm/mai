package hook

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Plain is the format that prints the text with no JSON.
const Plain = "plain"

type eventSpec struct {
	canonical string
	mode      string
}

// events maps a format and a lower-case event name to the canonical event and the output
// mode. The shapes are the same as the shapes of the 2.x hook.
var events = map[string]map[string]eventSpec{
	"claude": {
		"sessionstart":     {"SessionStart", "context"},
		"userpromptsubmit": {"UserPromptSubmit", "context"},
		"posttooluse":      {"PostToolUse", "context"},
		"stop":             {"Stop", "context"},
	},
	"codex": {
		"sessionstart":     {"SessionStart", "context"},
		"userpromptsubmit": {"UserPromptSubmit", "context"},
		"stop":             {"Stop", "block"},
	},
	// At stop, followup_message becomes the next user message of Cursor.
	"cursor": {
		"beforesubmitprompt": {"UserPromptSubmit", "additional_context"},
		"posttooluse":        {"PostToolUse", "additional_context"},
		"stop":               {"Stop", "followup_message"},
	},
	// GitHub Copilot in VS Code. The Local harness sends hook_event_name in the payload. The
	// Copilot harness does not. The output shape follows the harness.
	"copilot": {
		"sessionstart":     {"SessionStart", "copilot"},
		"userpromptsubmit": {"UserPromptSubmit", "copilot"},
		"posttooluse":      {"PostToolUse", "copilot"},
		"stop":             {"Stop", "copilot"},
		"agentstop":        {"Stop", "copilot"},
	},
	// PreInvocation runs before each model call. injectSteps adds a system message.
	"antigravity": {"preinvocation": {"UserPromptSubmit", "antigravity"}},
}

// Formats are the names of the known formats.
func Formats() []string {
	return []string{Plain, "claude", "codex", "cursor", "copilot", "antigravity"}
}

// Text is the notice text for a session with new mail.
func Text(n Notice) string {
	word := "messages"
	if n.NewMail == 1 {
		word = "message"
	}
	return "mai: session " + n.Session + " has " + strconv.Itoa(n.NewMail) + " new mail " + word +
		". Use the mai tools to read the mail."
}

// Payload is the part of the payload of the app that changes the output. The hook uses
// only these fields: the harness of Copilot, and the marks that stop a loop of Stop hooks.
type Payload struct {
	hasEventName bool
	stopActive   bool
	loopCount    int
}

// ParsePayload reads the payload of the app. A bad or empty payload gives the zero value.
func ParsePayload(raw []byte) Payload {
	var m map[string]json.RawMessage
	text := strings.TrimPrefix(string(raw), "\uFEFF")
	if json.Unmarshal([]byte(text), &m) != nil {
		return Payload{}
	}
	var p Payload
	_, p.hasEventName = m["hook_event_name"]
	for _, k := range []string{"stop_hook_active", "stopHookActive"} {
		var b bool
		if json.Unmarshal(m[k], &b) == nil && b {
			p.stopActive = true
		}
	}
	_ = json.Unmarshal(m["loop_count"], &p.loopCount)
	return p
}

// Lines gives the text of each session of the notice that has new mail: the pool session
// first, then each named session.
func Lines(n Notice) []string {
	var out []string
	for _, s := range n.All() {
		if s.NewMail > 0 {
			out = append(out, Text(s))
		}
	}
	return out
}

// Render gives the output for the app, or "" when the hook must print nothing: an unknown
// format or event, no new mail, or a Stop hook in a turn that a Stop hook already continued.
// The text has one line for each session with new mail.
func Render(format, event string, p Payload, n Notice) string {
	lines := Lines(n)
	if len(lines) == 0 {
		return ""
	}
	text := strings.Join(lines, "\n")
	if format == Plain {
		return text
	}
	spec, ok := events[format][strings.ToLower(event)]
	if !ok {
		return ""
	}
	if spec.canonical == "Stop" && (p.stopActive || p.loopCount > 0) {
		return ""
	}
	var out any
	switch spec.mode {
	case "context":
		out = map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": spec.canonical, "additionalContext": text}}
	case "block":
		out = map[string]any{"decision": "block", "reason": text}
	case "antigravity":
		out = map[string]any{"injectSteps": []any{map[string]any{"systemMessage": map[string]any{"systemMessage": text}}}}
	case "copilot":
		out = copilot(spec.canonical, p.hasEventName, text)
	default:
		out = map[string]any{spec.mode: text}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return ""
	}
	return string(b)
}

func copilot(event string, local bool, text string) any {
	if event == "Stop" {
		if local {
			return map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": "Stop", "decision": "block", "reason": text}}
		}
		return map[string]any{"decision": "block", "reason": text}
	}
	if local {
		return map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": event, "additionalContext": text}}
	}
	return map[string]any{"additionalContext": text}
}
