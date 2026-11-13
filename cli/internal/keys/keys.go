// Package keys keeps the owner key and the agent keys of each instance in the key file of
// the CLI, keys.json in the state folder.
//
// The file has mode 0600, in a folder with mode 0700. Its accounts have the names of the
// legacy keychain items: "owner@<instance>" and "agent:<name>@<instance>". Each write is
// atomic: a temporary file in the same folder, an fsync, and a rename. A lock on the file
// keys.json.lock lets one process at a time read or change the file. Thus two CLIs or
// bridges at the same time do not lose a key.
//
// mai 3.0 and 3.1 kept the keys in the system keychain, and the manifest of the state folder
// recorded each item. When the key file has no key for an account, Get reads the legacy item,
// writes the key to the key file, and then deletes the legacy item.
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
	"github.com/monorithm/mai/cli/v3/internal/manifest"
	"github.com/monorithm/mai/cli/v3/internal/paths"
)

// FileName is the name of the key file in the state folder.
const FileName = "keys.json"

// ErrNotFound means that the key file and the legacy keychain have no key for the account.
var ErrNotFound = errors.New("keys: no key for the account")

// ErrBusy means that a different process held the lock of the key file for more than the
// wait. A subsequent try can succeed.
var ErrBusy = errors.New("keys: the key file is busy")

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
// legacy keychain item failed, for example because the user refused the prompt of macOS. It
// is not ErrNotFound: the legacy item can hold the key. A keychain that is not available
// gives a LegacyError only when the manifest records a legacy item for the account. The text
// of the error gives the step for the user.
type LegacyError struct {
	Account string
	Err     error
}

func (e *LegacyError) Error() string {
	if errors.Is(e.Err, keychain.ErrUnavailable) {
		return "keys: the key file has no key for " + e.Account + ", and the keychain is not available (" + e.Err.Error() +
			"). The manifest records a keychain item of mai 3.0 or 3.1 for this account. " +
			"Make the keychain available, for example in a desktop session of the user, and then do the command again"
	}
	return "keys: the key file has no key for " + e.Account + ", and the read of its keychain item failed: " + e.Err.Error() +
		". Unlock the keychain and let mai read the item, and then do the command again"
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
// exists. When no keychain is available, Get gives ErrNotFound only when the manifest records
// no legacy item for the account. mai 3.0 and 3.1 did not operate with no keychain, thus
// then no legacy item exists.
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
	key, same, err := f.move(account)
	if errors.Is(err, ErrNotFound) {
		// A different process can have moved the legacy item in the meantime. Then the key
		// file has its key.
		return f.inFile(account)
	}
	if err == nil && same {
		// The key is in the key file. When the delete fails, the legacy item stays, and
		// "mai doctor" and "mai upgrade" tell about it.
		_ = f.DeleteLegacy(account)
	}
	return key, err
}

// inFile gives the key of the account in the key file. It does not read the legacy keychain.
// It gives ErrNotFound when the key file has no key for the account.
func (f *File) inFile(account string) (string, error) {
	var key string
	var ok bool
	if err := f.view(func(d *doc) { key, ok = d.Keys[account] }); err != nil {
		return "", err
	}
	if !ok {
		return "", ErrNotFound
	}
	return key, nil
}

// move reads the legacy item of the account and writes it to the key file. It does not delete
// the legacy item: when same is true, the key file has the key of the legacy item, and the
// caller deletes the item. When a different process wrote a key for the account in the
// meantime, that key stays, and move gives it. Then same is false if the legacy item holds a
// different key.
func (f *File) move(account string) (key string, same bool, err error) {
	secret, err := f.legacy.Get(account)
	switch {
	case errors.Is(err, keychain.ErrNotFound):
		return "", false, ErrNotFound
	case errors.Is(err, keychain.ErrUnavailable) && !f.recorded(account):
		return "", false, ErrNotFound
	case err != nil:
		return "", false, &LegacyError{Account: account, Err: err}
	case secret == "":
		return "", false, ErrNotFound
	}
	key = secret
	err = f.update(func(d *doc) bool {
		if k, ok := d.Keys[account]; ok {
			key = k
			return false
		}
		d.Keys[account] = secret
		return true
	})
	if err != nil {
		return "", false, fmt.Errorf("keys: move %s from the keychain to the key file: %w", account, err)
	}
	if key != secret {
		// Nothing tells which of the two keys the server accepts, thus the legacy item stays.
		// "mai upgrade" tells about it.
		return key, false, nil
	}
	return key, true, nil
}

// recorded tells if the manifest in the folder of the key file records a legacy keychain item
// for the account. mai 3.0 and 3.1 recorded each item that they wrote. When the manifest
// cannot be read, recorded gives true, because the item can exist.
func (f *File) recorded(account string) bool {
	m, err := manifest.Load(filepath.Join(filepath.Dir(f.path), manifest.FileName), InstanceOf(account))
	if err != nil {
		return true
	}
	for _, it := range m.Of(manifest.KeychainKey) {
		if it.Name == account {
			return true
		}
	}
	return false
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

// Add writes the key of the account to the key file only when the file has no key for the
// account. The check and the write occur with one lock. Add gives the key that the file has
// after the call, and added tells if it is key. Thus Add never replaces a key that a different
// process wrote.
func (f *File) Add(account, key string) (stored string, added bool, err error) {
	if account == "" || key == "" {
		return "", false, errors.New("keys: an account and a key are necessary")
	}
	err = f.update(func(d *doc) bool {
		if k, ok := d.Keys[account]; ok {
			stored = k
			return false
		}
		d.Keys[account] = key
		stored, added = key, true
		return true
	})
	if err != nil {
		return "", false, err
	}
	return stored, added, nil
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
	// Moved holds each account whose key went from the keychain to the key file, and whose
	// legacy item MoveAll deleted.
	Moved []string
	// Cleaned holds each account whose legacy item MoveAll deleted, because the key file
	// had the same key, or because the server accepts the key of the key file and refuses
	// the key of the legacy item.
	Cleaned []string
	// Conflicts holds each legacy item that stays, because the key file has a different key
	// for its account, and nothing proves that the server accepts the key of the key file.
	// A conflict is not in the error of MoveAll.
	Conflicts []Conflict
	// Stays holds each legacy item whose delete failed, although the key file has the key of
	// its account. A stay is not in the error of MoveAll.
	Stays []Stay
	// Unavailable is true when no keychain is available. Then MoveAll moved no key.
	Unavailable bool
}

// Stay is a legacy item that stays, because its delete failed. The key file has the key of
// its account.
type Stay struct {
	Account string
	// Err is the error of the delete.
	Err error
}

// Conflict is a legacy item whose key is not the key of the key file, with the result of the
// check of the server. The legacy item and the key of the key file stay.
type Conflict struct {
	Account string
	// FileAccepted and LegacyAccepted tell if the server accepts the key of the key file and
	// the key of the legacy item.
	FileAccepted, LegacyAccepted bool
	// Err is the error of the check of the server, or nil.
	Err error
}

// Accepts tells if the server accepts the key of the account. It gives an error when it
// cannot tell, for example when the server does not run.
type Accepts func(account, key string) (bool, error)

// MoveAll moves the legacy keychain item of each account to the key file. When the key file
// has a key for the account of a legacy item, MoveAll deletes the legacy item only when it
// holds the same key, or when accepts tells that the server accepts the key of the key file
// and refuses the key of the legacy item. Else the legacy item stays, and the report has a
// conflict. When the delete of a legacy item fails, the report has a stay. accepts can be nil.
// The error joins the failure of each account.
func (f *File) MoveAll(accounts []string, accepts Accepts) (MoveReport, error) {
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
		var key string
		var inFile bool
		if err := f.view(func(d *doc) { key, inFile = d.Keys[a] }); err != nil {
			return r, err
		}
		if !inFile {
			_, same, err := f.move(a)
			switch {
			case errors.Is(err, ErrNotFound):
			case err != nil:
				errs = append(errs, err)
			case !same:
				// A different process wrote a different key in the meantime.
				r.Conflicts = append(r.Conflicts, Conflict{Account: a})
			default:
				if err := f.DeleteLegacy(a); err != nil {
					r.Stays = append(r.Stays, Stay{Account: a, Err: err})
					continue
				}
				r.Moved = append(r.Moved, a)
			}
			continue
		}
		secret, err := f.legacy.Get(a)
		switch {
		case errors.Is(err, keychain.ErrNotFound):
			continue
		case err != nil:
			errs = append(errs, &LegacyError{Account: a, Err: err})
			continue
		}
		if secret != key {
			c := Conflict{Account: a}
			if accepts != nil {
				c.FileAccepted, c.Err = accepts(a, key)
				if c.Err == nil {
					c.LegacyAccepted, c.Err = accepts(a, secret)
				}
			}
			if c.Err != nil || !c.FileAccepted || c.LegacyAccepted {
				r.Conflicts = append(r.Conflicts, c)
				continue
			}
		}
		if err := f.DeleteLegacy(a); err != nil {
			r.Stays = append(r.Stays, Stay{Account: a, Err: err})
			continue
		}
		r.Cleaned = append(r.Cleaned, a)
	}
	return r, errors.Join(errs...)
}
