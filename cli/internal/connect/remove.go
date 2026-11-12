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
		return err
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
		return body, nil
	}
	text, with300 := e.mainText(body, lines, layoutOf(body).nl)
	same := hashOf(text) == it.Hash || (with300 != nil && hashOf(with300) == it.Hash)
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
// entry that is gone gives no error. An entry with a different text gives *ErrChanged, unless
// force is set; in a hook group that holds hooks of the user, force removes only the hook
// object of mai.
func removeJSON(before []byte, it manifest.Item, force bool) ([]byte, error) {
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
	made := map[string]bool{}
	for _, p := range rec.Created {
		made[p] = true
	}
	inner := func(pointer string) string { return rec.Empty[pointer] }
	finish := func(out []byte) []byte {
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
	// nodes[i] is the object that holds keys[i] at idx[i]; nodes[len(keys)] is the value.
	nodes, idx := []*jnode{root}, []int{}
	for _, k := range keys {
		cur := nodes[len(nodes)-1]
		if cur.kind != '{' {
			return before, nil
		}
		i, v := cur.member(k)
		if v == nil {
			return before, nil
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
					if !onlyMaiGroup(e, group) {
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
				return before, nil // the user removed the entry
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
		if !ownedByMai(before, target) {
			return before, nil // a value of the user replaced the entry of mai: it stays
		}
	}
	// An entry that mai writes only into a file with no such key, such as "version", stays when
	// the file holds other content, because that content needs it. "/version" of 3.0.0 has no
	// mark in its record.
	if !appendItem && (rec.IfAbsent || it.Pointer == "/version") && hasUserContent(before, nodes[len(nodes)-2], keys[len(keys)-1]) {
		return before, nil
	}
	// Remove the member keys[level] of nodes[level]. When it is the only member of an object
	// that mai made, remove that object from its parent instead.
	level := len(keys) - 1
	for level > 0 && len(nodes[level].members) == 1 && made[pointerOf(keys[:level])] {
		level--
	}
	return finish(removeChild(before, nodes[level], idx[level], inner(pointerOf(keys[:level])))), nil
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

// ownedByMai reports if the value n of the text b is an entry of mai: an object whose command
// is a mai binary, or a value that holds a hook command of mai.
func ownedByMai(b []byte, n *jnode) bool {
	v, err := parseJSON(b[n.start:n.end])
	if err != nil {
		return false
	}
	if o, ok := v.(*object); ok {
		if cmd, ok := o.vals["command"].(string); ok && isMaiProgram(cmd) {
			return true
		}
	}
	for _, s := range stringsIn(v) {
		if isMaiHook(s, "", "") {
			return true
		}
	}
	return false
}

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
	path := linkTarget(it.Path)
	b, existed, err := readFile(path)
	if err != nil || !existed {
		return !existed, err
	}
	t := bytes.TrimSpace(b)
	if len(t) == 0 || string(t) == "{}" {
		return true, os.Remove(path)
	}
	return false, nil
}

// Disconnect removes each entry of app in m, and each file that mai connect made for app and
// that is empty now. It removes the records of the removed items. It continues after an
// *ErrChanged and returns all of them at the end.
func Disconnect(app string, m *manifest.Manifest) error {
	var errs []error
	for _, it := range RemovalOrder(m.Of(manifest.AppEntry)) {
		if it.Name != app {
			continue
		}
		if err := RemoveEntry(it); err != nil {
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
