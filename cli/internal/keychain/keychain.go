// Package keychain reads and deletes the legacy items of mai in the system keychain: the
// macOS Keychain, the Windows Credential Manager, or the Linux Secret Service.
//
// mai 3.0 and 3.1 kept the owner key and the agent keys in the keychain. The CLI now keeps
// them in its key file. This package is only for the move of the legacy items to the key
// file and for the removal of the legacy items. It never writes an item.
//
// The service is "dev.mai". The accounts are "owner@<instance>" and
// "agent:<name>@<instance>".
package keychain

import (
	"errors"
	"sort"
	"sync"
)

// Service is the keychain service of each legacy item of mai.
const Service = "dev.mai"

// ErrNotFound means that the item does not exist.
var ErrNotFound = errors.New("keychain: item not found")

// ErrUnavailable means that this system has no keychain that the CLI can use, for example a
// Linux host with no Secret Service. Such a keychain holds no item of mai.
var ErrUnavailable = errors.New("keychain: not available")

// Store gives access to the legacy items of Service. An implementation never prints or logs
// a secret.
type Store interface {
	// Get reads the secret of the item of account.
	Get(account string) (string, error)
	// Has tells if the item of account exists. When it can, it reads no secret and shows no
	// prompt.
	Has(account string) (bool, error)
	// Delete removes the item of account. It gives ErrNotFound when the item does not exist.
	Delete(account string) error
}

// System returns the keychain of the OS, with the items of Service.
func System() Store { return system(Service) }

// Memory is a Store in memory, for tests. When Err is not nil, each call gives Err.
type Memory struct {
	mu    sync.Mutex
	items map[string]string
	Err   error
}

// NewMemory returns an empty Memory store.
func NewMemory() *Memory { return &Memory{items: map[string]string{}} }

// Put adds or replaces the item of account. Only a test writes an item.
func (m *Memory) Put(account, secret string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items[account] = secret
}

// Accounts gives the account of each item, sorted.
func (m *Memory) Accounts() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.items))
	for a := range m.items {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

func (m *Memory) Get(account string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Err != nil {
		return "", m.Err
	}
	s, ok := m.items[account]
	if !ok {
		return "", ErrNotFound
	}
	return s, nil
}

func (m *Memory) Has(account string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Err != nil {
		return false, m.Err
	}
	_, ok := m.items[account]
	return ok, nil
}

func (m *Memory) Delete(account string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Err != nil {
		return m.Err
	}
	if _, ok := m.items[account]; !ok {
		return ErrNotFound
	}
	delete(m.items, account)
	return nil
}
