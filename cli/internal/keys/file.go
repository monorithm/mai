package keys

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// fileVersion is the version of the format of the key file.
const fileVersion = 1

// maxFileSize is the largest key file that the CLI reads.
const maxFileSize = 1 << 20

// lockTimeout is the longest wait for the lock of the key file. A process holds the lock
// only for a read or a write of the file, never during a prompt. The tests change it.
var lockTimeout = 10 * time.Second

// lockPoll is the time between two tries of the lock.
const lockPoll = 10 * time.Millisecond

// procMu lets one goroutine of this process at a time hold the lock of a key file. The file
// lock is between processes.
var procMu sync.Mutex

// doc is the content of the key file.
type doc struct {
	Version int               `json:"version"`
	Keys    map[string]string `json:"keys"`
}

func (f *File) lockPath() string { return f.path + ".lock" }

// view reads the key file with the lock and calls fn. A missing file or folder gives an
// empty file.
func (f *File) view(fn func(d *doc)) error {
	if _, err := os.Stat(filepath.Dir(f.path)); errors.Is(err, os.ErrNotExist) {
		fn(&doc{Version: fileVersion, Keys: map[string]string{}})
		return nil
	}
	release, err := f.lock()
	if err != nil {
		return err
	}
	defer release()
	d, err := f.read()
	if err != nil {
		return err
	}
	fn(d)
	return nil
}

// update reads the key file with the lock, calls fn, and writes the file when fn gives true.
func (f *File) update(fn func(d *doc) bool) error {
	if err := ensureDir(filepath.Dir(f.path)); err != nil {
		return fmt.Errorf("keys: the folder of the key file: %w", err)
	}
	release, err := f.lock()
	if err != nil {
		return err
	}
	defer release()
	d, err := f.read()
	if err != nil {
		return err
	}
	if !fn(d) {
		return nil
	}
	return f.write(d)
}

// lock takes the lock of the key file. It waits a maximum of lockTimeout.
func (f *File) lock() (func(), error) {
	procMu.Lock()
	lf, err := os.OpenFile(f.lockPath(), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		procMu.Unlock()
		return nil, fmt.Errorf("keys: open the lock file: %w", err)
	}
	deadline := time.Now().Add(lockTimeout)
	for {
		ok, err := tryLock(lf)
		if err != nil {
			_ = lf.Close()
			procMu.Unlock()
			return nil, fmt.Errorf("keys: lock the key file: %w", err)
		}
		if ok {
			break
		}
		if time.Now().After(deadline) {
			_ = lf.Close()
			procMu.Unlock()
			return nil, fmt.Errorf("%w: a different process holds the lock %s for more than %s. Try again later", ErrBusy, f.lockPath(), lockTimeout)
		}
		time.Sleep(lockPoll)
	}
	return func() {
		_ = unlock(lf)
		_ = lf.Close()
		procMu.Unlock()
	}, nil
}

// read reads the key file. Hold the lock. A missing file gives an empty file.
func (f *File) read() (*doc, error) {
	empty := &doc{Version: fileVersion, Keys: map[string]string{}}
	fh, err := os.Open(f.path)
	if errors.Is(err, os.ErrNotExist) {
		return empty, nil
	}
	if err != nil {
		return nil, fmt.Errorf("keys: open the key file: %w", err)
	}
	defer fh.Close()
	fi, err := fh.Stat()
	if err != nil {
		return nil, fmt.Errorf("keys: examine the key file: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("keys: the key file %s is not a regular file", f.path)
	}
	if err := checkMode(f.path, fi); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(fh, maxFileSize+1))
	if err != nil {
		return nil, fmt.Errorf("keys: read the key file: %w", err)
	}
	if len(data) > maxFileSize {
		return nil, fmt.Errorf("keys: the key file %s is larger than %d bytes", f.path, maxFileSize)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return empty, nil
	}
	var d doc
	if err := json.Unmarshal(data, &d); err != nil {
		// The error of the decoder can quote the text of the file, thus it is not shown.
		return nil, fmt.Errorf("keys: the key file %s is not valid JSON. Correct it, or move it to a different folder", f.path)
	}
	if d.Version > fileVersion {
		return nil, fmt.Errorf("keys: the key file %s has the version %d, and this CLI knows only the version %d. Install a newer mai", f.path, d.Version, fileVersion)
	}
	d.Version = fileVersion
	if d.Keys == nil {
		d.Keys = map[string]string{}
	}
	return &d, nil
}

// write replaces the key file atomically: a temporary file in the same folder with mode
// 0600, an fsync, a rename, and an fsync of the folder. Hold the lock.
func (f *File) write(d *doc) error {
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(f.path)
	tmp, err := os.CreateTemp(dir, ".keys-*.tmp")
	if err != nil {
		return fmt.Errorf("keys: make a temporary file: %w", err)
	}
	name := tmp.Name()
	done := false
	defer func() {
		if !done {
			_ = tmp.Close()
			_ = os.Remove(name)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("keys: set the mode of the temporary file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("keys: write the temporary file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("keys: sync the temporary file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("keys: close the temporary file: %w", err)
	}
	if err := os.Rename(name, f.path); err != nil {
		_ = os.Remove(name)
		done = true
		return fmt.Errorf("keys: replace the key file: %w", err)
	}
	done = true
	syncDir(dir)
	return nil
}

// Check examines the key file and its folder, and reads no key. It gives nil when the file
// does not exist. "mai doctor" uses it.
func (f *File) Check() error {
	return f.view(func(*doc) {})
}
