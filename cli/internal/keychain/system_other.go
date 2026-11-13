//go:build !(darwin && cgo)

package keychain

// system returns the keychain of the OS for service.
func system(service string) Store { return goKeyring{service: service} }
