package connect

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// object is a JSON object that keeps the sequence of its keys. A value is *object, []any,
// string, json.Number, bool, or nil.
type object struct {
	keys []string
	vals map[string]any
}

func newObject() *object { return &object{vals: map[string]any{}} }

func (o *object) get(k string) (any, bool) {
	v, ok := o.vals[k]
	return v, ok
}

func (o *object) set(k string, v any) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

func (o *object) del(k string) {
	if _, ok := o.vals[k]; !ok {
		return
	}
	delete(o.vals, k)
	for i, x := range o.keys {
		if x == k {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
}

// parseJSON decodes one JSON value and keeps the sequence of the keys of each object.
func parseJSON(b []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	v, err := parseValue(d)
	if err != nil {
		return nil, err
	}
	if _, err := d.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("connect: text after the JSON value")
	}
	return v, nil
}

func parseValue(d *json.Decoder) (any, error) {
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch x := t.(type) {
	case json.Delim:
		switch x {
		case '{':
			o := newObject()
			for d.More() {
				kt, err := d.Token()
				if err != nil {
					return nil, err
				}
				k, ok := kt.(string)
				if !ok {
					return nil, errors.New("connect: a key is not a string")
				}
				v, err := parseValue(d)
				if err != nil {
					return nil, err
				}
				o.set(k, v)
			}
			if _, err := d.Token(); err != nil {
				return nil, err
			}
			return o, nil
		case '[':
			a := []any{}
			for d.More() {
				v, err := parseValue(d)
				if err != nil {
					return nil, err
				}
				a = append(a, v)
			}
			if _, err := d.Token(); err != nil {
				return nil, err
			}
			return a, nil
		}
		return nil, fmt.Errorf("connect: unexpected %v", x)
	default:
		return t, nil
	}
}

// encodeJSON writes v with the indent. An empty indent gives compact JSON.
func encodeJSON(v any, indent string) []byte {
	var b bytes.Buffer
	writeValue(&b, v, indent, 0)
	return b.Bytes()
}

func writeString(b *bytes.Buffer, s string) {
	var t bytes.Buffer
	e := json.NewEncoder(&t)
	e.SetEscapeHTML(false)
	_ = e.Encode(s)
	b.Write(bytes.TrimRight(t.Bytes(), "\n"))
}

func newline(b *bytes.Buffer, indent string, depth int) {
	if indent == "" {
		return
	}
	b.WriteByte('\n')
	b.WriteString(strings.Repeat(indent, depth))
}

func writeValue(b *bytes.Buffer, v any, indent string, depth int) {
	switch x := v.(type) {
	case *object:
		if len(x.keys) == 0 {
			b.WriteString("{}")
			return
		}
		b.WriteByte('{')
		for i, k := range x.keys {
			if i > 0 {
				b.WriteByte(',')
			}
			newline(b, indent, depth+1)
			writeString(b, k)
			b.WriteByte(':')
			if indent != "" {
				b.WriteByte(' ')
			}
			writeValue(b, x.vals[k], indent, depth+1)
		}
		newline(b, indent, depth)
		b.WriteByte('}')
	case []any:
		if len(x) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			newline(b, indent, depth+1)
			writeValue(b, e, indent, depth+1)
		}
		newline(b, indent, depth)
		b.WriteByte(']')
	case string:
		writeString(b, x)
	case json.Number:
		b.WriteString(x.String())
	case int:
		b.WriteString(strconv.Itoa(x))
	case bool:
		if x {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case nil:
		b.WriteString("null")
	default:
		panic(fmt.Sprintf("connect: no JSON form for %T", v))
	}
}

// detectIndent returns the indent of the first indented line, or "" when the text has no
// line break in its first value (compact JSON). An empty text gives two spaces.
func detectIndent(b []byte) string {
	t := bytes.TrimSpace(b)
	if len(t) == 0 {
		return "  "
	}
	i := bytes.IndexByte(t, '\n')
	if i < 0 {
		return ""
	}
	for _, line := range bytes.Split(t[i+1:], []byte("\n")) {
		n := 0
		for n < len(line) && (line[n] == ' ' || line[n] == '\t') {
			n++
		}
		if n > 0 && n < len(line) {
			return string(line[:n])
		}
	}
	return "  "
}

// splitPointer decodes a JSON pointer into its keys.
func splitPointer(p string) []string {
	if p == "" || p == "/" {
		return nil
	}
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	for i, s := range parts {
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(s, "~1", "/"), "~0", "~")
	}
	return parts
}

// escapeKey encodes one key for a JSON pointer.
func escapeKey(k string) string {
	return strings.ReplaceAll(strings.ReplaceAll(k, "~", "~0"), "/", "~1")
}
