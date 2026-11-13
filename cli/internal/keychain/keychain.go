// Package keychain keeps the keys of mai in the system keychain: the macOS Keychain, the
// Windows Credential Manager, or the Linux Secret Service.
//
// Service is "dev.mai". The accounts are "owner@<instance>" and "agent:<name>@<instance>".
package keychain

import "errors"

// Service is the keychain service of each item that mai writes.
const Service = "dev.mai"

// ErrNotFound means that the item does not exist.
var ErrNotFound = errors.New("keychain: item not found")

// Store reads and writes secrets. An implementation never prints or logs a secret.
type Store interface {
	Set(service, account, secret string) error
	Get(service, account string) (string, error)
	Delete(service, account string) error
}

// System returns the keychain of the OS. In a test build, the variable
// MAI_TEST_KEYCHAIN_SERVICE can move each item of Service to a test service (movedService).
// A release build always uses Service.
func System() Store {
	s := system()
	if t := movedService(); t != "" {
		return testStore{Store: s, service: t}
	}
	return s
}

// testStore keeps each item of Service in the test service. Thus a test of the mai binary
// does not change the items of the user.
type testStore struct {
	Store
	service string
}

func (t testStore) name(service string) string {
	if service == Service {
		return t.service
	}
	return service
}

func (t testStore) Set(service, account, secret string) error {
	return t.Store.Set(t.name(service), account, secret)
}

func (t testStore) Get(service, account string) (string, error) {
	return t.Store.Get(t.name(service), account)
}

func (t testStore) Delete(service, account string) error {
	return t.Store.Delete(t.name(service), account)
}

// OwnerAccount is the account of the owner key of an instance.
func OwnerAccount(instance string) string { return "owner@" + instance }

// AgentAccount is the account of the key of one agent of an instance.
func AgentAccount(instance, agent string) string { return "agent:" + agent + "@" + instance }

// Memory is a Store in memory, for tests.
type Memory struct{ items map[string]string }

// NewMemory returns an empty Memory store.
func NewMemory() *Memory { return &Memory{items: map[string]string{}} }

func (m *Memory) Set(service, account, secret string) error {
	m.items[service+"\x00"+account] = secret
	return nil
}

func (m *Memory) Get(service, account string) (string, error) {
	s, ok := m.items[service+"\x00"+account]
	if !ok {
		return "", ErrNotFound
	}
	return s, nil
}

func (m *Memory) Delete(service, account string) error {
	k := service + "\x00" + account
	if _, ok := m.items[k]; !ok {
		return ErrNotFound
	}
	delete(m.items, k)
	return nil
}
