// Package keys keeps the owner key and the agent keys of each instance in the key file of
// the CLI, keys.json in the state folder.
//
// The file has mode 0600, in a folder with mode 0700. Its accounts have the names of the
// legacy keychain items: "owner@<instance>" and "agent:<name>@<instance>". Each write is
// atomic: a temporary file in the same folder, an fsync, and a rename. A lock on the file
// keys.json.lock lets one process at a time read or change the file. Thus two CLIs or
// bridges at the same time do not lose a key.
//
// mai 3.0 and 3.1 kept the keys in the system keychain. When the key file has no key for an
// account, Get reads the legacy item, writes the key to the key file, and then deletes the
// legacy item.
//
// On a self-run instance, a program that can use the Docker socket controls mai fully. Thus
// the key file gives the same protection as the keychain, with no prompt.
package keys

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/monorithm/mai/cli/v3/internal/keychain"
	"github.com/monorithm/mai/cli/v3/internal/paths"
)

// FileName is the name of the key file in the state folder.
const FileName = "keys.json"

// ErrNotFound means that the key file and the legacy keychain have no key for the account.
var ErrNotFound = errors.New("keys: no key for the account")

// OwnerAccount is the account of the owner key of an instance.
func OwnerAccount(instance string) string { return "owner@" + instance }

// AgentAccount is the account of the key of one agent of an instance.
func AgentAccount(instance, agent string) string { return "agent:" + agent + "@" + instance }

// InstanceOf gives the instance of an account: the text after the last "@". It gives ""
// for an account with no "@".
func InstanceOf(account string) string {
	i := strings.LastIndexByte(account, '@')
	if i < 0 {
		return ""
	}
	return account[i+1:]
}

// File is the key file of the CLI. Make it with Open or Default.
type File struct {
	path   string
	legacy keychain.Store // nil: no legacy keychain
}

// Open returns the key file in the folder dir. legacy is the keychain of mai 3.0 and 3.1, or
// nil for no move of legacy items.
func Open(dir string, legacy keychain.Store) *File {
	return &File{path: filepath.Join(dir, FileName), legacy: legacy}
}

// Default returns the key file in the state folder of the CLI, with the system keychain as
// the legacy keychain. With MAI_HOME, the key file has no legacy keychain, because the
// items of the keychain are of the home of the user and not of that folder.
func Default() (*File, error) {
	d, err := paths.Default()
	if err != nil {
		return nil, err
	}
	var legacy keychain.Store
	if !paths.Isolated() {
		legacy = keychain.System()
	}
	return Open(d.State, legacy), nil
}

// Path is the location of the key file.
func (f *File) Path() string { return f.path }

// HasLegacy tells if the key file has a legacy keychain.
func (f *File) HasLegacy() bool { return f.legacy != nil }

// LegacyError means that the key file has no key for the account, and that the read of its
// legacy keychain item failed, for example because the user refused the prompt of macOS.
// It is not ErrNotFound: the legacy item can hold the key.
type LegacyError struct {
	Account string
	Err     error
}

func (e *LegacyError) Error() string {
	return "keys: the key file has no key for " + e.Account + ", and the read of its keychain item failed: " + e.Err.Error()
}

func (e *LegacyError) Unwrap() error { return e.Err }

// ModeError means that a group or other users can read or change the key file. The CLI
// then refuses the file. The error tells the command that corrects the mode.
type ModeError struct {
	Path string
	Mode os.FileMode
}

func (e *ModeError) Error() string {
	return fmt.Sprintf("keys: the key file %s has the mode %04o, thus other users can read or change it. Run: chmod 600 %q",
		e.Path, uint32(e.Mode.Perm()), e.Path)
}

// Get gives the key of the account. When the key file has no key for the account, Get moves
// the legacy keychain item of the account to the key file. It gives ErrNotFound when no key
// exists, also when no keychain is available.
func (f *File) Get(account string) (string, error) {
	var key string
	var ok bool
	if err := f.view(func(d *doc) { key, ok = d.Keys[account] }); err != nil {
		return "", err
	}
	if ok {
		return key, nil
	}
	if f.legacy == nil {
		return "", ErrNotFound
	}
	return f.move(account)
}

// move reads the legacy item of the account, writes it to the key file, and then deletes the
// legacy item. When a different process wrote a key for the account in the meantime, that
// key stays, and move gives it. When the write fails, the legacy item stays.
func (f *File) move(account string) (string, error) {
	secret, err := f.legacy.Get(account)
	switch {
	case errors.Is(err, keychain.ErrNotFound), errors.Is(err, keychain.ErrUnavailable):
		return "", ErrNotFound
	case err != nil:
		return "", &LegacyError{Account: account, Err: err}
	case secret == "":
		return "", ErrNotFound
	}
	key := secret
	err = f.update(func(d *doc) bool {
		if k, ok := d.Keys[account]; ok {
			key = k
			return false
		}
		d.Keys[account] = secret
		return true
	})
	if err != nil {
		return "", fmt.Errorf("keys: move %s from the keychain to the key file: %w", account, err)
	}
	// The key is in the key file. When the delete fails, the legacy item stays, and
	// "mai doctor" tells about it.
	_ = f.DeleteLegacy(account)
	return key, nil
}

// Set writes the key of the account to the key file.
func (f *File) Set(account, key string) error {
	if account == "" || key == "" {
		return errors.New("keys: an account and a key are necessary")
	}
	return f.update(func(d *doc) bool {
		if d.Keys[account] == key {
			return false
		}
		d.Keys[account] = key
		return true
	})
}

// Delete removes the key of the account from the key file. It gives ErrNotFound when the key
// file has no key for the account. It does not change the legacy keychain.
func (f *File) Delete(account string) error {
	found := false
	err := f.update(func(d *doc) bool {
		_, found = d.Keys[account]
		delete(d.Keys, account)
		return found
	})
	if err == nil && !found {
		return ErrNotFound
	}
	return err
}

// Has tells if the key file has a key for the account. It does not read the legacy keychain.
func (f *File) Has(account string) (bool, error) {
	var ok bool
	err := f.view(func(d *doc) { _, ok = d.Keys[account] })
	return ok, err
}

// Accounts gives each account of the key file, sorted.
func (f *File) Accounts() ([]string, error) {
	var out []string
	err := f.view(func(d *doc) {
		for a := range d.Keys {
			out = append(out, a)
		}
	})
	sort.Strings(out)
	return out, err
}

// OfInstance gives each account of the instance in the key file, sorted.
func (f *File) OfInstance(instance string) ([]string, error) {
	all, err := f.Accounts()
	var out []string
	for _, a := range all {
		if InstanceOf(a) == instance {
			out = append(out, a)
		}
	}
	return out, err
}

// DeleteInstance removes each key of the instance from the key file, and gives the accounts
// that it removed, sorted. The keys of the other instances stay.
func (f *File) DeleteInstance(instance string) ([]string, error) {
	var gone []string
	err := f.update(func(d *doc) bool {
		for a := range d.Keys {
			if InstanceOf(a) == instance {
				gone = append(gone, a)
				delete(d.Keys, a)
			}
		}
		return len(gone) > 0
	})
	sort.Strings(gone)
	return gone, err
}

// Candidates gives each account of the instance that can have a legacy keychain item, sorted:
// the owner account, each account in recorded, and each account of the instance in the key
// file. It ignores an account of recorded with a different instance.
func (f *File) Candidates(instance string, recorded []string) ([]string, error) {
	set := map[string]bool{OwnerAccount(instance): true}
	for _, a := range recorded {
		if InstanceOf(a) == instance {
			set[a] = true
		}
	}
	inFile, err := f.OfInstance(instance)
	for _, a := range inFile {
		set[a] = true
	}
	out := make([]string, 0, len(set))
	for a := range set {
		out = append(out, a)
	}
	sort.Strings(out)
	return out, err
}

// LegacyItems gives each account of accounts that has a legacy keychain item, and reads no
// secret. When the legacy keychain is not available, the error is keychain.ErrUnavailable.
func (f *File) LegacyItems(accounts []string) ([]string, error) {
	if f.legacy == nil {
		return nil, nil
	}
	var out []string
	for _, a := range accounts {
		ok, err := f.legacy.Has(a)
		if err != nil {
			return out, err
		}
		if ok {
			out = append(out, a)
		}
	}
	return out, nil
}

// HasLegacyItem tells if the account has a legacy keychain item. It reads no secret.
func (f *File) HasLegacyItem(account string) (bool, error) {
	if f.legacy == nil {
		return false, nil
	}
	return f.legacy.Has(account)
}

// DeleteLegacy removes the legacy keychain item of the account. An item that does not exist
// is not an error.
func (f *File) DeleteLegacy(account string) error {
	if f.legacy == nil {
		return nil
	}
	if err := f.legacy.Delete(account); err != nil && !errors.Is(err, keychain.ErrNotFound) {
		return err
	}
	return nil
}

// MoveReport tells what MoveAll did.
type MoveReport struct {
	// Moved holds each account whose key went from the keychain to the key file.
	Moved []string
	// Cleaned holds each account whose legacy item MoveAll deleted, because the key file
	// had a key for it. The key of the key file is the newer key.
	Cleaned []string
	// Unavailable is true when no keychain is available. Then MoveAll moved no key.
	Unavailable bool
}

// MoveAll moves the legacy keychain item of each account to the key file, and deletes each
// legacy item whose account the key file has. The error joins the failure of each account.
func (f *File) MoveAll(accounts []string) (MoveReport, error) {
	var r MoveReport
	if f.legacy == nil {
		return r, nil
	}
	var errs []error
	for _, a := range accounts {
		ok, err := f.legacy.Has(a)
		if errors.Is(err, keychain.ErrUnavailable) {
			r.Unavailable = true
			return r, nil
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", a, err))
			continue
		}
		if !ok {
			continue
		}
		inFile, err := f.Has(a)
		if err != nil {
			return r, err
		}
		if inFile {
			if err := f.DeleteLegacy(a); err != nil {
				errs = append(errs, fmt.Errorf("delete the keychain item %s: %w", a, err))
				continue
			}
			r.Cleaned = append(r.Cleaned, a)
			continue
		}
		if _, err := f.move(a); err != nil {
			if !errors.Is(err, ErrNotFound) {
				errs = append(errs, err)
			}
			continue
		}
		r.Moved = append(r.Moved, a)
	}
	return r, errors.Join(errs...)
}
