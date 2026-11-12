package connect

import (
	"bytes"
	"regexp"
	"strings"
)

// The table of mai in the config.toml of Codex is a set of whole lines: the comment line
// tomlBegin, the header [mcp_servers.mai], and its key lines. A sub-table of mai, for example
// [mcp_servers.mai.env], is part of the entry of mai, and so is each end comment tomlEnd that
// mai 3.0.0 wrote after the table, also when toml_edit moved it to the end of the file. Codex
// edits the file with toml_edit, which keeps the comment above a table with the table and puts
// new tables after the last table. Thus a blank line or a comment after the last key line of
// the table belongs to the next table, not to mai.
//
// The functions work on whole lines, so that a remove cannot join two lines of the user.

// tomlLine is one line of a TOML text. end includes the line break.
type tomlLine struct {
	start, end int
	text       string // the line with no line break, no trailing white space, and no BOM
	code       bool   // false for a line in a multi-line string
}

func tomlLines(body []byte) []tomlLine {
	var out []tomlLine
	state := "" // "" outside a multi-line string, or its delimiter
	for start := 0; start < len(body); {
		end := bytes.IndexByte(body[start:], '\n')
		if end < 0 {
			end = len(body)
		} else {
			end += start + 1
		}
		text := strings.TrimRight(string(body[start:end]), " \t\r\n")
		if start == 0 {
			text = strings.TrimPrefix(text, "\xef\xbb\xbf")
		}
		l := tomlLine{start: start, end: end, text: text, code: state == ""}
		state = multilineState(text, state)
		out = append(out, l)
		start = end
	}
	return out
}

// multilineState returns the state after the line: "" outside a multi-line string, or the
// delimiter (""" or ”') of the multi-line string that is open at the end of the line.
func multilineState(line, state string) string {
	for i := 0; i < len(line); {
		if state != "" {
			j := strings.Index(line[i:], state)
			if j < 0 {
				return state
			}
			i += j + 3
			state = ""
			continue
		}
		switch {
		case strings.HasPrefix(line[i:], `"""`):
			state, i = `"""`, i+3
		case strings.HasPrefix(line[i:], `'''`):
			state, i = `'''`, i+3
		case line[i] == '"':
			// A basic string on one line, with backslash escapes.
			i++
			for i < len(line) && line[i] != '"' {
				if line[i] == '\\' {
					i++
				}
				i++
			}
			i++
		case line[i] == '\'':
			if j := strings.IndexByte(line[i+1:], '\''); j >= 0 {
				i += j + 2
			} else {
				i = len(line)
			}
		case line[i] == '#':
			return ""
		default:
			i++
		}
	}
	return state
}

const (
	keyMCPServers = `(?:"mcp_servers"|'mcp_servers'|mcp_servers)`
	keyMai        = `(?:"mai"|'mai'|mai)`
	lineComment   = `\s*(?:#.*)?$`
)

var (
	// tomlHeader matches a table header or an array-of-tables header.
	tomlHeader = regexp.MustCompile(`^\s*\[`)
	// maiHeader matches the header of the table mcp_servers.mai in each form of TOML keys,
	// also the array form [[mcp_servers.mai]].
	maiHeader = regexp.MustCompile(`^\s*\[\[?\s*` + keyMCPServers + `\s*\.\s*` + keyMai + `\s*\]\]?` + lineComment)
	// maiSubHeader matches the header of a sub-table of mcp_servers.mai, such as
	// [mcp_servers.mai.env].
	maiSubHeader = regexp.MustCompile(`^\s*\[\[?\s*` + keyMCPServers + `\s*\.\s*` + keyMai + `\s*\.`)
	// mcpServersHeader matches the header of the table mcp_servers.
	mcpServersHeader = regexp.MustCompile(`^\s*\[\s*` + keyMCPServers + `\s*\]` + lineComment)
	// maiKey matches a key mai, or a dotted key that starts with mai, in the table mcp_servers.
	maiKey = regexp.MustCompile(`^\s*` + keyMai + `\s*[=.]`)
	// rootDottedMai matches a dotted key of the root table that defines mcp_servers.mai.
	rootDottedMai = regexp.MustCompile(`^\s*` + keyMCPServers + `\s*\.\s*` + keyMai + `\s*[=.]`)
	// rootInline matches mcp_servers as an inline table of the root table. TOML does not let a
	// table header add to an inline table, thus mai cannot add its table then.
	rootInline = regexp.MustCompile(`^\s*` + keyMCPServers + `\s*=\s*\{`)
	// keyLine matches a line that holds a key, not a blank line or a comment.
	keyLine = regexp.MustCompile(`^\s*[^\s#]`)
	// commandLine and argsLine match the two keys that mai writes in its table.
	commandLine = regexp.MustCompile(`^\s*("command"|'command'|command)\s*=\s*(.*)$`)
	argsLine    = regexp.MustCompile(`^\s*("args"|'args'|args)\s*=`)
)

// tomlEntry is the entry of mai in a config.toml.
type tomlEntry struct {
	marked  bool // the begin comment of mai is directly above the header
	begin   int  // the index of the begin comment line, or -1
	header  int  // the index of the header line
	lastKey int  // the index of the last key line of the table, or header
	ends    []int
	subs    [][2]int // the first and the last line of each sub-table
}

// findMaiTOML returns the entry of mai: the table [mcp_servers.mai] in each form of header,
// its sub-tables, and the comments of mai. The result is false when the file has no such
// table. A table that has no begin comment of mai directly above it gives marked false.
func findMaiTOML(lines []tomlLine) (tomlEntry, bool) {
	begin := strings.TrimRight(tomlBegin, "\n")
	end := strings.TrimRight(tomlEnd, "\n")
	e := tomlEntry{begin: -1, header: -1}
	for i, l := range lines {
		if !l.code {
			continue
		}
		switch {
		case e.header < 0 && maiHeader.MatchString(l.text):
			e.header = i
			if i > 0 && lines[i-1].code && lines[i-1].text == begin {
				e.marked, e.begin = true, i-1
			}
			e.lastKey = lastKeyLine(lines, i)
		case maiSubHeader.MatchString(l.text):
			e.subs = append(e.subs, [2]int{i, lastKeyLine(lines, i)})
		case l.text == end:
			e.ends = append(e.ends, i)
		}
	}
	return e, e.header >= 0
}

// lastKeyLine returns the index of the last key line of the table whose header is at h: the
// last line before the next header that holds a key.
func lastKeyLine(lines []tomlLine, h int) int {
	last := h
	for j := h + 1; j < len(lines); j++ {
		l := lines[j]
		if l.code && tomlHeader.MatchString(l.text) {
			break
		}
		// A line of a multi-line string belongs to the value above it.
		if !l.code || keyLine.MatchString(l.text) {
			last = j
		}
	}
	return last
}

// mainLines returns the indices of the begin comment and of the table, to its last key line.
func (e tomlEntry) mainLines() []int {
	first := e.header
	if e.begin >= 0 {
		first = e.begin
	}
	var out []int
	for i := first; i <= e.lastKey; i++ {
		out = append(out, i)
	}
	return out
}

// allLines returns the indices of each line of the entry.
func (e tomlEntry) allLines() map[int]bool {
	out := map[int]bool{}
	for _, i := range e.mainLines() {
		out[i] = true
	}
	for _, s := range e.subs {
		for i := s[0]; i <= s[1]; i++ {
			out[i] = true
		}
	}
	for _, i := range e.ends {
		out[i] = true
	}
	return out
}

// mainText returns the text of the main table as mai writes it (a line break, the begin
// comment, the table), and the same text with an end comment of 3.0.0 after it, as 3.0.0
// recorded it.
func (e tomlEntry) mainText(body []byte, lines []tomlLine, nl string) (text, with300 []byte) {
	ml := e.mainLines()
	text = append([]byte(nl), body[lines[ml[0]].start:lines[ml[len(ml)-1]].end]...)
	if len(e.ends) > 0 {
		end := e.ends[0]
		with300 = append(append([]byte(nil), text...), body[lines[end].start:lines[end].end]...)
	}
	return text, with300
}

// dropLines removes the lines in drop from body. For each run of removed lines, the blank line
// directly above the run goes too, because mai writes a line break before its table. When the
// run that starts at the line first (the start of the table of mai) reaches the end of the
// file and no blank line is above it, the line break of the line above came from mai, and it
// goes too. Each other line break stays, so that no two lines of the user join.
func dropLines(body []byte, lines []tomlLine, drop map[int]bool, first int) []byte {
	drop = copyDrop(drop)
	trimEnd := false
	for i := 0; i < len(lines); i++ {
		if !drop[i] || (i > 0 && drop[i-1]) {
			continue
		}
		// i starts a run.
		j := i
		for j+1 < len(lines) && drop[j+1] {
			j++
		}
		switch {
		case i > 0 && lines[i-1].text == "" && !drop[i-1]:
			drop[i-1] = true
		case i > 0 && i == first && j == len(lines)-1:
			trimEnd = true
		}
		i = j
	}
	var out []byte
	for i, l := range lines {
		if !drop[i] {
			out = append(out, body[l.start:l.end]...)
		}
	}
	if trimEnd {
		out = bytes.TrimSuffix(out, []byte("\n"))
		out = bytes.TrimSuffix(out, []byte("\r"))
	}
	return out
}

func copyDrop(m map[int]bool) map[int]bool {
	out := map[int]bool{}
	for k, v := range m {
		out[k] = v
	}
	return out
}

// otherMaiTable reports if body defines mcp_servers.mai outside the entry e: a second header,
// a sub-table of mai when the file has no table of mai, a key mai in the table mcp_servers, or
// a dotted key of the root table.
func otherMaiTable(lines []tomlLine, e tomlEntry, found bool) bool {
	table := "" // "" for the root table, "mcp_servers", or "other"
	for i, l := range lines {
		if !l.code {
			continue
		}
		t := l.text
		switch {
		case maiHeader.MatchString(t):
			if !found || i != e.header {
				return true
			}
			table = "other"
		case maiSubHeader.MatchString(t):
			if !found {
				return true
			}
			table = "other"
		case mcpServersHeader.MatchString(t):
			table = "mcp_servers"
		case tomlHeader.MatchString(t):
			table = "other"
		case table == "mcp_servers" && maiKey.MatchString(t):
			return true
		case table == "" && rootDottedMai.MatchString(t):
			return true
		}
	}
	return false
}

// inlineMCPServers reports if the root table defines mcp_servers as an inline table.
func inlineMCPServers(lines []tomlLine) bool {
	for _, l := range lines {
		if !l.code {
			continue
		}
		if tomlHeader.MatchString(l.text) {
			return false
		}
		if rootInline.MatchString(l.text) {
			return true
		}
	}
	return false
}

// tomlBlock is the text that mai writes in config.toml, with the line break of the file.
func tomlBlock(table, nl string) []byte {
	return []byte(strings.ReplaceAll("\n"+tomlBegin+table, "\n", nl))
}

// tomlStringValue reads a TOML basic or literal string on one line, and reports false for each
// other value.
func tomlStringValue(v string) (string, bool) {
	v = strings.TrimSpace(v)
	if i := strings.Index(v, " #"); i >= 0 && !strings.HasPrefix(v, `"`) && !strings.HasPrefix(v, "'") {
		v = strings.TrimSpace(v[:i])
	}
	switch {
	case strings.HasPrefix(v, "'"):
		if j := strings.IndexByte(v[1:], '\''); j >= 0 {
			return v[1 : j+1], true
		}
	case strings.HasPrefix(v, `"`):
		var b strings.Builder
		for i := 1; i < len(v); i++ {
			switch v[i] {
			case '"':
				return b.String(), true
			case '\\':
				if i+1 < len(v) {
					i++
					switch v[i] {
					case 'n':
						b.WriteByte('\n')
					case 't':
						b.WriteByte('\t')
					default:
						b.WriteByte(v[i])
					}
				}
			default:
				b.WriteByte(v[i])
			}
		}
	}
	return "", false
}

// updateMaiTable sets the keys command and args of the table of mai to the values of the new
// table text, and keeps each other line of the table. It removes each end comment of 3.0.0.
// The result is false when the command names a program that is not mai, or when the table has
// a value that mai cannot change line by line.
func updateMaiTable(body []byte, lines []tomlLine, e tomlEntry, table, nl string) ([]byte, bool) {
	var newCommand, newArgs string
	for _, l := range strings.Split(strings.TrimRight(table, "\n"), "\n") {
		switch {
		case commandLine.MatchString(l):
			newCommand = l
		case argsLine.MatchString(l):
			newArgs = l
		}
	}
	replace := map[int]string{}
	drop := map[int]bool{}
	for _, i := range e.ends {
		drop[i] = true
	}
	haveCommand, haveArgs := false, false
	for i := e.header + 1; i <= e.lastKey; i++ {
		l := lines[i]
		if !l.code {
			continue
		}
		if m := commandLine.FindStringSubmatch(l.text); m != nil {
			v, ok := tomlStringValue(m[2])
			if !ok || !isMaiProgram(v) {
				return nil, false
			}
			haveCommand = true
			if strings.TrimSpace(l.text) != newCommand {
				replace[i] = newCommand
			}
		}
		if argsLine.MatchString(l.text) {
			if !strings.Contains(l.text, "]") {
				return nil, false // an array on more than one line
			}
			haveArgs = true
			if strings.TrimSpace(l.text) != newArgs {
				replace[i] = newArgs
			}
		}
	}
	var out []byte
	for i, l := range lines {
		if drop[i] {
			continue
		}
		if r, ok := replace[i]; ok {
			out = append(out, r...)
			out = append(out, nl...)
		} else {
			out = append(out, body[l.start:l.end]...)
		}
		if i == e.header {
			if !haveCommand {
				out = append(out, newCommand+nl...)
			}
			if !haveArgs {
				out = append(out, newArgs+nl...)
			}
		}
	}
	return out, true
}
