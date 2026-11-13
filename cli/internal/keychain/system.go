package keychain

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/godbus/dbus/v5"
	"github.com/zalando/go-keyring"
)

// goKeyring is the Store of zalando/go-keyring for one service. On macOS it uses
// /usr/bin/security. The darwin build with cgo replaces it with the Security framework.
type goKeyring struct{ service string }

// wrap gives the error of this package for an error of go-keyring. On Linux and the other
// systems with a Secret Service, an error other than "not found" usually means that no
// Secret Service is available. Then the keychain holds no item. But when a Secret Service
// is available and refuses the request, the item can exist. Then the error is not
// ErrUnavailable, and the CLI does not make a new key in place of the item.
func (g goKeyring) wrap(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, keyring.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, keyring.ErrUnsupportedPlatform):
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	case runtime.GOOS != "darwin" && runtime.GOOS != "windows":
		if secretServiceRefused(err) {
			return fmt.Errorf("keychain: the Secret Service refused the request: %w", err)
		}
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return err
}

// secretErrors is the prefix of the names of the errors of the Secret Service API.
const secretErrors = "org.freedesktop.Secret.Error."

// unlockFailed is the start of the text of go-keyring when the unlock of a collection or of
// an item fails, for example because the user refused the prompt.
const unlockFailed = "failed to unlock correct collection"

// secretServiceRefused tells if an error of go-keyring comes from a Secret Service that is
// available and refused the request: a failed unlock, or an error of the Secret Service API,
// for example IsLocked. The error NoSuchObject means that the collection does not exist,
// thus the collection holds no item.
func secretServiceRefused(err error) bool {
	var de dbus.Error
	if errors.As(err, &de) {
		return strings.HasPrefix(de.Name, secretErrors) && de.Name != secretErrors+"NoSuchObject"
	}
	return strings.HasPrefix(err.Error(), unlockFailed)
}

func (g goKeyring) Get(account string) (string, error) {
	s, err := keyring.Get(g.service, account)
	if err != nil {
		return "", g.wrap(err)
	}
	return s, nil
}

// Has reads the item on Linux and Windows, because go-keyring has no call that reads only the
// attributes. On macOS, it reads only the attributes, thus macOS shows no prompt.
func (g goKeyring) Has(account string) (bool, error) {
	if runtime.GOOS == "darwin" {
		return securityHas(g.service, account)
	}
	_, err := g.Get(account)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (g goKeyring) Delete(account string) error {
	return g.wrap(keyring.Delete(g.service, account))
}

// errSecurityNotFound is the exit code of /usr/bin/security for an item that does not exist.
const errSecurityNotFound = 44

// securityHas runs /usr/bin/security with no option -w. Thus the tool reads only the
// attributes of the item, and macOS shows no prompt. The output goes to the null device.
func securityHas(service, account string) (bool, error) {
	err := exec.Command("/usr/bin/security", "find-generic-password", "-s", service, "-a", account).Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &exit) && exit.ExitCode() == errSecurityNotFound:
		return false, nil
	}
	return false, fmt.Errorf("keychain: security find-generic-password: %w", err)
}
