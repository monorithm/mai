// Package connect writes the MCP entry and the hooks of mai in the user configuration files
// of an agent app, and removes exactly those entries again. Each entry that it writes goes
// into the manifest with the SHA-256 of its content. The entries hold no secret.
package connect

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/monorithm/mai/cli/internal/manifest"
	"github.com/monorithm/mai/cli/internal/paths"
)

// ErrExists means that the file has a mai entry with a different content. Run mai disconnect
// first, or remove the entry by hand.
var ErrExists = errors.New("connect: the file has a different mai entry")

// Block markers of the TOML table in the config.toml of Codex.
const (
	tomlBegin = "# >>> mai >>> (mai connect writes this block; mai disconnect removes it)\n"
	tomlEnd   = "# <<< mai <<<\n"
)

// FileChange is the change of one file.
type FileChange struct {
	Path    string
	Before  []byte
	After   []byte
	Existed bool
	Items   []manifest.Item
	records map[string]record // the record of each entry, by its pointer
}

// record is the local note of one entry: the text that mai wrote, and the pointer of each
// object or array that did not exist before mai connect.
type record struct {
	Text    string   `json:"text"`
	Created []string `json:"created,omitempty"`
}

func recordName(it manifest.Item) string {
	return hashOf([]byte(it.Path + "\x00" + it.Pointer + "\x00" + it.Hash))
}

// Plan is the set of changes of mai connect for one app.
type Plan struct {
	App   string
	Agent string
	Files []*FileChange
}

func hashOf(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func readFile(path string) ([]byte, bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	return b, err == nil, err
}

// Prepare computes the changes for app and agent in the files under home. It writes nothing.
func Prepare(home, app, agent string) (*Plan, error) {
	if agent == "" {
		return nil, errors.New("connect: the agent name is empty")
	}
	cs, err := changes(home, app, agent)
	if err != nil {
		return nil, err
	}
	p := &Plan{App: app, Agent: agent}
	byPath := map[string]*FileChange{}
	docs := map[string]any{}
	origs := map[string]*object{}
	tomls := map[string][]byte{}
	for _, c := range cs {
		fc, ok := byPath[c.path]
		if !ok {
			before, existed, err := readFile(c.path)
			if err != nil {
				return nil, err
			}
			fc = &FileChange{Path: c.path, Before: before, Existed: existed, records: map[string]record{}}
			byPath[c.path] = fc
			p.Files = append(p.Files, fc)
			if c.pointer == TOMLPointer {
				tomls[c.path] = append([]byte(nil), before...)
			} else {
				doc, err := loadDoc(c.path, before)
				if err != nil {
					return nil, err
				}
				docs[c.path] = doc
				orig, _ := loadDoc(c.path, before)
				origs[c.path] = orig
			}
		}
		if c.pointer == TOMLPointer {
			body, item, text, err := addTOML(tomls[c.path], c, app)
			if err != nil {
				return nil, err
			}
			tomls[c.path] = body
			if item != nil {
				fc.Items = append(fc.Items, *item)
				fc.records[item.Pointer] = record{Text: string(text)}
			}
			continue
		}
		item, text, err := addJSON(docs[c.path].(*object), c, app)
		if err != nil {
			return nil, err
		}
		if item != nil {
			fc.Items = append(fc.Items, *item)
			fc.records[item.Pointer] = record{Text: string(text), Created: created(origs[c.path], c.pointer)}
		}
	}
	for _, fc := range p.Files {
		if t, ok := tomls[fc.Path]; ok {
			fc.After = t
			continue
		}
		fc.After = renderDoc(fc.Before, docs[fc.Path])
	}
	return p, nil
}

func loadDoc(path string, b []byte) (*object, error) {
	if len(bytes.TrimSpace(b)) == 0 {
		return newObject(), nil
	}
	v, err := parseJSON(b)
	if err != nil {
		return nil, fmt.Errorf("connect: %s is not plain JSON (comments are not supported): %w", path, err)
	}
	o, ok := v.(*object)
	if !ok {
		return nil, fmt.Errorf("connect: %s does not hold a JSON object", path)
	}
	return o, nil
}

// renderDoc encodes doc with the indent and the final line break of the original text.
func renderDoc(before []byte, doc any) []byte {
	out := encodeJSON(doc, detectIndent(before))
	if len(bytes.TrimSpace(before)) == 0 || bytes.HasSuffix(before, []byte("\n")) {
		out = append(out, '\n')
	}
	return out
}

// walk returns the object at keys. With create set, it makes each missing object.
func walk(root *object, keys []string, create bool) (*object, error) {
	cur := root
	for _, k := range keys {
		v, ok := cur.get(k)
		if !ok {
			if !create {
				return nil, nil
			}
			n := newObject()
			cur.set(k, n)
			cur = n
			continue
		}
		o, isObj := v.(*object)
		if !isObj {
			return nil, fmt.Errorf("connect: the key %q is not an object", k)
		}
		cur = o
	}
	return cur, nil
}

// created returns the pointer of each container on the path of pointer that orig does not
// have. mai removes such a container again when it is empty.
func created(orig *object, pointer string) []string {
	// The last key is the entry itself, or "-" for an append. Each key before it is a container.
	keys := splitPointer(pointer)
	keys = keys[:len(keys)-1]
	var out []string
	cur := orig
	prefix := ""
	for _, k := range keys {
		prefix += "/" + escapeKey(k)
		var v any
		ok := false
		if cur != nil {
			v, ok = cur.get(k)
		}
		if !ok {
			out = append(out, prefix)
			cur = nil
			continue
		}
		cur, _ = v.(*object)
	}
	return out
}

func addJSON(doc *object, c change, app string) (*manifest.Item, []byte, error) {
	keys := splitPointer(c.pointer)
	text := encodeJSON(c.value, "")
	h := hashOf(text)
	item := &manifest.Item{Type: manifest.AppEntry, Name: app, Path: c.path, Pointer: c.pointer, Hash: h}
	if keys[len(keys)-1] == "-" {
		parent, err := walk(doc, keys[:len(keys)-2], true)
		if err != nil {
			return nil, nil, err
		}
		k := keys[len(keys)-2]
		v, ok := parent.get(k)
		var arr []any
		if ok {
			a, isArr := v.([]any)
			if !isArr {
				return nil, nil, fmt.Errorf("connect: %s: %s is not an array", c.path, k)
			}
			arr = a
		}
		for _, e := range arr {
			if hashOf(encodeJSON(e, "")) == h {
				return item, text, nil
			}
		}
		parent.set(k, append(arr, c.value))
		return item, text, nil
	}
	parent, err := walk(doc, keys[:len(keys)-1], true)
	if err != nil {
		return nil, nil, err
	}
	k := keys[len(keys)-1]
	if v, ok := parent.get(k); ok {
		if c.ifAbsent {
			return nil, nil, nil
		}
		if hashOf(encodeJSON(v, "")) == h {
			return item, text, nil
		}
		return nil, nil, fmt.Errorf("%w: %s at %s", ErrExists, c.path, c.pointer)
	}
	parent.set(k, c.value)
	return item, text, nil
}

func addTOML(body []byte, c change, app string) ([]byte, *manifest.Item, []byte, error) {
	block := []byte("\n" + tomlBegin + c.toml + tomlEnd)
	item := &manifest.Item{Type: manifest.AppEntry, Name: app, Path: c.path, Pointer: c.pointer, Hash: hashOf(block)}
	if bytes.Contains(body, block) {
		return body, item, block, nil
	}
	if bytes.Contains(body, []byte(tomlBegin)) || bytes.Contains(body, []byte("[mcp_servers.mai]")) {
		return nil, nil, nil, fmt.Errorf("%w: %s at %s", ErrExists, c.path, c.pointer)
	}
	return append(append([]byte(nil), body...), block...), item, block, nil
}

// Changed reports if the plan changes a file.
func (p *Plan) Changed() bool {
	for _, f := range p.Files {
		if !bytes.Equal(f.Before, f.After) || !f.Existed {
			return true
		}
	}
	return false
}

// Diff returns a unified diff of each change of the plan.
func (p *Plan) Diff() string {
	var b strings.Builder
	for _, f := range p.Files {
		if f.Existed && bytes.Equal(f.Before, f.After) {
			continue
		}
		from := f.Path
		if !f.Existed {
			from = "/dev/null"
		}
		b.WriteString(unifiedDiff(from, f.Path, string(f.Before), string(f.After)))
	}
	return b.String()
}

func entriesDir() (string, error) {
	d, err := paths.Default()
	if err != nil {
		return "", err
	}
	return filepath.Join(d.State, "entries"), nil
}

// Apply writes the files of the plan and records each entry in m. A file that did not exist
// gets a record of type file too, so that mai disconnect can remove it.
func (p *Plan) Apply(m *manifest.Manifest) error {
	dir, err := entriesDir()
	if err != nil {
		return err
	}
	for _, f := range p.Files {
		if !f.Existed || !bytes.Equal(f.Before, f.After) {
			if err := writeFile(f.Path, f.After); err != nil {
				return err
			}
		}
		if !f.Existed {
			if err := m.Add(manifest.Item{Type: manifest.File, Name: p.App, Path: f.Path}); err != nil {
				return err
			}
		}
		for _, it := range f.Items {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return err
			}
			rb, err := json.Marshal(f.records[it.Pointer])
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(dir, recordName(it)), rb, 0o600); err != nil {
				return err
			}
			if err := m.Add(it); err != nil {
				return err
			}
		}
	}
	return nil
}

// writeFile replaces the file and keeps its mode. A new file gets mode 0600.
func writeFile(path string, b []byte) error {
	mode := os.FileMode(0o600)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".mai-tmp"
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
