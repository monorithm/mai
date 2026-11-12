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
	// code is false for a line that continues a value of the line above it: a line in a
	// multi-line string, or in an array on more than one line.
	code bool
}

func tomlLines(body []byte) []tomlLine {
	var out []tomlLine
	var sc tomlScan
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
		out = append(out, tomlLine{start: start, end: end, text: text, code: sc.str == "" && sc.depth == 0})
		sc.line(text)
		start = end
	}
	return out
}

// tomlScan is the state of the scan at the end of a line: the delimiter of an open
// multi-line string ("" for none), and the depth of open arrays of a value.
type tomlScan struct {
	str   string
	depth int
}

// line reads one line and updates the state.
func (sc *tomlScan) line(line string) {
	header := sc.str == "" && sc.depth == 0 && strings.HasPrefix(strings.TrimSpace(line), "[")
	for i := 0; i < len(line); {
		if sc.str != "" {
			// In a multi-line basic string, a backslash escapes the next character.
			if sc.str == `"""` && line[i] == '\\' {
				i += 2
				continue
			}
			if strings.HasPrefix(line[i:], sc.str) {
				i += 3
				// A closing delimiter can have one or two quotes of the content before it.
				for n := 0; n < 2 && i < len(line) && line[i] == sc.str[0]; n++ {
					i++
				}
				sc.str = ""
				continue
			}
			i++
			continue
		}
		switch c := line[i]; {
		case strings.HasPrefix(line[i:], `"""`):
			sc.str, i = `"""`, i+3
		case strings.HasPrefix(line[i:], `'''`):
			sc.str, i = `'''`, i+3
		case c == '"':
			i++
			for i < len(line) && line[i] != '"' {
				if line[i] == '\\' {
					i++
				}
				i++
			}
			i++
		case c == '\'':
			if j := strings.IndexByte(line[i+1:], '\''); j >= 0 {
				i += j + 2
			} else {
				i = len(line)
			}
		case c == '#':
			return
		case c == '[' && !header:
			sc.depth++
			i++
		case c == ']' && !header && sc.depth > 0:
			sc.depth--
			i++
		default:
			i++
		}
	}
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
	// argsOfMai matches an args array whose first value is "mcp", as mai writes it.
	argsOfMai = regexp.MustCompile(`^\s*("args"|'args'|args)\s*=\s*\[\s*("mcp"|'mcp')\s*[,\]]`)
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
			// The begin comment of mai is above the header, also with blank lines or comments
			// of the user between them.
			for j := i - 1; j >= 0 && lines[j].code && !keyLine.MatchString(lines[j].text); j-- {
				if lines[j].text == begin {
					e.marked, e.begin = true, j
					break
				}
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
// The lines of the user between the begin comment and the header are not in it.
func (e tomlEntry) mainLines() []int {
	var out []int
	if e.begin >= 0 {
		out = append(out, e.begin)
	}
	for i := e.header; i <= e.lastKey; i++ {
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
	text = []byte(nl)
	for _, i := range e.mainLines() {
		text = append(text, body[lines[i].start:lines[i].end]...)
	}
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
		above := ""
		if i > 0 {
			above = string(body[lines[i-1].start:lines[i-1].end])
		}
		switch {
		case (above == "\n" || above == "\r\n") && !drop[i-1]:
			// The blank line that mai wrote before its table: only a line break.
			drop[i-1] = true
		case i > 0 && i == first && j == len(lines)-1 && !strings.HasPrefix(strings.TrimSpace(lines[i-1].text), "#"):
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

// splitComment returns the part of a TOML line before a comment, and the comment with the
// white space before it ("" for none).
func splitComment(line string) (code, comment string) {
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '"', '\'':
			// Skip a string on one line.
			j := i
			rest := line[j:]
			k := 1
			if rest[0] == '"' {
				for k < len(rest) && rest[k] != '"' {
					if rest[k] == '\\' {
						k++
					}
					k++
				}
			} else if m := strings.IndexByte(rest[1:], '\''); m >= 0 {
				k = m + 1
			} else {
				k = len(rest)
			}
			i = j + k
		case '#':
			code = strings.TrimRight(line[:i], " \t")
			return code, line[len(code):]
		}
	}
	return line, ""
}

// updateMaiTable sets the keys command and args of the table of mai to the values of the new
// table text, and keeps each other line of the table and each comment. It removes each end
// comment of 3.0.0. The result is false when the command names a program that is not mai, or
// when the args value goes on more than one line.
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
			code, _ := splitComment(m[2])
			v, ok := tomlStringValue(code)
			if !ok || !isMaiProgram(v) {
				return nil, false
			}
			haveCommand = true
			want, _ := tomlStringValue(strings.SplitN(newCommand, "=", 2)[1])
			if v != want {
				_, comment := splitComment(l.text)
				replace[i] = newCommand + comment
			}
		}
		if argsLine.MatchString(l.text) {
			value := argsValue(lines, i)
			if !argsOfMai.MatchString(value) {
				return nil, false // a server of the user that runs a program named mai
			}
			haveArgs = true
			have, ok1 := tomlStringArray(strings.SplitN(value, "=", 2)[1])
			want, ok2 := tomlStringArray(strings.SplitN(newArgs, "=", 2)[1])
			if ok1 && ok2 && strings.Join(have, "\x00") == strings.Join(want, "\x00") {
				continue // the same values: each line and comment of the user stays
			}
			// Different values: the array becomes the one line of mai.
			for j := i + 1; j < len(lines) && !lines[j].code; j++ {
				drop[j] = true
			}
			_, comment := splitComment(l.text)
			replace[i] = newArgs + comment
		}
	}
	var out []byte
	add := func(text string) {
		if len(out) > 0 && out[len(out)-1] != '\n' {
			out = append(out, nl...) // the line above had no line break: it was the last line
		}
		out = append(out, text...)
	}
	for i, l := range lines {
		if drop[i] {
			continue
		}
		if r, ok := replace[i]; ok {
			add(r + nl)
		} else {
			add(string(body[l.start:l.end]))
		}
		if i == e.header {
			if !haveCommand {
				add(newCommand + nl)
			}
			if !haveArgs {
				add(newArgs + nl)
			}
		}
	}
	return out, true
}

// argsValue returns the line i and each line after it that continues its value, on one line.
func argsValue(lines []tomlLine, i int) string {
	first, _ := splitComment(lines[i].text)
	parts := []string{first}
	for j := i + 1; j < len(lines) && !lines[j].code; j++ {
		code, _ := splitComment(lines[j].text)
		parts = append(parts, strings.TrimSpace(code))
	}
	return strings.Join(parts, " ")
}

// ownedTOML reports if the table of the entry is a server of mai: its command is a mai binary,
// and its args, if any, start with "mcp".
func ownedTOML(lines []tomlLine, e tomlEntry) bool {
	command := false
	for i := e.header + 1; i <= e.lastKey; i++ {
		if !lines[i].code {
			continue
		}
		if m := commandLine.FindStringSubmatch(lines[i].text); m != nil {
			code, _ := splitComment(m[2])
			v, ok := tomlStringValue(code)
			if !ok || !isMaiProgram(v) {
				return false
			}
			command = true
		}
		if argsLine.MatchString(lines[i].text) && !argsOfMai.MatchString(argsValue(lines, i)) {
			return false
		}
	}
	return command
}

// tomlStringAt reads the string that starts at s[i] (a basic or a literal string on one line),
// and returns its value and the index after it.
func tomlStringAt(s string, i int) (string, int, bool) {
	if i >= len(s) {
		return "", i, false
	}
	switch s[i] {
	case '\'':
		j := strings.IndexByte(s[i+1:], '\'')
		if j < 0 {
			return "", i, false
		}
		return s[i+1 : i+1+j], i + j + 2, true
	case '"':
		j := i + 1
		for j < len(s) && s[j] != '"' {
			if s[j] == '\\' {
				j++
			}
			j++
		}
		if j >= len(s) {
			return "", i, false
		}
		v, ok := tomlStringValue(s[i : j+1])
		return v, j + 1, ok
	}
	return "", i, false
}

// tomlStringArray reads an array of strings, such as ["mcp", '--agent', "a"], also with line
// breaks and comments between the values, and reports false for each other value.
func tomlStringArray(v string) ([]string, bool) {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, "[") {
		return nil, false
	}
	var out []string
	for i := 1; i < len(v); {
		switch c := v[i]; {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == ',':
			i++
		case c == ']':
			return out, true
		case c == '#':
			j := strings.IndexByte(v[i:], '\n')
			if j < 0 {
				return nil, false
			}
			i += j
		default:
			s, next, ok := tomlStringAt(v, i)
			if !ok {
				return nil, false
			}
			out = append(out, s)
			i = next
		}
	}
	return nil, false
}

// sameTOMLTable reports if two texts of the table of mai have the same headers, keys, and
// values, with no regard to comments, white space, line breaks, or the form of a string.
func sameTOMLTable(a, b []byte) bool {
	norm := func(text []byte) []string {
		var out []string
		lines := tomlLines(text)
		for i := 0; i < len(lines); i++ {
			l := lines[i]
			if !l.code {
				continue
			}
			code, _ := splitComment(l.text)
			code = strings.TrimSpace(code)
			if code == "" {
				continue
			}
			if tomlHeader.MatchString(code) {
				out = append(out, "h:"+strings.Join(strings.Fields(code), ""))
				continue
			}
			k, v, ok := strings.Cut(code, "=")
			if !ok {
				out = append(out, "x:"+code)
				continue
			}
			// A value on more than one line continues on the lines that are not code.
			for j := i + 1; j < len(lines) && !lines[j].code; j++ {
				v += "\n" + lines[j].text
				i = j
			}
			k = strings.Trim(strings.TrimSpace(k), `"'`)
			if s, ok := tomlStringValue(v); ok {
				out = append(out, "s:"+k+"="+s)
			} else if arr, ok := tomlStringArray(v); ok {
				out = append(out, "a:"+k+"="+strings.Join(arr, "\x00"))
			} else {
				out = append(out, "v:"+k+"="+strings.TrimSpace(v))
			}
		}
		return out
	}
	x, y := norm(a), norm(b)
	if len(x) != len(y) || len(x) == 0 {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}
