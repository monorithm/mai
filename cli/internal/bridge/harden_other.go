//go:build !linux

package bridge

// harden does nothing on this OS. On macOS the signed binary with the hardened runtime
// refuses an attach.
func harden() error { return nil }
