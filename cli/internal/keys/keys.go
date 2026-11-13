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
	"encoding/base64"
	"encoding/hex"
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
	key, secret, err := f.move(account)
	if errors.Is(err, ErrNotFound) {
		// A different process can have moved the legacy item in the meantime. Then the key
		// file has its key.
		return f.inFile(account)
	}
	if err == nil && key == secret {
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

// move reads the legacy item of the account and writes it to the key file. It gives the key
// of the key file and the secret of the legacy item. It does not delete the legacy item: when
// key is secret, the caller deletes the item. When a different process wrote a key for the
// account in the meantime, that key stays, and move gives it. Then key is not secret if the
// legacy item holds a different key.
func (f *File) move(account string) (key, secret string, err error) {
	secret, err = f.legacyGet(account)
	switch {
	case errors.Is(err, keychain.ErrNotFound):
		return "", "", ErrNotFound
	case errors.Is(err, keychain.ErrUnavailable) && !f.recorded(account):
		return "", "", ErrNotFound
	case err != nil:
		return "", "", &LegacyError{Account: account, Err: err}
	case secret == "":
		return "", "", ErrNotFound
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
		return "", "", fmt.Errorf("keys: move %s from the keychain to the key file: %w", account, err)
	}
	// When key is not secret, nothing tells which of the two keys the server accepts. Thus the
	// legacy item stays, unless "mai upgrade" does a check of the server.
	return key, secret, nil
}

// Prefixes of the data of a keychain item that go-keyring wrote. mai 3.0 and 3.1 built on macOS
// with no cgo wrote each item through go-keyring and /usr/bin/security. go-keyring keeps the
// secret as "go-keyring-base64:" and the base64 of the secret. Its older versions used
// "go-keyring-encoded:" and the hex of the secret.
const (
	goKeyringBase64 = "go-keyring-base64:"
	goKeyringHex    = "go-keyring-encoded:"
)

// legacyGet reads the secret of the legacy item of the account. It decodes the data that
// go-keyring wrote. A Store that decoded the data gives the secret, and a secret of mai never
// starts with a prefix of go-keyring.
func (f *File) legacyGet(account string) (string, error) {
	data, err := f.legacy.Get(account)
	if err != nil {
		return "", err
	}
	var dec []byte
	switch {
	case strings.HasPrefix(data, goKeyringBase64):
		dec, err = base64.StdEncoding.DecodeString(strings.TrimPrefix(data, goKeyringBase64))
	case strings.HasPrefix(data, goKeyringHex):
		dec, err = hex.DecodeString(strings.TrimPrefix(data, goKeyringHex))
	default:
		return data, nil
	}
	if err != nil {
		// The error of the decoder can quote the data, thus it is not shown.
		return "", errors.New("keychain: the data of the item has a prefix of go-keyring, but it is not valid")
	}
	return string(dec), nil
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

// RemoveEmpty removes the key file and its lock file when the key file holds no key. The check
// and the remove of the key file occur with the lock. Thus a key that a different process
// writes before the remove stays. RemoveEmpty gives the accounts of a key file that stays.
// After the remove, a process that waited for the lock writes a new key file.
func (f *File) RemoveEmpty() ([]string, error) {
	if _, err := os.Stat(filepath.Dir(f.path)); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	release, err := f.lock()
	if err != nil {
		return nil, err
	}
	d, err := f.read()
	if err != nil {
		release()
		return nil, err
	}
	if len(d.Keys) > 0 {
		release()
		var rest []string
		for a := range d.Keys {
			rest = append(rest, a)
		}
		sort.Strings(rest)
		return rest, nil
	}
	err = os.Remove(f.path)
	release()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("keys: remove the key file: %w", err)
	}
	if err := os.Remove(f.lockPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("keys: remove the lock file: %w", err)
	}
	return nil, nil
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
// the owner account, each account in recorded, each account that the manifest in the folder of
// the key file records for the instance, and each account of the instance in the key file. It
// ignores an account of recorded with a different instance. Thus a command finds the legacy
// item of an agent through its record, also when the caller gives no record.
func (f *File) Candidates(instance string, recorded []string) ([]string, error) {
	set := map[string]bool{OwnerAccount(instance): true}
	names := append([]string(nil), recorded...)
	m, merr := manifest.Load(filepath.Join(filepath.Dir(f.path), manifest.FileName), instance)
	if merr == nil {
		for _, it := range m.Of(manifest.KeychainKey) {
			names = append(names, it.Name)
		}
	} else {
		merr = fmt.Errorf("keys: read the manifest: %w", merr)
	}
	for _, a := range names {
		if InstanceOf(a) == instance {
			set[a] = true
		}
	}
	inFile, err := f.OfInstance(instance)
	err = errors.Join(err, merr)
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
	// Replaced holds each account whose key MoveAll replaced in the key file with the key of
	// the legacy item, because the server accepts the key of the legacy item and refuses the
	// key of the key file. MoveAll then deleted the legacy item.
	Replaced []string
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
	// Checked tells if the check of the server occurred. When it is false, the other fields
	// tell nothing about the server.
	Checked bool
	// FileAccepted and LegacyAccepted tell if the server accepts the key of the key file and
	// the key of the legacy item.
	FileAccepted, LegacyAccepted bool
	// Err is the error of the check of the server, or nil.
	Err error
}

// Accepts tells if the server accepts the key of the account. It gives an error when it
// cannot tell, for example when the server does not run.
type Accepts func(account, key string) (bool, error)

// check does the check of the server for a legacy item whose secret is not the key of the key
// file. clean is true when the server accepts the key of the key file and refuses the secret of
// the legacy item. Then the legacy item can go. With no accepts, no check occurs.
func check(account, key, secret string, accepts Accepts) (c Conflict, clean bool) {
	c = Conflict{Account: account}
	if accepts == nil {
		return c, false
	}
	c.Checked = true
	c.FileAccepted, c.Err = accepts(account, key)
	if c.Err == nil {
		c.LegacyAccepted, c.Err = accepts(account, secret)
	}
	return c, c.Err == nil && c.FileAccepted && !c.LegacyAccepted
}

// replaceWithLegacy writes secret, the key of the legacy item, to the key file in place of key,
// and then deletes the legacy item. When a different process changed the key of the key file
// after the check, nothing proves which key the server accepts. Then nothing changes, and the
// report has a conflict with no check.
func (f *File) replaceWithLegacy(r MoveReport, account, key, secret string) (MoveReport, error) {
	replaced := false
	err := f.update(func(d *doc) bool {
		if d.Keys[account] != key {
			return false
		}
		d.Keys[account], replaced = secret, true
		return true
	})
	switch {
	case err != nil:
		return r, fmt.Errorf("keys: write the key of the keychain item of %s to the key file: %w", account, err)
	case !replaced:
		r.Conflicts = append(r.Conflicts, Conflict{Account: account})
		return r, nil
	}
	if err := f.DeleteLegacy(account); err != nil {
		r.Stays = append(r.Stays, Stay{Account: account, Err: err})
		return r, nil
	}
	r.Replaced = append(r.Replaced, account)
	return r, nil
}

// MoveAll moves the legacy keychain item of each account to the key file. When the key file
// has a key for the account of a legacy item, also a key that a different process wrote during
// the move, MoveAll deletes the legacy item only when it holds the same key, or when accepts
// tells that the server accepts the key of the key file and refuses the key of the legacy item.
// When accepts tells that the server accepts only the key of the legacy item, MoveAll writes
// that key to the key file and deletes the legacy item. Else the legacy item stays, and the
// report has a conflict. When the delete of a legacy item
// fails, the report has a stay. accepts can be nil. The error joins the failure of each account.
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
		var secret string
		if inFile {
			secret, err = f.legacyGet(a)
			switch {
			case errors.Is(err, keychain.ErrNotFound):
				continue
			case err != nil:
				errs = append(errs, &LegacyError{Account: a, Err: err})
				continue
			}
		} else {
			key, secret, err = f.move(a)
			switch {
			case errors.Is(err, ErrNotFound):
				continue
			case err != nil:
				errs = append(errs, err)
				continue
			case key == secret:
				if err := f.DeleteLegacy(a); err != nil {
					r.Stays = append(r.Stays, Stay{Account: a, Err: err})
					continue
				}
				r.Moved = append(r.Moved, a)
				continue
			}
			// Else a different process wrote a different key in the meantime.
		}
		if secret != key {
			c, clean := check(a, key, secret, accepts)
			if c.Checked && c.Err == nil && c.LegacyAccepted && !c.FileAccepted {
				// The server proves the key of the legacy item, and the key of the key file is of
				// no use.
				if r, err = f.replaceWithLegacy(r, a, key, secret); err != nil {
					errs = append(errs, err)
				}
				continue
			}
			if !clean {
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
