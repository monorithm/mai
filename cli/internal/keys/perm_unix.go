//go:build !windows

package keys

import "os"

// checkMode refuses a key file that a group or other users can read or change.
func checkMode(path string, fi os.FileInfo) error {
	if m := fi.Mode().Perm(); m&0o077 != 0 {
		return &ModeError{Path: path, Mode: m}
	}
	return nil
}

// ensureDir makes the folder of the key file with mode 0700. A folder of mai with a looser
// mode gets the mode 0700.
func ensureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if fi.Mode().Perm() != 0o700 {
		return os.Chmod(dir, 0o700)
	}
	return nil
}

// syncDir writes the entries of the folder to the disk, thus a rename stays after a crash.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}
