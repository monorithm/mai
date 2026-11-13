// Package manifest records each item that the CLI makes, so that mai uninstall can remove all
// of them and then do a check that each one is gone. The manifest holds no secret.
package manifest

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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
}

// Manifest is the list of items of one instance.
type Manifest struct {
	mu       sync.Mutex
	path     string
	Instance string `json:"instance"`
	Items    []Item `json:"items"`
}

// FileName is the name of the manifest in the state folder.
const FileName = "manifest.json"

// Load reads the manifest at path. A missing file gives an empty manifest.
func Load(path, instance string) (*Manifest, error) {
	m := &Manifest{path: path, Instance: instance}
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
	return m, nil
}

func same(a, b Item) bool {
	return a.Type == b.Type && a.Name == b.Name && a.Path == b.Path && a.Pointer == b.Pointer
}

// Add records an item, or replaces the record of the same item, and saves the file.
func (m *Manifest) Add(it Item) error {
	m.mu.Lock()
	defer m.mu.Unlock()
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

// Of returns the items of one type.
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
