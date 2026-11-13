//go:build !presencefake

package keychain

// movedService is always empty in a release build. Thus a release build uses only Service.
func movedService() string { return "" }
