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
	"runtime"
	"strings"

	"github.com/monorithm/mai/cli/v3/internal/manifest"
	"github.com/monorithm/mai/cli/v3/internal/paths"
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

// Binary gives the path of the mai binary that the entries name. The apps start mai with the
// PATH of the app, not the PATH of the shell of the user, thus the entries use a full path.
// The default is the path of the running binary. A test replaces it.
var Binary = runningBinary

func runningBinary() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("connect: find the path of the mai binary: %w", err)
	}
	p = filepath.Clean(p)
	if isTemporaryBuild(p) {
		return "", errors.New("connect: this mai is a temporary build of \"go run\"; install mai with \"go install\", then run mai connect again")
	}
	return p, nil
}

// isTemporaryBuild reports if p is in the build cache of "go run", which Go deletes.
func isTemporaryBuild(p string) bool {
	return strings.Contains(filepath.ToSlash(p), "/go-build")
}

// BinaryPath returns the path of the mai binary that Prepare writes in the entries.
func BinaryPath() (string, error) { return Binary() }

// Prepare computes the changes for app and agent in the files under home. It writes nothing.
// A file that exists keeps each byte outside the entries of mai.
func Prepare(home, app, agent string) (*Plan, error) {
	if agent == "" {
		return nil, errors.New("connect: the agent name is empty")
	}
	bin, err := Binary()
	if err != nil {
		return nil, err
	}
	cs, err := changes(home, app, agent, bin, runtime.GOOS)
	if err != nil {
		return nil, err
	}
	p := &Plan{App: app, Agent: agent}
	byPath := map[string]*FileChange{}
	texts := map[string][]byte{}
	units := map[string]string{}
	fresh := map[string]bool{}
	origs := map[string]*object{}
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
			texts[c.path] = append([]byte(nil), before...)
			if c.pointer != TOMLPointer {
				orig, err := loadDoc(c.path, before)
				if err != nil {
					return nil, err
				}
				origs[c.path] = orig
				units[c.path] = detectIndent(before)
				if len(bytes.TrimSpace(before)) == 0 {
					// A new or empty file starts as an empty object with an indent of two spaces.
					texts[c.path] = []byte("{}")
					units[c.path] = "  "
					fresh[c.path] = true
				}
			}
		}
		if c.pointer == TOMLPointer {
			body, item, text, err := addTOML(texts[c.path], c, app)
			if err != nil {
				return nil, err
			}
			texts[c.path] = body
			if item != nil {
				fc.Items = append(fc.Items, *item)
				fc.records[item.Pointer] = record{Text: string(text)}
			}
			continue
		}
		body, item, text, err := addText(texts[c.path], c, app, units[c.path])
		if err != nil {
			return nil, err
		}
		texts[c.path] = body
		if item != nil {
			fc.Items = append(fc.Items, *item)
			fc.records[item.Pointer] = record{Text: string(text), Created: created(origs[c.path], c.pointer)}
		}
	}
	for _, fc := range p.Files {
		fc.After = texts[fc.Path]
		if fresh[fc.Path] {
			fc.After = append(fc.After, '\n')
		}
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

// addText adds the entry of c to the JSON text b. Each other byte of b stays. The result is the
// new text, the manifest item, and the compact text of the entry. A nil item means that the
// entry is not recorded: an ifAbsent entry whose key exists.
func addText(b []byte, c change, app, unit string) ([]byte, *manifest.Item, []byte, error) {
	root, err := scanJSON(b)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("connect: %s is not plain JSON (comments are not supported): %w", c.path, err)
	}
	if root.kind != '{' {
		return nil, nil, nil, fmt.Errorf("connect: %s does not hold a JSON object", c.path)
	}
	text := encodeJSON(c.value, "")
	h := hashOf(text)
	item := &manifest.Item{Type: manifest.AppEntry, Name: app, Path: c.path, Pointer: c.pointer, Hash: h}
	keys := splitPointer(c.pointer)
	appendItem := keys[len(keys)-1] == "-"
	if appendItem {
		keys = keys[:len(keys)-1]
	}
	leaf := c.value
	if appendItem {
		leaf = []any{c.value}
	}
	// Each key before the last one is an object. A missing object goes in with the entry.
	cur := root
	for i, k := range keys[:len(keys)-1] {
		_, v := cur.member(k)
		if v == nil {
			return insertChild(b, cur, k, nest(keys[i+1:], leaf), unit), item, text, nil
		}
		if v.kind != '{' {
			return nil, nil, nil, fmt.Errorf("connect: %s: the key %q is not an object", c.path, k)
		}
		cur = v
	}
	last := keys[len(keys)-1]
	_, v := cur.member(last)
	if v == nil {
		return insertChild(b, cur, last, leaf, unit), item, text, nil
	}
	if !appendItem {
		switch {
		case c.ifAbsent:
			return b, nil, nil, nil
		case hashOf(canonical(b, v)) == h:
			return b, item, text, nil
		case c.ownsText(b, v):
			return replaceNode(b, v, cur, c.value, unit), item, text, nil
		}
		return nil, nil, nil, fmt.Errorf("%w: %s at %s", ErrExists, c.path, c.pointer)
	}
	if v.kind != '[' {
		return nil, nil, nil, fmt.Errorf("connect: %s: %s is not an array", c.path, last)
	}
	for _, e := range v.elems {
		if hashOf(canonical(b, e)) == h {
			return b, item, text, nil
		}
	}
	for _, e := range v.elems {
		if c.ownsText(b, e) {
			return replaceNode(b, e, v, c.value, unit), item, text, nil
		}
	}
	return insertChild(b, v, "", c.value, unit), item, text, nil
}

// ownsText reports if the value n of the text b is an earlier entry of mai for c.
func (c change) ownsText(b []byte, n *jnode) bool {
	v, err := parseJSON(b[n.start:n.end])
	return err == nil && c.owns(v)
}

func addTOML(body []byte, c change, app string) ([]byte, *manifest.Item, []byte, error) {
	block := []byte("\n" + tomlBegin + c.toml + tomlEnd)
	item := &manifest.Item{Type: manifest.AppEntry, Name: app, Path: c.path, Pointer: c.pointer, Hash: hashOf(block)}
	if bytes.Contains(body, block) {
		return body, item, block, nil
	}
	// A block between the markers of mai is an earlier entry of mai: replace it.
	if start := bytes.Index(body, []byte("\n"+tomlBegin)); start >= 0 {
		if end := bytes.Index(body[start:], []byte(tomlEnd)); end >= 0 {
			end += start + len(tomlEnd)
			return splice(body, start, end, string(block)), item, block, nil
		}
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
			// An earlier connect of the same entry knows which containers mai made before the
			// file had them. Keep that knowledge, and remove the record of a replaced entry.
			r := f.records[it.Pointer]
			for _, old := range m.Of(manifest.AppEntry) {
				if old.Name != it.Name || old.Path != it.Path || old.Pointer != it.Pointer {
					continue
				}
				r.Created = union(r.Created, loadRecord(old).Created)
				if old.Hash != it.Hash {
					forget(old)
				}
			}
			f.records[it.Pointer] = r
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

// union returns the items of a and b, each one time, in the sequence of a and then b.
func union(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range append(append([]string(nil), a...), b...) {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
