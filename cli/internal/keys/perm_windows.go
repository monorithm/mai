//go:build windows

package keys

import "os"

// checkMode does nothing on Windows: the access list of the folder of the user protects the
// key file, and Windows has no Unix mode.
func checkMode(string, os.FileInfo) error { return nil }

// ensureDir makes the folder of the key file.
func ensureDir(dir string) error { return os.MkdirAll(dir, 0o700) }

// syncDir does nothing on Windows, because Windows cannot sync a folder.
func syncDir(string) {}
