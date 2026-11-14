package connect

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Apps are the names of the apps that mai connect knows.
var Apps = []string{"antigravity", "claude", "codex", "copilot", "cursor"}

// TOMLPointer is the pointer of the MCP table that mai writes in the config.toml of Codex.
const TOMLPointer = "toml:mcp_servers.mai"

// change is one entry that mai writes. A pointer that ends in "/-" appends value to the array
// at the prefix. A pointer with ifAbsent set writes value only when the key does not exist,
// and mai records it only then.
//
// An MCP entry (mcp set) or a hook (format and event set) replaces an entry of mai that is
// there already, for example after the mai binary moved. Each other existing value stays.
type change struct {
	path     string
	pointer  string
	value    any
	ifAbsent bool
	toml     string // for TOMLPointer: the body of the table
	mcp      bool
	format   string
	event    string
	// drop marks an earlier place of the MCP entry, which the app does not read. mai writes
	// nothing there. It removes an entry of mai from that place. An entry that is not of mai
	// stays.
	drop bool
	// inner is the hook object of a hook group, for an app that puts hooks in groups
	// {"hooks": [...]} (Claude Code, Codex). A group can hold hooks of the user too, thus mai
	// changes only its own hook object in a group that holds more.
	inner *object
}

func obj(kv ...any) *object {
	o := newObject()
	for i := 0; i+1 < len(kv); i += 2 {
		o.set(kv[i].(string), kv[i+1])
	}
	return o
}

func mcpArgs(agent string) []any { return []any{"mcp", "--agent", agent} }

// hookCommand is the shell command of a hook. The apps run a hook through a shell, thus a
// path with a space or a special character gets quotes.
func hookCommand(bin, goos, format, event string) string {
	return shellWord(bin, goos) + " hook --format " + format + " --event " + event
}

// shellWord quotes p for the shell of the OS when p has a character that the shell reads in a
// special way. On Windows, the backslash is not special.
func shellWord(p, goos string) string {
	special := " \t\n'\"$`\\!&;|<>()*?[]{}#~%^"
	if goos == "windows" {
		special = " \t\"&|<>()^%!;,"
	}
	if p != "" && !strings.ContainsAny(p, special) {
		return p
	}
	if goos == "windows" {
		return `"` + p + `"`
	}
	return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
}

// tomlString writes s as a TOML basic string.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// isMaiProgram reports if the program p of a command is a mai binary: its base name is mai,
// mai.exe, or the base name of the running mai binary, with or without quotes. The names are
// not case-sensitive, as on Windows and macOS.
func isMaiProgram(p string) bool {
	base := strings.ToLower(baseName(p))
	if base == "mai" || base == "mai.exe" {
		return true
	}
	if bin, err := Binary(); err == nil {
		return base == strings.ToLower(baseName(bin))
	}
	return false
}

// baseName returns the last part of the path p, with no quotes around p.
func baseName(p string) string {
	p = strings.Trim(strings.TrimSpace(p), `'"`)
	return p[strings.LastIndexAny(p, `/\`)+1:]
}

// isMaiHook reports if cmd is the hook command of mai for format and event, with any path of
// mai. An empty format or event matches each one.
func isMaiHook(cmd, format, event string) bool {
	i := strings.LastIndex(cmd, " hook --format ")
	if i < 0 || !isMaiProgram(cmd[:i]) {
		return false
	}
	f, e, ok := strings.Cut(cmd[i+len(" hook --format "):], " --event ")
	return ok && f != "" && e != "" && !strings.Contains(e, " ") &&
		(format == "" || f == format) && (event == "" || e == event)
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

// changes returns the entries of one app, sorted by file. bin is the path of the mai binary
// that the entries name, and goos is the OS of the shell that runs the hooks.
func changes(home, app, agent, bin, goos string) ([]change, error) {
	hookCommand := func(format, event string) string { return hookCommand(bin, goos, format, event) }
	var out []change
	switch app {
	case "claude":
		out = append(out, change{
			path:    filepath.Join(home, ".claude.json"),
			pointer: "/mcpServers/mai",
			value:   obj("type", "stdio", "command", bin, "args", mcpArgs(agent)),
			mcp:     true,
		})
		settings := filepath.Join(home, ".claude", "settings.json")
		for _, e := range []string{"SessionStart", "UserPromptSubmit", "PostToolUse", "Stop"} {
			inner := obj("type", "command", "command", hookCommand("claude", e), "timeout", 10)
			out = append(out, change{
				path:    settings,
				pointer: "/hooks/" + e + "/-",
				value:   obj("hooks", []any{inner}),
				format:  "claude",
				event:   e,
				inner:   inner,
			})
		}
	case "cursor":
		out = append(out, change{
			path:    filepath.Join(home, ".cursor", "mcp.json"),
			pointer: "/mcpServers/mai",
			value:   obj("command", bin, "args", mcpArgs(agent)),
			mcp:     true,
		})
		hooks := filepath.Join(home, ".cursor", "hooks.json")
		out = append(out, change{path: hooks, pointer: "/version", value: 1, ifAbsent: true})
		for _, e := range []string{"beforeSubmitPrompt", "postToolUse", "stop"} {
			v := obj("command", hookCommand("cursor", e), "timeout", 10)
			if e == "stop" {
				v.set("loop_limit", 1)
			}
			out = append(out, change{path: hooks, pointer: "/hooks/" + e + "/-", value: v, format: "cursor", event: e})
		}
	case "copilot":
		out = append(out, change{
			path:    filepath.Join(vscodeUserDir(home), "mcp.json"),
			pointer: "/servers/mai",
			value:   obj("type", "stdio", "command", bin, "args", mcpArgs(agent)),
			mcp:     true,
		})
		hooks := filepath.Join(home, ".copilot", "hooks", "mai.json")
		out = append(out, change{path: hooks, pointer: "/version", value: 1, ifAbsent: true})
		for _, e := range []string{"sessionStart", "agentStop"} {
			out = append(out, change{
				path:    hooks,
				pointer: "/hooks/" + e + "/-",
				value:   obj("type", "command", "command", hookCommand("copilot", e), "timeoutSec", 10),
				format:  "copilot",
				event:   e,
			})
		}
	case "codex":
		// Codex reads its folder from CODEX_HOME when it is set, else ~/.codex.
		codexHome := os.Getenv("CODEX_HOME")
		if codexHome == "" {
			codexHome = filepath.Join(home, ".codex")
		}
		out = append(out, change{
			path:    filepath.Join(codexHome, "config.toml"),
			pointer: TOMLPointer,
			toml:    fmt.Sprintf("[mcp_servers.mai]\ncommand = %s\nargs = [\"mcp\", \"--agent\", %s]\n", tomlString(bin), tomlString(agent)),
			mcp:     true,
		})
		hooks := filepath.Join(codexHome, "hooks.json")
		for _, e := range []string{"SessionStart", "UserPromptSubmit", "Stop"} {
			inner := obj("type", "command", "command", hookCommand("codex", e), "timeout", 10)
			out = append(out, change{
				path:    hooks,
				pointer: "/hooks/" + e + "/-",
				value:   obj("hooks", []any{inner}),
				format:  "codex",
				event:   e,
				inner:   inner,
			})
		}
	case "antigravity":
		// Antigravity reads its MCP servers only from the file of "agy mcp add",
		// ~/.gemini/config/mcp_config.json. The versions 3.3.0 and earlier of mai wrote the
		// entry in ~/.gemini/antigravity/mcp_config.json. Antigravity does not read that file.
		// Thus mai connect moves the entry: it writes the new entry, and it removes the entry
		// of mai from the earlier file.
		gemini := filepath.Join(home, ".gemini")
		out = append(out, change{
			path:    filepath.Join(gemini, "config", "mcp_config.json"),
			pointer: "/mcpServers/mai",
			value:   obj("command", bin, "args", mcpArgs(agent)),
			mcp:     true,
		})
		out = append(out, change{
			path:    filepath.Join(gemini, "antigravity", "mcp_config.json"),
			pointer: "/mcpServers/mai",
			mcp:     true,
			drop:    true,
		})
		// The hook of mai is one element of the array PreInvocation of the hook set "mai", thus
		// a hook of the user in the same array stays.
		out = append(out, change{
			path:    filepath.Join(gemini, "config", "hooks.json"),
			pointer: "/mai/PreInvocation/-",
			value:   obj("type", "command", "command", hookCommand("antigravity", "PreInvocation"), "timeout", 10),
			format:  "antigravity",
			event:   "PreInvocation",
		})
	default:
		return nil, fmt.Errorf("connect: unknown app %q; use one of %v", app, Apps)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out, nil
}

// groupApps are the apps that put hooks in groups {"hooks": [...]}.
var groupApps = map[string]bool{"claude": true, "codex": true}

// Known reports if mai connect knows the app.
func Known(app string) bool {
	for _, a := range Apps {
		if a == app {
			return true
		}
	}
	return false
}

// owns reports if the existing JSON value v is an earlier entry of mai for c: an MCP entry
// whose command is a mai binary with the argument "mcp", or a value that holds the hook
// command of mai for the format and the event of c.
func (c change) owns(v any) bool {
	if c.mcp {
		o, ok := v.(*object)
		if !ok {
			return false
		}
		cmd, _ := o.vals["command"].(string)
		args, _ := o.vals["args"].([]any)
		return isMaiProgram(cmd) && len(args) > 0 && args[0] == "mcp"
	}
	if c.format == "" {
		return false
	}
	for _, s := range stringsIn(v) {
		if isMaiHook(s, c.format, c.event) {
			return true
		}
	}
	return false
}

// stringsIn returns each string value in v, at each depth.
func stringsIn(v any) []string {
	switch x := v.(type) {
	case string:
		return []string{x}
	case *object:
		var out []string
		for _, k := range x.keys {
			out = append(out, stringsIn(x.vals[k])...)
		}
		return out
	case []any:
		var out []string
		for _, e := range x {
			out = append(out, stringsIn(e)...)
		}
		return out
	}
	return nil
}
