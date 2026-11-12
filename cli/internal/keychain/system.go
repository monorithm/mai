package keychain

import (
	"errors"

	"github.com/zalando/go-keyring"
)

// goKeyring is the Store of zalando/go-keyring. On macOS it uses /usr/bin/security, thus
// each program of the user can read the items. The darwin build replaces it with the
// Security framework and an access list for mai only.
type goKeyring struct{}

func (goKeyring) Set(service, account, secret string) error {
	return keyring.Set(service, account, secret)
}

func (goKeyring) Get(service, account string) (string, error) {
	s, err := keyring.Get(service, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotFound
	}
	return s, err
}

func (goKeyring) Delete(service, account string) error {
	err := keyring.Delete(service, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return ErrNotFound
	}
	return err
}
