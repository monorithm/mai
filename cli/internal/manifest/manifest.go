// Package manifest records each item that the CLI makes, so that mai uninstall can remove all
// of them and then do a check that each one is gone. The manifest holds no secret.
//
// One manifest in the state folder holds the items of each instance of that folder. Each item
// records its instance. Thus mai uninstall of one instance removes only the items of that
// instance.
package manifest

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Types of items.
const (
	Container   = "container"
	Volume      = "volume"
	Image       = "image"
	KeychainKey = "keychain"  // a legacy keychain item of mai 3.0 or 3.1; only mai uninstall adds a record, for an item that stays
	AppEntry    = "app-entry" // a change that mai connect wrote in a file of an app
	Folder      = "folder"
	File        = "file"
)

// DataFolder is the name of the record of the host data folder of an instance, a folder.
const DataFolder = "data"

// Item is one thing that mai made.
type Item struct {
	Type    string `json:"type"`
	Name    string `json:"name"`              // container, volume, or account of a key; the app for an app entry
	ID      string `json:"id,omitempty"`      // engine ID
	Path    string `json:"path,omitempty"`    // file of an app, folder, or file
	Pointer string `json:"pointer,omitempty"` // JSON pointer of the entry in the file of an app
	Hash    string `json:"hash,omitempty"`    // SHA-256 of the content that mai wrote
	// For a file that mai connect made: the real file at that time (the file that a symbolic
	// link named), and each folder that mai made for it, the deepest first.
	Target string   `json:"target,omitempty"`
	Dirs   []string `json:"dirs,omitempty"`
	// Instance is the instance of the item. An item of mai 3.0 or 3.1 has no instance. Load
	// then gives it the instance of its name, or the instance of the file (instanceOf). A data
	// folder, an app entry, and a file of an app of such a record stay with no instance
	// (Unassigned).
	Instance string `json:"instance,omitempty"`
}

// Manifest is the list of the items of each instance of one state folder.
type Manifest struct {
	mu       sync.Mutex
	path     string
	instance string // the instance of the command; Add records each item with no instance for it
	// Instance is the instance of the command that made the file. mai 3.0 and 3.1 wrote no
	// instance in an item, thus an item with no instance and with no instance in its name is
	// of this instance.
	Instance string `json:"instance"`
	Items    []Item `json:"items"`
}

// FileName is the name of the manifest in the state folder.
const FileName = "manifest.json"

// Load reads the manifest at path for a command of the instance. A missing file gives an
// empty manifest.
func Load(path, instance string) (*Manifest, error) {
	m := &Manifest{path: path, instance: instance, Instance: instance}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, m); err != nil {
		return nil, err
	}
	m.path = path
	if m.Instance == "" {
		m.Instance = instance
	}
	for i := range m.Items {
		if m.Items[i].Instance == "" && !legacyData(m.Items[i]) && !legacyApp(m.Items[i]) {
			m.Items[i].Instance = instanceOf(m.Items[i], m.Instance)
		}
	}
	return m, nil
}

// legacyData tells if a record with no instance is of a data folder. The instance of the file
// of mai 3.0 or 3.1 is the instance of the first command that wrote the file. A data folder can
// be of a different instance, for example of "mai start --instance mai-dev --data-dir PATH".
// Thus the record stays with no instance, and the caller finds the instance, for example from
// the mounts of the containers.
func legacyData(it Item) bool { return it.Type == Folder && it.Name == DataFolder }

// legacyApp tells if a record with no instance is of an app entry or of a file of an app. The
// entry names no instance, thus its bridge uses the instance of config.json at each start, and
// not the instance of the file. Thus the record stays with no instance. The uninstall keeps
// the entry while an instance stays.
func legacyApp(it Item) bool { return it.Type == AppEntry || it.Type == File }

// Unassigned gives each record with no instance: a data folder, an app entry, or a file of an
// app of mai 3.0 or 3.1.
func (m *Manifest) Unassigned() []Item {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Item
	for _, x := range m.Items {
		if x.Instance == "" {
			out = append(out, x)
		}
	}
	return out
}

// Assign gives the record of the item the instance, in memory. The next save writes it.
func (m *Manifest) Assign(it Item, instance string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.Items {
		if same(m.Items[i], it) {
			m.Items[i].Instance = instance
		}
	}
}

// The ends of the names of the volumes of an instance, after the name of the instance. They
// agree with engine.DataVolume and engine.SpoolVolume.
const (
	dataVolumeEnd  = "3-data"
	spoolVolumeEnd = "3-spool"
)

// instanceOf gives the instance of an item from its name, or other when the name does not
// tell the instance. The name of a container is the name of its instance. The name of a volume
// is the instance and "3-data" or "3-spool". The account of a keychain item ends with "@" and
// the instance.
func instanceOf(it Item, other string) string {
	switch it.Type {
	case Container:
		if it.Name != "" {
			return it.Name
		}
	case Volume:
		for _, end := range []string{dataVolumeEnd, spoolVolumeEnd} {
			if inst, ok := strings.CutSuffix(it.Name, end); ok && inst != "" {
				return inst
			}
		}
	case KeychainKey:
		if i := strings.LastIndexByte(it.Name, '@'); i >= 0 && i < len(it.Name)-1 {
			return it.Name[i+1:]
		}
	}
	return other
}

// same tells if a and b are records of the same item. The instance is not part of the
// comparison: one item has one record, and the last command that records it sets its instance.
func same(a, b Item) bool {
	return a.Type == b.Type && a.Name == b.Name && a.Path == b.Path && a.Pointer == b.Pointer
}

// Add records an item, or replaces the record of the same item, and saves the file. An item
// with no instance gets the instance of its name, or the instance of the command.
func (m *Manifest) Add(it Item) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if it.Instance == "" {
		it.Instance = instanceOf(it, m.instance)
	}
	for i := range m.Items {
		if same(m.Items[i], it) {
			m.Items[i] = it
			return m.save()
		}
	}
	m.Items = append(m.Items, it)
	return m.save()
}

// Remove deletes the record of an item and saves the file.
func (m *Manifest) Remove(it Item) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.Items[:0]
	for _, x := range m.Items {
		if !same(x, it) {
			out = append(out, x)
		}
	}
	m.Items = out
	return m.save()
}

// Of returns the items of one type, of all instances.
func (m *Manifest) Of(typ string) []Item {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Item
	for _, x := range m.Items {
		if x.Type == typ {
			out = append(out, x)
		}
	}
	return out
}

// OfInstance returns the items of one type of the instance.
func (m *Manifest) OfInstance(instance, typ string) []Item {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Item
	for _, x := range m.Items {
		if x.Type == typ && x.Instance == instance {
			out = append(out, x)
		}
	}
	return out
}

// Instances returns each instance that has an item, sorted.
func (m *Manifest) Instances() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := map[string]bool{}
	var out []string
	for _, x := range m.Items {
		if x.Instance != "" && !seen[x.Instance] {
			seen[x.Instance] = true
			out = append(out, x.Instance)
		}
	}
	sort.Strings(out)
	return out
}

// Path is the location of the manifest file.
func (m *Manifest) Path() string { return m.path }

func (m *Manifest) save() error {
	if err := os.MkdirAll(filepath.Dir(m.path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}
