package connect

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
)

// Apps are the names of the apps that mai connect knows.
var Apps = []string{"antigravity", "claude", "codex", "copilot", "cursor"}

// TOMLPointer is the pointer of the MCP table that mai writes in the config.toml of Codex.
const TOMLPointer = "toml:mcp_servers.mai"

// change is one entry that mai writes. A pointer that ends in "/-" appends value to the array
// at the prefix. A pointer with ifAbsent set writes value only when the key does not exist,
// and mai records it only then.
type change struct {
	path     string
	pointer  string
	value    any
	ifAbsent bool
	toml     string // for TOMLPointer: the body of the table
}

func obj(kv ...any) *object {
	o := newObject()
	for i := 0; i+1 < len(kv); i += 2 {
		o.set(kv[i].(string), kv[i+1])
	}
	return o
}

func mcpArgs(agent string) []any { return []any{"mcp", "--agent", agent} }

func hookCommand(format, event string) string {
	return "mai hook --format " + format + " --event " + event
}

// vscodeUserDir is the user folder of Visual Studio Code.
func vscodeUserDir(home string) string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Code", "User")
	case "windows":
		app := os.Getenv("AppData")
		if app == "" {
			app = filepath.Join(home, "AppData", "Roaming")
		}
		return filepath.Join(app, "Code", "User")
	default:
		base := os.Getenv("XDG_CONFIG_HOME")
		if base == "" {
			base = filepath.Join(home, ".config")
		}
		return filepath.Join(base, "Code", "User")
	}
}

// changes returns the entries of one app, sorted by file.
func changes(home, app, agent string) ([]change, error) {
	var out []change
	switch app {
	case "claude":
		out = append(out, change{
			path:    filepath.Join(home, ".claude.json"),
			pointer: "/mcpServers/mai",
			value:   obj("type", "stdio", "command", "mai", "args", mcpArgs(agent)),
		})
		settings := filepath.Join(home, ".claude", "settings.json")
		for _, e := range []string{"SessionStart", "UserPromptSubmit", "PostToolUse", "Stop"} {
			out = append(out, change{
				path:    settings,
				pointer: "/hooks/" + e + "/-",
				value:   obj("hooks", []any{obj("type", "command", "command", hookCommand("claude", e), "timeout", 10)}),
			})
		}
	case "cursor":
		out = append(out, change{
			path:    filepath.Join(home, ".cursor", "mcp.json"),
			pointer: "/mcpServers/mai",
			value:   obj("command", "mai", "args", mcpArgs(agent)),
		})
		hooks := filepath.Join(home, ".cursor", "hooks.json")
		out = append(out, change{path: hooks, pointer: "/version", value: 1, ifAbsent: true})
		for _, e := range []string{"beforeSubmitPrompt", "postToolUse", "stop"} {
			v := obj("command", hookCommand("cursor", e), "timeout", 10)
			if e == "stop" {
				v.set("loop_limit", 1)
			}
			out = append(out, change{path: hooks, pointer: "/hooks/" + e + "/-", value: v})
		}
	case "copilot":
		out = append(out, change{
			path:    filepath.Join(vscodeUserDir(home), "mcp.json"),
			pointer: "/servers/mai",
			value:   obj("type", "stdio", "command", "mai", "args", mcpArgs(agent)),
		})
		hooks := filepath.Join(home, ".copilot", "hooks", "mai.json")
		out = append(out, change{path: hooks, pointer: "/version", value: 1, ifAbsent: true})
		for _, e := range []string{"sessionStart", "agentStop"} {
			out = append(out, change{
				path:    hooks,
				pointer: "/hooks/" + e + "/-",
				value:   obj("type", "command", "command", hookCommand("copilot", e), "timeoutSec", 10),
			})
		}
	case "codex":
		out = append(out, change{
			path:    filepath.Join(home, ".codex", "config.toml"),
			pointer: TOMLPointer,
			toml:    fmt.Sprintf("[mcp_servers.mai]\ncommand = \"mai\"\nargs = [\"mcp\", \"--agent\", %q]\n", agent),
		})
		hooks := filepath.Join(home, ".codex", "hooks.json")
		for _, e := range []string{"SessionStart", "UserPromptSubmit", "Stop"} {
			out = append(out, change{
				path:    hooks,
				pointer: "/hooks/" + e + "/-",
				value:   obj("hooks", []any{obj("type", "command", "command", hookCommand("codex", e), "timeout", 10)}),
			})
		}
	case "antigravity":
		out = append(out, change{
			path:    filepath.Join(home, ".gemini", "antigravity", "mcp_config.json"),
			pointer: "/mcpServers/mai",
			value:   obj("command", "mai", "args", mcpArgs(agent)),
		})
		out = append(out, change{
			path:    filepath.Join(home, ".gemini", "config", "hooks.json"),
			pointer: "/mai",
			value: obj("PreInvocation", []any{
				obj("type", "command", "command", hookCommand("antigravity", "PreInvocation"), "timeout", 10),
			}),
		})
	default:
		return nil, fmt.Errorf("connect: unknown app %q; use one of %v", app, Apps)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out, nil
}

// Known reports if mai connect knows the app.
func Known(app string) bool {
	for _, a := range Apps {
		if a == app {
			return true
		}
	}
	return false
}
