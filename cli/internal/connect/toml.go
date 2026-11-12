package connect

import (
	"bytes"
	"regexp"
	"strings"
)

// The table of mai in the config.toml of Codex starts with the comment line tomlBegin, then
// the header [mcp_servers.mai], then its keys. Codex edits the file with toml_edit, which keeps
// the comment line above the table and puts new tables after the last table. Thus the table of
// mai ends at the next table header, or at the end of the file. The end marker tomlEnd that
// mai 3.0.0 wrote directly after the table is part of the table of mai, if it is there.

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

var (
	// tomlHeader matches a table header or an array-of-tables header.
	tomlHeader = regexp.MustCompile(`^\s*\[`)
	// maiHeader matches the header of the table mcp_servers.mai in each form of TOML keys.
	maiHeader = regexp.MustCompile(`^\s*\[\[?\s*("mcp_servers"|'mcp_servers'|mcp_servers)\s*\.\s*("mai"|'mai'|mai)\s*\]`)
	// mcpServersHeader matches the header of the table mcp_servers.
	mcpServersHeader = regexp.MustCompile(`^\s*\[\s*("mcp_servers"|'mcp_servers'|mcp_servers)\s*\]`)
	// maiKey matches a key mai, or a dotted key that starts with mai, in the table mcp_servers.
	maiKey = regexp.MustCompile(`^\s*("mai"|'mai'|mai)\s*[=.]`)
	// rootMaiKey matches a key of the root table that defines mcp_servers.mai.
	rootMaiKey = regexp.MustCompile(`^\s*("mcp_servers"|'mcp_servers'|mcp_servers)\s*(\.\s*("mai"|'mai'|mai)\s*[=.]|=\s*\{.*\b("mai"|'mai'|mai)\s*=)`)
)

// tomlRegion is the span of the table of mai in body.
type tomlRegion struct {
	start, end int // the span; start includes the line break before the begin comment, if any
	// orphanStart and orphanEnd give the line of an end comment of 3.0.0 after other tables,
	// or -1. toml_edit moves that comment to the end of the file.
	orphanStart, orphanEnd int
}

// replace puts text in place of the region, and removes the orphan end comment.
func (r tomlRegion) replace(body []byte, text string) []byte {
	if r.orphanStart >= 0 {
		body = splice(body, r.orphanStart, r.orphanEnd, "")
	}
	return splice(body, r.start, r.end, text)
}

// findMaiTOML returns the span of the table of mai, or false when the file has no begin
// comment of mai that a header [mcp_servers.mai] follows.
func findMaiTOML(body []byte) (tomlRegion, bool) {
	lines := tomlLines(body)
	begin := strings.TrimRight(tomlBegin, "\n")
	end := strings.TrimRight(tomlEnd, "\n")
	for i, l := range lines {
		if l.text != begin || i+1 >= len(lines) || strings.TrimSpace(lines[i+1].text) != "[mcp_servers.mai]" {
			continue
		}
		r := tomlRegion{start: l.start, orphanStart: -1, orphanEnd: -1}
		if l.start > 0 && body[l.start-1] == '\n' {
			r.start = l.start - 1
			if l.start > 1 && body[l.start-2] == '\r' {
				r.start = l.start - 2
			}
		}
		last, closed := i+1, false
		for j := i + 2; j < len(lines); j++ {
			t := lines[j].text
			if t == end {
				last, closed = j, true
				break
			}
			if tomlHeader.MatchString(t) {
				break
			}
			if strings.TrimSpace(t) != "" {
				last = j
			}
		}
		r.end = lines[last].end
		if !closed {
			for _, o := range lines[last+1:] {
				if o.text == end {
					r.orphanStart, r.orphanEnd = o.start, o.end
					break
				}
			}
		}
		return r, true
	}
	return tomlRegion{}, false
}

// otherMaiTable reports if body defines mcp_servers.mai outside the region of mai: a header in
// a different form, a key mai in the table mcp_servers, or a key of the root table.
func otherMaiTable(body []byte, r tomlRegion, found bool) bool {
	table := "" // "" for the root table, "mcp_servers", or "other"
	for _, l := range tomlLines(body) {
		if found && l.start >= r.start && l.end <= r.end {
			continue
		}
		t := l.text
		switch {
		case maiHeader.MatchString(t):
			return true
		case mcpServersHeader.MatchString(t):
			table = "mcp_servers"
		case tomlHeader.MatchString(t):
			table = "other"
		case table == "mcp_servers" && maiKey.MatchString(t):
			return true
		case table == "" && rootMaiKey.MatchString(t):
			return true
		}
	}
	return false
}

// tomlBlock is the text that mai writes in config.toml, with the line break of the file.
func tomlBlock(table, nl string) []byte {
	return []byte(strings.ReplaceAll("\n"+tomlBegin+table, "\n", nl))
}
