//go:build !(darwin && cgo)

package keychain

// system returns the keychain of the OS.
func system() Store { return goKeyring{} }
