package connect

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/monorithm/mai/cli/v3/internal/manifest"
)

// ErrChanged means that the entry in the file is not the entry that mai wrote. Diff shows the
// text that mai wrote and the text in the file now. The caller asks the user, and then calls
// ForceRemoveEntry or keeps the entry.
type ErrChanged struct {
	Item    manifest.Item
	Current string
	Diff    string
}

func (e *ErrChanged) Error() string {
	return fmt.Sprintf("connect: the entry %s in %s changed after mai connect wrote it", e.Item.Pointer, e.Item.Path)
}

func loadRecord(it manifest.Item) record {
	var r record
	dir, err := entriesDir()
	if err != nil {
		return r
	}
	b, err := os.ReadFile(filepath.Join(dir, recordName(it)))
	if err != nil {
		return r
	}
	_ = json.Unmarshal(b, &r)
	return r
}

func changedErr(it manifest.Item, current string) *ErrChanged {
	return &ErrChanged{
		Item:    it,
		Current: current,
		Diff:    unifiedDiff("written by mai", "current", loadRecord(it).Text+"\n", current+"\n"),
	}
}

func forget(it manifest.Item) {
	if dir, err := entriesDir(); err == nil {
		_ = os.Remove(filepath.Join(dir, recordName(it)))
	}
}

// ErrKept means that the entry stays, because other content of the file needs it, for example
// "version" of a hooks file that holds hooks. The record of the entry stays too.
var ErrKept = errors.New("connect: the entry stays, because other content of the file needs it")

// RemoveEntry removes exactly the entry of the item. An entry that is gone already gives no
// error. An entry with a different hash gives *ErrChanged and the file stays the same.
func RemoveEntry(it manifest.Item) error { return remove(it, false) }

// ForceRemoveEntry removes the entry of the item also when its content changed.
func ForceRemoveEntry(it manifest.Item) error { return remove(it, true) }

func remove(it manifest.Item, force bool) error {
	if it.Type != manifest.AppEntry {
		return fmt.Errorf("connect: the item has the type %q, not %q", it.Type, manifest.AppEntry)
	}
	before, existed, err := readFile(it.Path)
	if err != nil {
		return err
	}
	if !existed {
		forget(it)
		return nil
	}
	var after []byte
	if it.Pointer == TOMLPointer {
		after, err = removeTOML(before, it, force)
	} else {
		after, err = removeJSON(before, it, force)
	}
	if err != nil {
		return err // also ErrKept: the record stays
	}
	if !bytes.Equal(before, after) {
		if err := writeFile(it.Path, after); err != nil {
			return err
		}
	}
	forget(it)
	return nil
}

// removeTOML removes the table of mai from the config.toml of Codex, with its sub-tables and
// the comments of mai, line by line. A table that is gone gives no error. A table with a
// different text, a sub-table, or a table with no begin comment of mai gives *ErrChanged,
// unless force is set.
func removeTOML(body []byte, it manifest.Item, force bool) ([]byte, error) {
	lines := tomlLines(body)
	e, found := findMaiTOML(lines)
	if !found {
		// The table is gone (for example "codex mcp remove mai"). An end comment of 3.0.0 is
		// a line of mai, and it goes.
		if len(e.ends) > 0 {
			drop := map[int]bool{}
			for _, i := range e.ends {
				drop[i] = true
			}
			return dropLines(body, lines, drop, -1), nil
		}
		return body, nil
	}
	// The record has one kind of line break. 3.0.0 wrote "\n" also in a file with CRLF, and
	// toml_edit of Codex writes "\n" after a key line in a file with CRLF. Thus the text counts
	// as the same with each kind of line break.
	same := false
	var text []byte
	for _, nl := range []string{layoutOf(body).nl, "\n"} {
		t, with300 := e.mainText(body, lines, nl)
		if text == nil {
			text = t
		}
		for _, v := range [][]byte{t, with300} {
			if v == nil {
				continue
			}
			lf := toLF(v)
			crlf := bytes.ReplaceAll(lf, []byte("\n"), []byte("\r\n"))
			if hashOf(v) == it.Hash || hashOf(lf) == it.Hash || hashOf(crlf) == it.Hash {
				same = true
			}
		}
	}
	if !same && sameTOMLTable(text, []byte(loadRecord(it).Text)) {
		same = true // the same keys and values, in a different form (a literal string of toml_edit)
	}
	if force && !ownedTOML(lines, e) {
		return body, nil // a server of the user named mai replaced the table of mai: it stays
	}
	if (!same || !e.marked || len(e.subs) > 0) && !force {
		current := strings.TrimLeft(string(text), "\r\n")
		for _, s := range e.subs {
			current += string(body[lines[s[0]].start:lines[s[1]].end])
		}
		return nil, changedErr(it, strings.TrimRight(current, "\r\n"))
	}
	return dropLines(body, lines, e.allLines(), e.mainLines()[0]), nil
}

// removeJSON removes the entry of the item from the JSON text before. Each other byte stays.
// When the entry was the last child of a container that mai connect made, the container goes
// too, from the deepest one up. A container that was empty before gets its bytes back. An
// entry that is gone gives no error, and the empty containers that mai made for it go. An
// entry with a different text gives *ErrChanged, unless force is set; with force, mai removes
// only its own hooks from a value that holds hooks of the user, and keeps a value that is not
// an entry of mai.
func removeJSON(before []byte, it manifest.Item, force bool) ([]byte, error) {
	if bom := []byte("\xef\xbb\xbf"); bytes.HasPrefix(before, bom) {
		out, err := removeJSON(before[len(bom):], it, force)
		if err != nil {
			return nil, err
		}
		return append(append([]byte(nil), bom...), out...), nil
	}
	if len(bytes.TrimSpace(before)) == 0 {
		return before, nil
	}
	root, err := scanJSON(before)
	if err != nil {
		return nil, fmt.Errorf("connect: %s is not plain JSON (comments are not supported): %w", it.Path, err)
	}
	keys := splitPointer(it.Pointer)
	if len(keys) == 0 {
		return nil, errors.New("connect: the item has no pointer")
	}
	rec := loadRecord(it)
	if rec.Target != "" && linkTarget(it.Path) != rec.Target {
		// The path names a different file now. A key that mai writes only when it is absent
		// ("version") stays in that file. The notes of mai about containers apply only when the
		// file holds the entry of mai as mai wrote it: the user moved the file of mai into a
		// dotfiles folder and linked it (stow --adopt).
		if rec.IfAbsent || it.Pointer == "/version" {
			return before, nil
		}
		if !entryPresent(before, it) {
			rec.Created, rec.Empty, rec.Blank = nil, nil, nil
		}
	}
	made := map[string]bool{}
	for _, p := range rec.Created {
		made[p] = true
	}
	inner := func(pointer string) string { return rec.Empty[pointer] }
	finish := func(out []byte) []byte {
		// Each empty container that mai made in the file goes, also one that is not on the
		// path of this entry, for example "hooks" after the remove of "version".
		out = pruneAllMade(out, made, inner)
		// A file that held only white space gets that text back when no member stays.
		if rec.Blank != nil {
			if n, err := scanJSON(out); err == nil && n.kind == '{' && len(n.members) == 0 {
				return []byte(*rec.Blank)
			}
		}
		return out
	}
	appendItem := keys[len(keys)-1] == "-"
	if appendItem {
		keys = keys[:len(keys)-1]
	}
	// gone is the result for an entry that is not there: the empty containers that mai made
	// for it go.
	gone := func() ([]byte, error) { return finish(pruneMade(before, keys, made, inner)), nil }
	// nodes[i] is the object that holds keys[i] at idx[i]; nodes[len(keys)] is the value.
	nodes, idx := []*jnode{root}, []int{}
	for _, k := range keys {
		cur := nodes[len(nodes)-1]
		if cur.kind != '{' {
			return before, nil
		}
		i, v := cur.member(k)
		if v == nil {
			return gone()
		}
		nodes, idx = append(nodes, v), append(idx, i)
	}
	target := nodes[len(nodes)-1]
	if appendItem {
		if target.kind != '[' {
			return nil, changedErr(it, string(before[target.start:target.end]))
		}
		found := -1
		for i, e := range target.elems {
			if hashOf(canonical(before, e)) == it.Hash {
				found = i
				break
			}
		}
		if found < 0 {
			// The entry is not there in the form that mai wrote. Find the hook of mai for
			// the app and the event of the item.
			event := keys[len(keys)-1]
			for i, e := range target.elems {
				if groupApps[it.Name] {
					group, hook := maiHookInGroup(before, e, it.Name, event)
					if hook == nil {
						continue
					}
					if !force {
						return nil, changedErr(it, string(before[e.start:e.end]))
					}
					if !allMaiHooks(before, group) {
						gi := 0
						for j, h := range group.elems {
							if h == hook {
								gi = j
							}
						}
						return finish(removeChild(before, group, gi, "")), nil
					}
					found = i
					break
				}
				if hasMaiHook(before, e, it.Name, event) {
					if !force {
						return nil, changedErr(it, string(before[e.start:e.end]))
					}
					found = i
					break
				}
			}
			if found < 0 {
				return gone() // the user removed the entry
			}
		}
		if len(target.elems) > 1 || !made[pointerOf(keys)] {
			return finish(removeChild(before, target, found, inner(pointerOf(keys)))), nil
		}
		// The array has only this element, and mai made the array: remove the array.
	} else if hashOf(canonical(before, target)) != it.Hash {
		if !force {
			return nil, changedErr(it, string(before[target.start:target.end]))
		}
		switch {
		case isMaiMCP(before, target):
			// An MCP entry of mai: the keys that the user added in it go with it.
		case hasAnyMaiHook(before, target):
			// A value with hooks of mai and of the user, such as the hook set "mai" of
			// Antigravity of 3.0.0: only the hooks of mai go.
			out := removeMaiHooks(before, keys)
			if n, err := scanJSON(out); err == nil {
				if v := nodeAt(n, keys); v != nil && hasValueText(out, v) {
					return finish(out), nil
				}
			}
			before = out
			root, _ = scanJSON(before)
			nodes, idx = []*jnode{root}, []int{}
			for _, k := range keys {
				i, v := nodes[len(nodes)-1].member(k)
				nodes, idx = append(nodes, v), append(idx, i)
			}
		default:
			return before, nil // a value of the user replaced the entry of mai: it stays
		}
	}
	// An entry that mai writes only into a file with no such key, such as "version", stays when
	// the file holds other content, because that content needs it. "/version" of 3.0.0 has no
	// mark in its record.
	if !appendItem && (rec.IfAbsent || it.Pointer == "/version") && !rec.HadContent && hasUserContent(before, nodes[len(nodes)-2], keys[len(keys)-1]) {
		return nil, ErrKept
	}
	// Remove the member keys[level] of nodes[level]. When it is the only member of an object
	// that mai made, remove that object from its parent instead.
	level := len(keys) - 1
	for level > 0 && len(nodes[level].members) == 1 && made[pointerOf(keys[:level])] {
		level--
	}
	return finish(removeChild(before, nodes[level], idx[level], inner(pointerOf(keys[:level])))), nil
}

// nodeAt follows the keys from n, or returns nil.
func nodeAt(n *jnode, keys []string) *jnode {
	for _, k := range keys {
		if n == nil || n.kind != '{' {
			return nil
		}
		_, n = n.member(k)
	}
	return n
}

// pruneMade removes the containers on the path of keys that mai made and that are empty, from
// the deepest one up.
func pruneMade(b []byte, keys []string, made map[string]bool, inner func(string) string) []byte {
	for level := len(keys); level > 0; level-- {
		root, err := scanJSON(b)
		if err != nil {
			return b
		}
		parent := nodeAt(root, keys[:level-1])
		if parent == nil || parent.kind != '{' {
			continue
		}
		i, v := parent.member(keys[level-1])
		if v == nil {
			continue
		}
		if !made[pointerOf(keys[:level])] || len(v.members) > 0 || len(v.elems) > 0 || (v.kind != '{' && v.kind != '[') {
			return b
		}
		b = removeChild(b, parent, i, inner(pointerOf(keys[:level-1])))
	}
	return b
}

// pruneAllMade removes each container in b that mai made and that is empty, until none stays.
func pruneAllMade(b []byte, made map[string]bool, inner func(string) string) []byte {
	for range 1000 {
		root, err := scanJSON(b)
		if err != nil || root.kind != '{' {
			return b
		}
		parent, i, pointer := emptyMade(root, "", made)
		if parent == nil {
			return b
		}
		b = removeChild(b, parent, i, inner(pointer))
	}
	return b
}

// emptyMade returns an object that holds an empty container that mai made, the index of that
// member, and the pointer of the object, or nil. It looks at the deepest containers first.
func emptyMade(n *jnode, pointer string, made map[string]bool) (*jnode, int, string) {
	for i, m := range n.members {
		p := pointer + "/" + escapeKey(m.key)
		if m.val.kind == '{' {
			if parent, j, pp := emptyMade(m.val, p, made); parent != nil {
				return parent, j, pp
			}
		}
		if made[p] && (m.val.kind == '{' || m.val.kind == '[') && len(m.val.members) == 0 && len(m.val.elems) == 0 {
			return n, i, pointer
		}
	}
	return nil, 0, ""
}

// removeMaiHooks removes each hook of mai (an object with a command of mai, or a string of
// mai) from the arrays in the value at keys.
func removeMaiHooks(b []byte, keys []string) []byte {
	for range 1000 {
		root, err := scanJSON(b)
		if err != nil {
			return b
		}
		v := nodeAt(root, keys)
		if v == nil {
			return b
		}
		arr, i := findMaiHook(b, v)
		if arr == nil {
			return b
		}
		b = removeChild(b, arr, i, "")
	}
	return b
}

// findMaiHook returns an array in n and the index of a hook of mai in it, or nil.
func findMaiHook(b []byte, n *jnode) (*jnode, int) {
	for i, e := range n.elems {
		if hasMaiHook(b, e, "", "") && !hasUserHook(b, e) {
			return n, i
		}
	}
	for _, e := range n.elems {
		if a, i := findMaiHook(b, e); a != nil {
			return a, i
		}
	}
	for _, m := range n.members {
		if a, i := findMaiHook(b, m.val); a != nil {
			return a, i
		}
	}
	return nil, 0
}

// hasUserHook reports if the value n holds a command that is not a hook command of mai.
func hasUserHook(b []byte, n *jnode) bool {
	v, err := parseJSON(b[n.start:n.end])
	if err != nil {
		return true
	}
	o, ok := v.(*object)
	if !ok {
		return false
	}
	for _, s := range stringsIn(o.vals["hooks"]) {
		if !isMaiHook(s, "", "") {
			return true
		}
	}
	return false
}

// hasValueText reports if the value n of the text b holds a value that is not an object or an
// array.
func hasValueText(b []byte, n *jnode) bool {
	v, err := parseJSON(b[n.start:n.end])
	return err != nil || hasValue(v)
}

// hasUserContent reports if the object n holds a member other than skip with a value in it
// (not only empty objects and arrays). mai removes "version" after its other entries, thus
// each hook that stays, also a changed hook of mai, needs the key.
func hasUserContent(b []byte, n *jnode, skip string) bool {
	for _, m := range n.members {
		if m.key == skip {
			continue
		}
		v, err := parseJSON(b[m.val.start:m.val.end])
		if err != nil || hasValue(v) {
			return true
		}
	}
	return false
}

// hasValue reports if v holds a value that is not an object or an array.
func hasValue(v any) bool {
	switch x := v.(type) {
	case *object:
		for _, k := range x.keys {
			if hasValue(x.vals[k]) {
				return true
			}
		}
		return false
	case []any:
		for _, e := range x {
			if hasValue(e) {
				return true
			}
		}
		return false
	}
	return true
}

// isMaiMCP reports if the value n of the text b is an MCP entry of mai: an object whose
// command is a mai binary and whose args start with "mcp", as mai connect writes it.
func isMaiMCP(b []byte, n *jnode) bool {
	v, err := parseJSON(b[n.start:n.end])
	if err != nil {
		return false
	}
	o, ok := v.(*object)
	if !ok {
		return false
	}
	cmd, _ := o.vals["command"].(string)
	args, _ := o.vals["args"].([]any)
	return isMaiProgram(cmd) && len(args) > 0 && args[0] == "mcp"
}

// hasAnyMaiHook reports if the value n of the text b holds a hook command of mai.
func hasAnyMaiHook(b []byte, n *jnode) bool { return hasMaiHook(b, n, "", "") }

// hasMaiHook reports if the value n of the text b holds the hook command of mai for format
// and event.
func hasMaiHook(b []byte, n *jnode, format, event string) bool {
	v, err := parseJSON(b[n.start:n.end])
	if err != nil {
		return false
	}
	for _, s := range stringsIn(v) {
		if isMaiHook(s, format, event) {
			return true
		}
	}
	return false
}

// RemoveCreatedFile removes a file that mai connect made, when it holds no content now. A
// file with content stays, and the result is false.
func RemoveCreatedFile(it manifest.Item) (bool, error) {
	// For a symbolic link of the user, the file that the link names is the file that mai made.
	// When the link names a different file now (the user made the link after mai connect),
	// that file is not of mai, and it stays.
	path := linkTarget(it.Path)
	if it.Target != "" && path != it.Target {
		return false, nil
	}
	if it.Target == "" {
		if st, err := os.Lstat(it.Path); err == nil && st.Mode()&os.ModeSymlink != 0 {
			return false, nil // a record of 3.0.0 has no target: keep the file of a link
		}
	}
	b, existed, err := readFile(path)
	if err != nil {
		return false, err
	}
	if existed {
		t := bytes.TrimSpace(b)
		if len(t) != 0 && string(t) != "{}" {
			return false, nil
		}
		if err := os.Remove(path); err != nil {
			return false, err
		}
	}
	// The folders that mai made for the file go when they are empty. A link of the user in the
	// place of a folder stays.
	for _, d := range it.Dirs {
		if st, err := os.Lstat(d); err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			continue
		}
		_ = os.Remove(d) // a folder with other files stays
	}
	return true, nil
}

// entryPresent reports if the JSON text b holds the entry of the item as mai wrote it.
func entryPresent(b []byte, it manifest.Item) bool {
	if bom := []byte("\xef\xbb\xbf"); bytes.HasPrefix(b, bom) {
		b = b[len(bom):]
	}
	root, err := scanJSON(b)
	if err != nil || root.kind != '{' {
		return false
	}
	keys := splitPointer(it.Pointer)
	if len(keys) == 0 {
		return false
	}
	appendItem := keys[len(keys)-1] == "-"
	if appendItem {
		keys = keys[:len(keys)-1]
	}
	v := nodeAt(root, keys)
	if v == nil {
		return false
	}
	if !appendItem {
		return hashOf(canonical(b, v)) == it.Hash
	}
	for _, e := range v.elems {
		if hashOf(canonical(b, e)) == it.Hash {
			return true
		}
	}
	return false
}

// Disconnect removes each entry of app in m, and each file that mai connect made for app and
// that is empty now. It removes the records of the removed items. It continues after an
// *ErrChanged and returns all of them at the end. An entry that other content needs
// (ErrKept) stays with its record, and gives no error.
func Disconnect(app string, m *manifest.Manifest) error { return disconnect(app, m, false) }

// DisconnectForce is Disconnect with ForceRemoveEntry for each entry.
func DisconnectForce(app string, m *manifest.Manifest) error { return disconnect(app, m, true) }

func disconnect(app string, m *manifest.Manifest, force bool) error {
	var errs []error
	for _, it := range RemovalOrder(m.Of(manifest.AppEntry)) {
		if it.Name != app {
			continue
		}
		err := remove(it, force)
		switch {
		case errors.Is(err, ErrKept):
			continue
		case err != nil:
			errs = append(errs, err)
			continue
		}
		if err := m.Remove(it); err != nil {
			return err
		}
	}
	for _, it := range m.Of(manifest.File) {
		if it.Name != app {
			continue
		}
		gone, err := RemoveCreatedFile(it)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if gone {
			if err := m.Remove(it); err != nil {
				return err
			}
		}
	}
	return errors.Join(errs...)
}

func pointerOf(keys []string) string {
	var b strings.Builder
	for _, k := range keys {
		b.WriteString("/" + escapeKey(k))
	}
	return b.String()
}

// RemovalOrder returns the items with each "version" entry at the end, because a hook that
// stays needs it. The sequence of the other items stays.
func RemovalOrder(items []manifest.Item) []manifest.Item {
	out := append([]manifest.Item(nil), items...)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Pointer != "/version" && out[j].Pointer == "/version"
	})
	return out
}
