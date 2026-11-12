package connect

import (
	"bytes"
	"regexp"
	"sort"
	"strings"
)

// The table of mai in the config.toml of Codex starts with the comment line tomlBegin, then
// the header [mcp_servers.mai], then its keys. Codex edits the file with toml_edit, which keeps
// the comment line above the table and puts new tables after the last table. Thus the table of
// mai ends after its last key line: a blank line or a comment after it belongs to the next
// table. A sub-table of mai, for example [mcp_servers.mai.env], is part of the entry of mai.
// The end comment tomlEnd that mai 3.0.0 wrote after the table is part of the entry too, also
// when toml_edit moved it to the end of the file.

// tomlLine is one line of a TOML text, with its byte span. end includes the line break.
type tomlLine struct {
	start, end int
	text       string // the line with no line break and no trailing white space
}

func tomlLines(body []byte) []tomlLine {
	var out []tomlLine
	for start := 0; start < len(body); {
		end := bytes.IndexByte(body[start:], '\n')
		if end < 0 {
			end = len(body)
		} else {
			end += start + 1
		}
		out = append(out, tomlLine{start: start, end: end, text: strings.TrimRight(string(body[start:end]), " \t\r\n")})
		start = end
	}
	return out
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
)

// span is a byte range [start, end) of a text.
type span struct{ start, end int }

// tomlEntry is the entry of mai in a config.toml.
type tomlEntry struct {
	marked bool // the begin comment of mai is directly above the header
	main   span // the begin comment and the table [mcp_servers.mai], to its last key line
	subs   []span
	orphan span // an end comment of 3.0.0 that is not directly after the table, or {-1,-1}
}

// findMaiTOML returns the entry of mai: the table [mcp_servers.mai] in each form of header,
// its sub-tables, and the comments of mai. The result is false when the file has no such
// table. A table that has no begin comment of mai above it gives marked false.
func findMaiTOML(body []byte) (tomlEntry, bool) {
	lines := tomlLines(body)
	begin := strings.TrimRight(tomlBegin, "\n")
	end := strings.TrimRight(tomlEnd, "\n")
	e := tomlEntry{orphan: span{-1, -1}}
	found, closed := false, false
	// tableEnd returns the index of the last key line of the table whose header is at h, and
	// whether an end comment of 3.0.0 closed it.
	tableEnd := func(h int) (int, bool) {
		last := h
		for j := h + 1; j < len(lines); j++ {
			t := lines[j].text
			if t == end {
				return j, true
			}
			if tomlHeader.MatchString(t) {
				break
			}
			if keyLine.MatchString(t) {
				last = j
			}
		}
		return last, false
	}
	withBreak := func(start int) int {
		// The line break before a line goes with it, so that a remove leaves no blank line.
		if start > 0 && body[start-1] == '\n' {
			start--
			if start > 0 && body[start-1] == '\r' {
				start--
			}
		}
		return start
	}
	for i := 0; i < len(lines); i++ {
		t := lines[i].text
		switch {
		case !found && maiHeader.MatchString(t):
			found = true
			start := lines[i].start
			if i > 0 && lines[i-1].text == begin {
				e.marked = true
				start = lines[i-1].start
			}
			last, c := tableEnd(i)
			closed = c
			e.main = span{withBreak(start), lines[last].end}
			i = last
		case maiSubHeader.MatchString(t):
			last, _ := tableEnd(i)
			e.subs = append(e.subs, span{withBreak(lines[i].start), lines[last].end})
			i = last
		}
	}
	if !found {
		return e, false
	}
	if !closed {
		for _, l := range lines {
			if l.text == end && l.start >= e.main.end {
				e.orphan = span{l.start, l.end}
				break
			}
		}
	}
	return e, true
}

// covers reports if the line at start is in the main table or a sub-table of the entry.
func (e tomlEntry) covers(start int) bool {
	if start >= e.main.start && start < e.main.end {
		return true
	}
	for _, s := range e.subs {
		if start >= s.start && start < s.end {
			return true
		}
	}
	return false
}

// remove deletes the main table, each sub-table, and the orphan end comment of the entry.
func (e tomlEntry) remove(body []byte) []byte {
	spans := append([]span{e.main}, e.subs...)
	if e.orphan.start >= 0 {
		spans = append(spans, e.orphan)
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start > spans[j].start })
	for _, s := range spans {
		body = splice(body, s.start, s.end, "")
	}
	return body
}

// replaceMain puts block in place of the main table, removes the orphan end comment, and keeps
// the sub-tables of the user.
func (e tomlEntry) replaceMain(body []byte, block string) []byte {
	if e.orphan.start >= 0 {
		body = splice(body, e.orphan.start, e.orphan.end, "")
	}
	return splice(body, e.main.start, e.main.end, block)
}

// mainText returns the text of the main table, and the same text with the orphan end comment
// of 3.0.0 after it, as 3.0.0 recorded it.
func (e tomlEntry) mainText(body []byte) (text, with300 []byte) {
	text = body[e.main.start:e.main.end]
	if e.orphan.start >= 0 {
		with300 = append(append([]byte(nil), text...), body[e.orphan.start:e.orphan.end]...)
	}
	return text, with300
}

// otherMaiTable reports if body defines mcp_servers.mai outside the entry e: a second header,
// a key mai in the table mcp_servers, or a dotted key of the root table.
func otherMaiTable(body []byte, e tomlEntry, found bool) bool {
	table := "" // "" for the root table, "mcp_servers", or "other"
	for _, l := range tomlLines(body) {
		if found && e.covers(l.start) {
			table = "other"
			continue
		}
		t := l.text
		switch {
		case maiHeader.MatchString(t):
			return true
		case maiSubHeader.MatchString(t):
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
func inlineMCPServers(body []byte) bool {
	for _, l := range tomlLines(body) {
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
