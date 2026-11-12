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
	"regexp"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/monorithm/mai/cli/v3/internal/manifest"
	"github.com/monorithm/mai/cli/v3/internal/paths"
)

// ErrExists means that the file has a mai entry with a different content. Run mai disconnect
// first, or remove the entry by hand.
var ErrExists = errors.New("connect: the file has a different mai entry")

// The begin comment of the table of mai in the config.toml of Codex, and the end comment that
// mai 3.0.0 wrote after the table. mai reads the end comment, but does not write it.
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
	app     string
	changes []change
}

// record is the local note of one entry: the text that mai wrote, the pointer of each object
// or array that did not exist before mai connect, the bytes between the brackets of each
// container that was empty before, and the text of a file that held only white space.
type record struct {
	Text    string            `json:"text"`
	Created []string          `json:"created,omitempty"`
	Empty   map[string]string `json:"empty,omitempty"`
	Blank   *string           `json:"blank,omitempty"`
	// IfAbsent marks an entry that mai writes only into a file that does not have the key,
	// such as "version" of a hooks file. mai removes it only when no content of the user is
	// in the file.
	IfAbsent bool `json:"if_absent,omitempty"`
	// HadContent marks an IfAbsent entry in a file that held other content before mai wrote
	// the key: that content did not need the key, thus mai removes it again.
	HadContent bool `json:"had_content,omitempty"`
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

// temporaryBuild matches the binary of "go run": the work folder go-buildNNN in the temporary
// folder, or a binary in the build cache (<cache>/xx/<64 hex>-d/<name>), which Go deletes.
var temporaryBuild = regexp.MustCompile(`(^|/)go-build[0-9]+/|/[0-9a-f]{2}/[0-9a-f]{64}-d/[^/]+$`)

// isTemporaryBuild reports if p is a binary of "go run".
func isTemporaryBuild(p string) bool {
	return temporaryBuild.MatchString(filepath.ToSlash(p))
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
	if !utf8.ValidString(bin) {
		return nil, fmt.Errorf("connect: the path of the mai binary is not valid UTF-8, thus a JSON or TOML file cannot hold it: %q", bin)
	}
	cs, err := changes(home, app, agent, bin, runtime.GOOS)
	if err != nil {
		return nil, err
	}
	p := &Plan{App: app, Agent: agent}
	byPath := map[string][]change{}
	var order []string
	for _, c := range cs {
		if _, ok := byPath[c.path]; !ok {
			order = append(order, c.path)
		}
		byPath[c.path] = append(byPath[c.path], c)
	}
	for _, path := range order {
		before, existed, err := readFile(path)
		if err != nil {
			return nil, err
		}
		fc, err := planFile(path, before, existed, byPath[path], app)
		if err != nil {
			return nil, err
		}
		p.Files = append(p.Files, fc)
	}
	return p, nil
}

// planFile computes the change of one file from its text before.
func planFile(path string, before []byte, existed bool, cs []change, app string) (*FileChange, error) {
	fc := &FileChange{Path: path, Before: before, Existed: existed, records: map[string]record{}, app: app, changes: cs}
	text := append([]byte(nil), before...)
	if len(cs) > 0 && cs[0].pointer == TOMLPointer {
		for _, c := range cs {
			body, item, entry, err := addTOML(text, c, app)
			if err != nil {
				return nil, err
			}
			text = body
			if item != nil {
				fc.Items = append(fc.Items, *item)
				fc.records[item.Pointer] = record{Text: string(entry)}
			}
		}
		fc.After = text
		return fc, nil
	}
	orig, err := loadDoc(path, before)
	if err != nil {
		return nil, err
	}
	l := layoutOf(before)
	var blank *string
	if len(bytes.TrimSpace(before)) == 0 {
		// A new or empty file starts as an empty object with an indent of two spaces. A file
		// that held only white space gets that text back at the end of mai disconnect.
		text, l = []byte("{}"), layout{unit: "  ", nl: "\n"}
		if existed {
			s := string(before)
			blank = &s
		}
	}
	// The bytes of each container that was empty before go into each record of the file,
	// because the last entry that leaves a container is not always the first one that went in.
	empty := map[string]string{}
	for _, c := range cs {
		res, err := addText(text, c, app, l)
		if err != nil {
			return nil, err
		}
		text = res.text
		if res.emptyInner != nil {
			if _, ok := empty[res.emptyPointer]; !ok {
				empty[res.emptyPointer] = *res.emptyInner
			}
		}
		if res.item != nil {
			fc.Items = append(fc.Items, *res.item)
			r := record{Text: string(res.entry), Created: created(orig, c.pointer), Blank: blank, IfAbsent: c.ifAbsent}
			if c.ifAbsent {
				key := splitPointer(c.pointer)[0]
				for _, k := range orig.keys {
					if k != key && hasValue(orig.vals[k]) {
						r.HadContent = true
					}
				}
			}
			fc.records[res.item.Pointer] = r
		}
	}
	if len(empty) > 0 {
		for k, r := range fc.records {
			r.Empty = empty
			fc.records[k] = r
		}
	}
	if len(bytes.TrimSpace(before)) == 0 {
		text = append(text, l.nl...)
	}
	fc.After = text
	return fc, nil
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

// addResult is the result of addText.
type addResult struct {
	text  []byte
	item  *manifest.Item // nil: no record (an ifAbsent entry whose key exists)
	entry []byte         // the compact text of the entry
	// emptyPointer and emptyInner give the container that was empty before the insert, and
	// the bytes between its brackets.
	emptyPointer string
	emptyInner   *string
}

// addText adds the entry of c to the JSON text b. Each other byte of b stays.
func addText(b []byte, c change, app string, l layout) (addResult, error) {
	root, err := scanJSON(b)
	if err != nil {
		return addResult{}, fmt.Errorf("connect: %s is not plain JSON (comments are not supported): %w", c.path, err)
	}
	if root.kind != '{' {
		return addResult{}, fmt.Errorf("connect: %s does not hold a JSON object", c.path)
	}
	entry := encodeJSON(c.value, "")
	h := hashOf(entry)
	res := addResult{text: b, item: &manifest.Item{Type: manifest.AppEntry, Name: app, Path: c.path, Pointer: c.pointer, Hash: h}, entry: entry}
	insert := func(n *jnode, pointer, key string, v any) (addResult, error) {
		out, inner := insertChild(b, n, key, v, l)
		res.text = out
		if inner != nil {
			res.emptyPointer, res.emptyInner = pointer, inner
		}
		return res, nil
	}
	replace := func(n, parent *jnode, v any) (addResult, error) {
		res.text = replaceNode(b, n, parent, v, l)
		return res, nil
	}
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
			return insert(cur, pointerOf(keys[:i]), k, nest(keys[i+1:], leaf))
		}
		if v.kind != '{' {
			return addResult{}, fmt.Errorf("connect: %s: the key %q is not an object", c.path, k)
		}
		cur = v
	}
	last := keys[len(keys)-1]
	_, v := cur.member(last)
	if v == nil {
		return insert(cur, pointerOf(keys[:len(keys)-1]), last, leaf)
	}
	if !appendItem {
		switch {
		case c.ifAbsent:
			return addResult{text: b}, nil
		case hashOf(canonical(b, v)) == h:
			return res, nil
		case c.ownsText(b, v) && c.mcp && v.kind == '{':
			// An MCP entry of mai: change only the keys that mai writes, and keep each key
			// that the user added, for example "env".
			res.text = mergeMembers(b, keys, c.value.(*object), l)
			// The record is the entry as it is in the file now: the keys of the user and the
			// sequence of the keys of the file.
			if root, err := scanJSON(res.text); err == nil {
				n := root
				for _, k := range keys {
					if n != nil {
						_, n = n.member(k)
					}
				}
				if n != nil {
					res.entry = canonical(res.text, n)
					res.item.Hash = hashOf(res.entry)
				}
			}
			return res, nil
		case c.ownsText(b, v):
			return replace(v, cur, c.value)
		}
		return addResult{}, fmt.Errorf("%w: %s at %s", ErrExists, c.path, c.pointer)
	}
	if v.kind != '[' {
		return addResult{}, fmt.Errorf("connect: %s: %s is not an array", c.path, last)
	}
	for _, e := range v.elems {
		if hashOf(canonical(b, e)) == h {
			return res, nil
		}
	}
	for ei, e := range v.elems {
		if c.inner == nil {
			if !c.ownsText(b, e) {
				continue
			}
			// A hook object of mai: set the keys of mai, keep each key of the user, and
			// record the hook as it is now.
			out := mergeObjectAt(b, func(root *jnode) *jnode { return elemAt(root, keys, ei) }, c.value.(*object), l)
			return adopt(res, out, func(root *jnode) *jnode { return elemAt(root, keys, ei) }), nil
		}
		// A hook group. mai changes only its own hook object in the group, and keeps each key
		// of the user in the hook and in the group (for example "matcher").
		group, hook := maiHookInGroup(b, e, c.format, c.event)
		if hook == nil {
			continue
		}
		hi := 0
		for j, h := range group.elems {
			if h == hook {
				hi = j
			}
		}
		hookAt := func(root *jnode) *jnode {
			g := elemAt(root, keys, ei)
			if g == nil {
				return nil
			}
			_, hs := g.member("hooks")
			if hs == nil || hi >= len(hs.elems) {
				return nil
			}
			return hs.elems[hi]
		}
		out := mergeObjectAt(b, hookAt, c.inner, l)
		if allMaiHooks(b, group) {
			// The group holds only hooks of mai: it is the entry of mai.
			return adopt(res, out, func(root *jnode) *jnode { return elemAt(root, keys, ei) }), nil
		}
		res.text = out
		return res, nil
	}
	return insert(v, pointerOf(keys), "", c.value)
}

// elemAt returns the element i of the array at keys, or nil.
func elemAt(root *jnode, keys []string, i int) *jnode {
	a := nodeAt(root, keys)
	if a == nil || a.kind != '[' || i >= len(a.elems) {
		return nil
	}
	return a.elems[i]
}

// adopt records the value that at finds in out as the entry, with its text now.
func adopt(res addResult, out []byte, at func(*jnode) *jnode) addResult {
	res.text = out
	if root, err := scanJSON(out); err == nil {
		if n := at(root); n != nil {
			res.entry = canonical(out, n)
			res.item.Hash = hashOf(res.entry)
		}
	}
	return res
}

// mergeObjectAt sets each key of want in the object that at finds, and keeps each other key.
// at runs on each new scan, because each change moves the bytes after it.
func mergeObjectAt(b []byte, at func(*jnode) *jnode, want *object, l layout) []byte {
	for _, k := range want.keys {
		root, err := scanJSON(b)
		if err != nil {
			return b
		}
		n := at(root)
		if n == nil || n.kind != '{' {
			return b
		}
		_, cur := n.member(k)
		switch {
		case cur == nil:
			b, _ = insertChild(b, n, k, want.vals[k], l)
		case hashOf(canonical(b, cur)) != hashOf(encodeJSON(want.vals[k], "")):
			b = replaceNode(b, cur, n, want.vals[k], l)
		}
	}
	return b
}

// mergeMembers sets each key of want in the object at keys of the text b, and keeps each
// other key of that object.
func mergeMembers(b []byte, keys []string, want *object, l layout) []byte {
	for _, k := range want.keys {
		root, err := scanJSON(b)
		if err != nil {
			return b
		}
		n := root
		for _, key := range keys {
			if _, n = n.member(key); n == nil {
				return b
			}
		}
		_, cur := n.member(k)
		switch {
		case cur == nil:
			b, _ = insertChild(b, n, k, want.vals[k], l)
		case hashOf(canonical(b, cur)) != hashOf(encodeJSON(want.vals[k], "")):
			b = replaceNode(b, cur, n, want.vals[k], l)
		}
	}
	return b
}

// maiHookInGroup returns the array "hooks" of the hook group e and the hook object of mai in
// it for format and event, or nil when e is not a group or holds no hook of mai.
func maiHookInGroup(b []byte, e *jnode, format, event string) (group, hook *jnode) {
	if e.kind != '{' {
		return nil, nil
	}
	_, g := e.member("hooks")
	if g == nil || g.kind != '[' {
		return nil, nil
	}
	for _, h := range g.elems {
		if h.kind != '{' {
			continue
		}
		_, cmd := h.member("command")
		if cmd == nil {
			continue
		}
		var s string
		if json.Unmarshal(b[cmd.start:cmd.end], &s) == nil && isMaiHook(s, format, event) {
			return g, h
		}
	}
	return nil, nil
}

// allMaiHooks reports if each hook in the array group has a hook command of mai.
func allMaiHooks(b []byte, group *jnode) bool {
	for _, h := range group.elems {
		if !hasMaiHook(b, h, "", "") {
			return false
		}
	}
	return len(group.elems) > 0
}

// ownsText reports if the value n of the text b is an earlier entry of mai for c.
func (c change) ownsText(b []byte, n *jnode) bool {
	v, err := parseJSON(b[n.start:n.end])
	return err == nil && c.owns(v)
}

// addTOML adds the table of mai to the config.toml of Codex. For an earlier table of mai, it
// changes only the keys command and args, and keeps each other key, comment, and sub-table. A
// definition of mcp_servers.mai that mai did not write, and an inline table mcp_servers, give
// ErrExists.
func addTOML(body []byte, c change, app string) ([]byte, *manifest.Item, []byte, error) {
	nl := layoutOf(body).nl
	block := tomlBlock(c.toml, nl)
	item := &manifest.Item{Type: manifest.AppEntry, Name: app, Path: c.path, Pointer: c.pointer, Hash: hashOf(block)}
	lines := tomlLines(body)
	if inlineMCPServers(lines) {
		return nil, nil, nil, fmt.Errorf("%w: %s defines mcp_servers as an inline table; write it as tables [mcp_servers.NAME], then run mai connect again", ErrExists, c.path)
	}
	e, found := findMaiTOML(lines)
	if otherMaiTable(lines, e, found) || (found && !e.marked) {
		return nil, nil, nil, fmt.Errorf("%w: %s defines mcp_servers.mai, and mai did not write that table; remove that table from the file, or give that server a different name, then run mai connect again", ErrExists, c.path)
	}
	if !found {
		return append(append([]byte(nil), body...), block...), item, block, nil
	}
	if text, _ := e.mainText(body, lines, nl); bytes.Equal(text, block) && len(e.ends) == 0 {
		return body, item, block, nil
	}
	out, ok := updateMaiTable(body, lines, e, c.toml, nl)
	if !ok {
		return nil, nil, nil, fmt.Errorf("%w: the table [mcp_servers.mai] in %s names a server that is not mai, or has an args array on more than one line; remove that table from the file, or give that server a different name, then run mai connect again", ErrExists, c.path)
	}
	// The record is the table as it is now, with the keys of the user in it.
	nlines := tomlLines(out)
	ne, ok := findMaiTOML(nlines)
	if !ok {
		return nil, nil, nil, fmt.Errorf("connect: %s: the table of mai is not readable after the change", c.path)
	}
	text, _ := ne.mainText(out, nlines, nl)
	item.Hash = hashOf(text)
	return out, item, text, nil
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
	var b bytes.Buffer
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
// gets a record of type file too, so that mai disconnect can remove it. When an app wrote a
// file after Prepare, Apply makes the same entries in the new text of the file, so that the
// write of the app stays.
func (p *Plan) Apply(m *manifest.Manifest) error {
	dir, err := entriesDir()
	if err != nil {
		return err
	}
	for i, f := range p.Files {
		cur, existed, err := readFile(f.Path)
		if err != nil {
			return err
		}
		if existed != f.Existed || !bytes.Equal(cur, f.Before) {
			nf, err := planFile(f.Path, cur, existed, f.changes, f.app)
			if err != nil {
				return fmt.Errorf("connect: %s changed after the diff: %w", f.Path, err)
			}
			p.Files[i], f = nf, nf
		}
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
		// An entry of an earlier version of mai can be a container of a new entry, for example
		// /mai of 3.0.0 for /mai/PreInvocation/- of Antigravity. mai made that container and each
		// container in it: the new record gets them, and the old entry goes.
		extra := map[string][]string{}
		newPointers := map[string]bool{}
		for _, it := range f.Items {
			newPointers[it.Pointer] = true
		}
		for _, old := range m.Of(manifest.AppEntry) {
			if old.Name != p.App || old.Path != f.Path || newPointers[old.Pointer] {
				continue
			}
			for _, it := range f.Items {
				if !strings.HasPrefix(it.Pointer, old.Pointer+"/") {
					continue
				}
				keys := splitPointer(it.Pointer)
				for i := len(splitPointer(old.Pointer)); i < len(keys); i++ {
					extra[it.Pointer] = append(extra[it.Pointer], pointerOf(keys[:i]))
				}
				extra[it.Pointer] = append(extra[it.Pointer], loadRecord(old).Created...)
				forget(old)
				if err := m.Remove(old); err != nil {
					return err
				}
				break
			}
		}
		// The containers that mai made are one list for the whole file: an earlier remove can
		// forget the record that knew a container, while a different entry is still in it.
		var fileCreated []string
		for _, old := range m.Of(manifest.AppEntry) {
			if old.Name == p.App && old.Path == f.Path {
				fileCreated = union(fileCreated, loadRecord(old).Created)
			}
		}
		for _, it := range f.Items {
			fileCreated = union(fileCreated, f.records[it.Pointer].Created)
			fileCreated = union(fileCreated, extra[it.Pointer])
		}
		for _, it := range f.Items {
			// An earlier connect of the same entry knows which containers mai made, and their
			// bytes before. Keep that knowledge, and remove the record of a replaced entry.
			r := f.records[it.Pointer]
			r.Created = union(r.Created, fileCreated)
			for _, old := range m.Of(manifest.AppEntry) {
				if old.Name != it.Name || old.Path != it.Path || old.Pointer != it.Pointer {
					continue
				}
				o := loadRecord(old)
				r.Created = union(r.Created, o.Created)
				r.Empty = mergeEmpty(r.Empty, o.Empty)
				if o.Blank != nil {
					r.Blank = o.Blank
				}
				if old.Hash != it.Hash {
					forget(old)
				}
			}
			f.records[it.Pointer] = r
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return err
			}
			rb, err := json.Marshal(r)
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

// writeFile replaces the file and keeps its mode. A new file gets mode 0600. For a symbolic
// link, the file that the link names gets the text, and the link stays.
func writeFile(path string, b []byte) error {
	path = linkTarget(path)
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

// mergeEmpty returns the notes of a, and each note of b for a container that a does not have.
// The note of the earliest connect (b) is the correct one, thus b wins.
func mergeEmpty(a, b map[string]string) map[string]string {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := map[string]string{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

// linkTarget returns the file that a symbolic link at path names, also when that file does not
// exist yet (a dangling link). For a path that is not a link, the result is path.
func linkTarget(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	for range 40 {
		st, err := os.Lstat(path)
		if err != nil || st.Mode()&os.ModeSymlink == 0 {
			return path
		}
		dest, err := os.Readlink(path)
		if err != nil {
			return path
		}
		if !filepath.IsAbs(dest) {
			// The system reads ".." of the link from the real folder of the link.
			dir := filepath.Dir(path)
			if real, err := filepath.EvalSymlinks(dir); err == nil {
				dir = real
			}
			dest = filepath.Join(dir, dest)
		}
		path = dest
	}
	return path
}
