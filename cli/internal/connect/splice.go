package connect

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// The functions in this file change a JSON text in place. They add, replace, or remove one
// value and keep each other byte of the text, so that a diff shows only the entry of mai, and
// a remove gives back the bytes from before the add.

// jnode is one JSON value of a text, with its byte span [start, end).
type jnode struct {
	kind    byte // '{' for an object, '[' for an array, 'v' for each other value
	start   int
	end     int
	members []jmember // the members of an object, in the sequence of the text
	elems   []*jnode  // the elements of an array
}

// jmember is one member of an object. keyStart is the offset of the quote of the key.
type jmember struct {
	key      string
	keyStart int
	val      *jnode
}

// member returns the index and the value of the last member with the key, or -1 and nil.
// For a key that occurs two times, the last one is the value, as in encoding/json.
func (n *jnode) member(key string) (int, *jnode) {
	idx, val := -1, (*jnode)(nil)
	for i, m := range n.members {
		if m.key == key {
			idx, val = i, m.val
		}
	}
	return idx, val
}

// childSpans returns the start and the end of each child of an object or an array. The span
// of a member starts at the quote of its key.
func (n *jnode) childSpans() (starts, ends []int) {
	for _, m := range n.members {
		starts = append(starts, m.keyStart)
		ends = append(ends, m.val.end)
	}
	for _, e := range n.elems {
		starts = append(starts, e.start)
		ends = append(ends, e.end)
	}
	return starts, ends
}

type scanner struct {
	b []byte
	i int
}

// scanJSON reads one strict JSON value (no comments, no trailing commas) and records the span
// of each value in it.
func scanJSON(b []byte) (*jnode, error) {
	s := &scanner{b: b}
	s.ws()
	n, err := s.value()
	if err != nil {
		return nil, err
	}
	s.ws()
	if s.i != len(b) {
		return nil, s.errorf("text after the JSON value")
	}
	return n, nil
}

func (s *scanner) errorf(format string, a ...any) error {
	return fmt.Errorf("byte %d: %s", s.i, fmt.Sprintf(format, a...))
}

func (s *scanner) ws() {
	for s.i < len(s.b) {
		switch s.b[s.i] {
		case ' ', '\t', '\n', '\r':
			s.i++
		default:
			return
		}
	}
}

func (s *scanner) value() (*jnode, error) {
	if s.i >= len(s.b) {
		return nil, s.errorf("the text ends before a value")
	}
	switch s.b[s.i] {
	case '{':
		return s.object()
	case '[':
		return s.array()
	case '"':
		start := s.i
		if _, err := s.str(); err != nil {
			return nil, err
		}
		return &jnode{kind: 'v', start: start, end: s.i}, nil
	}
	start := s.i
	for s.i < len(s.b) && !bytes.ContainsRune([]byte(" \t\r\n,]}:"), rune(s.b[s.i])) {
		s.i++
	}
	tok := s.b[start:s.i]
	if len(tok) == 0 || !json.Valid(tok) {
		s.i = start
		return nil, s.errorf("%q is not a JSON value", tok)
	}
	return &jnode{kind: 'v', start: start, end: s.i}, nil
}

func (s *scanner) str() (string, error) {
	start := s.i
	s.i++ // the opening quote
	for s.i < len(s.b) {
		switch c := s.b[s.i]; {
		case c == '\\':
			s.i += 2
		case c == '"':
			s.i++
			var out string
			if err := json.Unmarshal(s.b[start:s.i], &out); err != nil {
				return "", fmt.Errorf("byte %d: %w", start, err)
			}
			return out, nil
		case c < 0x20:
			return "", s.errorf("a control character in a string")
		default:
			s.i++
		}
	}
	return "", s.errorf("a string has no end")
}

func (s *scanner) object() (*jnode, error) {
	n := &jnode{kind: '{', start: s.i}
	s.i++
	s.ws()
	if s.i < len(s.b) && s.b[s.i] == '}' {
		s.i++
		n.end = s.i
		return n, nil
	}
	for {
		s.ws()
		if s.i >= len(s.b) || s.b[s.i] != '"' {
			return nil, s.errorf("a key must be a string")
		}
		ks := s.i
		k, err := s.str()
		if err != nil {
			return nil, err
		}
		s.ws()
		if s.i >= len(s.b) || s.b[s.i] != ':' {
			return nil, s.errorf("a colon must come after a key")
		}
		s.i++
		s.ws()
		v, err := s.value()
		if err != nil {
			return nil, err
		}
		n.members = append(n.members, jmember{key: k, keyStart: ks, val: v})
		s.ws()
		if s.i >= len(s.b) {
			return nil, s.errorf("an object has no end")
		}
		switch s.b[s.i] {
		case ',':
			s.i++
		case '}':
			s.i++
			n.end = s.i
			return n, nil
		default:
			return nil, s.errorf("a comma or a brace must come after a member")
		}
	}
}

func (s *scanner) array() (*jnode, error) {
	n := &jnode{kind: '[', start: s.i}
	s.i++
	s.ws()
	if s.i < len(s.b) && s.b[s.i] == ']' {
		s.i++
		n.end = s.i
		return n, nil
	}
	for {
		s.ws()
		v, err := s.value()
		if err != nil {
			return nil, err
		}
		n.elems = append(n.elems, v)
		s.ws()
		if s.i >= len(s.b) {
			return nil, s.errorf("an array has no end")
		}
		switch s.b[s.i] {
		case ',':
			s.i++
		case ']':
			s.i++
			n.end = s.i
			return n, nil
		default:
			return nil, s.errorf("a comma or a bracket must come after an element")
		}
	}
}

// splice replaces b[from:to] with text, in a new slice.
func splice(b []byte, from, to int, text string) []byte {
	out := make([]byte, 0, len(b)-(to-from)+len(text))
	out = append(out, b[:from]...)
	out = append(out, text...)
	return append(out, b[to:]...)
}

// lineIndent returns the white space at the start of the line that holds pos.
func lineIndent(b []byte, pos int) string {
	ls := bytes.LastIndexByte(b[:pos], '\n') + 1
	e := ls
	for e < len(b) && (b[e] == ' ' || b[e] == '\t') {
		e++
	}
	return string(b[ls:e])
}

// startsLine reports if only white space comes before pos on its line.
func startsLine(b []byte, pos int) bool {
	ls := bytes.LastIndexByte(b[:pos], '\n') + 1
	return len(bytes.TrimLeft(b[ls:pos], " \t")) == 0
}

// multiline reports if the value n has a line break in it.
func multiline(b []byte, n *jnode) bool {
	return bytes.IndexByte(b[n.start:n.end], '\n') >= 0
}

// spaced reports if the container n has a space after its opening bracket, as in "[ 1 ]".
func spaced(b []byte, n *jnode) bool {
	return n.start+1 < len(b) && b[n.start+1] == ' '
}

// layout is the form of a JSON text: the indent unit ("" for a minified text) and the line
// break ("\n", or "\r\n" for a text that a Windows program wrote).
type layout struct {
	unit string
	nl   string
}

// layoutOf returns the layout of the text b. A text with no line break gets two spaces.
func layoutOf(b []byte) layout {
	l := layout{unit: detectIndent(b), nl: "\n"}
	if bytes.Contains(b, []byte("\r\n")) {
		l.nl = "\r\n"
	}
	return l
}

// reindent puts the line break of the layout and prefix at each line break of text.
func (l layout) reindent(text []byte, prefix string) string {
	return strings.ReplaceAll(string(text), "\n", l.nl+prefix)
}

// encodeInline writes v on one line, with ": " and ", " as in a JSON text that a person wrote.
// With pad set, a container has a space inside its brackets, as in "{ "a": 1 }".
func encodeInline(v any, pad bool) string {
	var b strings.Builder
	writeInline(&b, v, pad)
	return b.String()
}

func writeInline(b *strings.Builder, v any, pad bool) {
	open := func(c byte) {
		b.WriteByte(c)
		if pad {
			b.WriteByte(' ')
		}
	}
	shut := func(c byte) {
		if pad {
			b.WriteByte(' ')
		}
		b.WriteByte(c)
	}
	switch x := v.(type) {
	case *object:
		if len(x.keys) == 0 {
			b.WriteString("{}")
			return
		}
		open('{')
		for i, k := range x.keys {
			if i > 0 {
				b.WriteString(", ")
			}
			b.Write(encodeJSON(k, ""))
			b.WriteString(": ")
			writeInline(b, x.vals[k], pad)
		}
		shut('}')
	case []any:
		if len(x) == 0 {
			b.WriteString("[]")
			return
		}
		open('[')
		for i, e := range x {
			if i > 0 {
				b.WriteString(", ")
			}
			writeInline(b, e, pad)
		}
		shut(']')
	default:
		b.Write(encodeJSON(x, ""))
	}
}

// childText returns the text of a new child value v of the container n, when the child goes on
// the line of the container. key is the quoted key and ": " for an object member, or "" for an
// array element.
func childText(b []byte, n *jnode, key string, v any, unit string) (text string, inline bool) {
	switch {
	case unit == "":
		return strings.Replace(key, ": ", ":", 1) + string(encodeJSON(v, "")), true
	case len(n.members) == 0 && len(n.elems) == 0:
		return "", false
	case !multiline(b, n):
		return key + encodeInline(v, spaced(b, n)), true
	}
	return "", false
}

// insertChild adds the value v as the last child of the container n: a member with the key
// for an object, an element for an array (key is ""). The layout follows the container. For
// an empty container, the result also gives the bytes between its brackets before the
// insert, so that a remove can put them back.
func insertChild(b []byte, n *jnode, key string, v any, l layout) (out []byte, emptyInner *string) {
	kt := ""
	if n.kind == '{' {
		kt = string(encodeJSON(key, "")) + ": "
	}
	starts, ends := n.childSpans()
	if len(starts) == 0 {
		inner := string(b[n.start+1 : n.end-1])
		emptyInner = &inner
	}
	if text, inline := childText(b, n, kt, v, l.unit); inline {
		if len(starts) == 0 {
			return splice(b, n.start+1, n.end-1, text), emptyInner
		}
		sep := ", "
		if l.unit == "" {
			sep = ","
		}
		return splice(b, ends[len(ends)-1], ends[len(ends)-1], sep+text), emptyInner
	}
	if len(starts) == 0 {
		// An empty container gets the child on a new line, and the closing bracket on the
		// next line with the indent of the line of the opening bracket.
		ind := lineIndent(b, n.start)
		child := ind + l.unit
		return splice(b, n.start+1, n.end-1, l.nl+child+kt+l.reindent(encodeJSON(v, l.unit), child)+l.nl+ind), emptyInner
	}
	child := lineIndent(b, n.start) + l.unit
	if startsLine(b, starts[0]) {
		child = lineIndent(b, starts[0])
	}
	last := ends[len(ends)-1]
	return splice(b, last, last, ","+l.nl+child+kt+l.reindent(encodeJSON(v, l.unit), child)), nil
}

// replaceNode replaces the value n with v. parent is the container of n.
func replaceNode(b []byte, n, parent *jnode, v any, l layout) []byte {
	switch {
	case l.unit == "":
		return splice(b, n.start, n.end, string(encodeJSON(v, "")))
	case !multiline(b, parent):
		return splice(b, n.start, n.end, encodeInline(v, spaced(b, parent)))
	}
	return splice(b, n.start, n.end, l.reindent(encodeJSON(v, l.unit), lineIndent(b, n.start)))
}

// removeChild removes the child i of the container n with its comma. When n has no other
// child, the bytes between its brackets become inner: the bytes from before mai connect, or ""
// for "{}" and "[]".
func removeChild(b []byte, n *jnode, i int, inner string) []byte {
	starts, ends := n.childSpans()
	switch {
	case len(starts) == 1:
		return splice(b, n.start+1, n.end-1, inner)
	case i > 0:
		return splice(b, ends[i-1], ends[i], "")
	default:
		return splice(b, starts[0], starts[1], "")
	}
}

// canonical returns the compact JSON of the value n, the form that the hashes of the manifest
// use. A value that does not parse gives its text.
func canonical(b []byte, n *jnode) []byte {
	v, err := parseJSON(b[n.start:n.end])
	if err != nil {
		return b[n.start:n.end]
	}
	return encodeJSON(v, "")
}

// nest puts leaf under the keys, as nested objects. With no keys, the result is leaf.
func nest(keys []string, leaf any) any {
	if len(keys) == 0 {
		return leaf
	}
	return obj(keys[0], nest(keys[1:], leaf))
}
