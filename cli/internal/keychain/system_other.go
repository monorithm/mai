//go:build !(darwin && cgo)

package keychain

// System returns the keychain of the OS.
func System() Store { return goKeyring{} }
