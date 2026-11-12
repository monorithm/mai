package connect

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/monorithm/mai/cli/internal/manifest"
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

func removeTOML(body []byte, it manifest.Item, force bool) ([]byte, error) {
	start := bytes.Index(body, []byte("\n"+tomlBegin))
	if start < 0 {
		return body, nil
	}
	rest := body[start:]
	end := bytes.Index(rest, []byte(tomlEnd))
	if end < 0 {
		return nil, changedErr(it, string(rest))
	}
	block := rest[:end+len(tomlEnd)]
	if hashOf(block) != it.Hash && !force {
		return nil, changedErr(it, strings.TrimSuffix(string(block), "\n"))
	}
	out := append([]byte(nil), body[:start]...)
	return append(out, rest[end+len(tomlEnd):]...), nil
}

func removeJSON(before []byte, it manifest.Item, force bool) ([]byte, error) {
	doc, err := loadDoc(it.Path, before)
	if err != nil {
		return nil, err
	}
	keys := splitPointer(it.Pointer)
	if len(keys) == 0 {
		return nil, errors.New("connect: the item has no pointer")
	}
	made := map[string]bool{}
	for _, p := range loadRecord(it).Created {
		made[p] = true
	}
	appendItem := keys[len(keys)-1] == "-"
	if appendItem {
		keys = keys[:len(keys)-1]
	}
	parents := []*object{doc}
	for _, k := range keys[:len(keys)-1] {
		v, ok := parents[len(parents)-1].get(k)
		o, isObj := v.(*object)
		if !ok || !isObj {
			return before, nil
		}
		parents = append(parents, o)
	}
	parent := parents[len(parents)-1]
	last := keys[len(keys)-1]
	v, ok := parent.get(last)
	if !ok {
		return before, nil
	}
	if appendItem {
		arr, isArr := v.([]any)
		if !isArr {
			return nil, changedErr(it, string(encodeJSON(v, "  ")))
		}
		out := arr[:0:0]
		found := false
		for _, e := range arr {
			if !found && hashOf(encodeJSON(e, "")) == it.Hash {
				found = true
				continue
			}
			if force && !found && strings.Contains(string(encodeJSON(e, "")), `"mai hook `) {
				found = true
				continue
			}
			out = append(out, e)
		}
		if !found {
			return nil, changedErr(it, string(encodeJSON(arr, "  ")))
		}
		if len(out) > 0 || !made[pointerOf(keys)] {
			parent.set(last, out)
			return renderDoc(before, doc), nil
		}
	} else if hashOf(encodeJSON(v, "")) != it.Hash && !force {
		return nil, changedErr(it, string(encodeJSON(v, "  ")))
	}
	parent.del(last)
	// Remove each object that mai made and that is empty now, from the deepest one up.
	for i := len(parents) - 1; i > 0; i-- {
		if len(parents[i].keys) > 0 || !made[pointerOf(keys[:i])] {
			break
		}
		parents[i-1].del(keys[i-1])
	}
	return renderDoc(before, doc), nil
}

// RemoveCreatedFile removes a file that mai connect made, when it holds no content now. A
// file with content stays, and the result is false.
func RemoveCreatedFile(it manifest.Item) (bool, error) {
	b, existed, err := readFile(it.Path)
	if err != nil || !existed {
		return !existed, err
	}
	t := bytes.TrimSpace(b)
	if len(t) == 0 || string(t) == "{}" {
		return true, os.Remove(it.Path)
	}
	return false, nil
}

// Disconnect removes each entry of app in m, and each file that mai connect made for app and
// that is empty now. It removes the records of the removed items. It continues after an
// *ErrChanged and returns all of them at the end.
func Disconnect(app string, m *manifest.Manifest) error {
	var errs []error
	for _, it := range m.Of(manifest.AppEntry) {
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
