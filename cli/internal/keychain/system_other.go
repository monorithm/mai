//go:build !darwin

package keychain

// System returns the keychain of the OS.
func System() Store { return goKeyring{} }
