package keychain

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/zalando/go-keyring"
)

func TestMemoryGetHasDelete(t *testing.T) {
	m := NewMemory()
	if _, err := m.Get("owner@x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get of no item: %v", err)
	}
	m.Put("owner@x", "v1")
	if ok, err := m.Has("owner@x"); err != nil || !ok {
		t.Fatalf("has = %v, %v", ok, err)
	}
	if got, err := m.Get("owner@x"); err != nil || got != "v1" {
		t.Fatalf("get = %q, %v", got, err)
	}
	if err := m.Delete("owner@x"); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete("owner@x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	m.Err = ErrUnavailable
	if _, err := m.Has("owner@x"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("has with Err: %v", err)
	}
}

func TestGoKeyringErrorsBecomeTheErrorsOfThePackage(t *testing.T) {
	g := goKeyring{service: "dev.mai.test.wrap"}
	if err := g.wrap(keyring.ErrNotFound); !errors.Is(err, ErrNotFound) {
		t.Fatalf("not found: %v", err)
	}
	if err := g.wrap(keyring.ErrUnsupportedPlatform); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unsupported platform: %v", err)
	}
	other := g.wrap(errors.New("dbus: no session bus"))
	switch runtime.GOOS {
	case "darwin", "windows":
		if errors.Is(other, ErrUnavailable) || errors.Is(other, ErrNotFound) {
			t.Fatalf("a different error on %s: %v", runtime.GOOS, other)
		}
	default:
		if !errors.Is(other, ErrUnavailable) {
			t.Fatalf("a different error on %s: %v", runtime.GOOS, other)
		}
	}
	if g.wrap(nil) != nil {
		t.Fatal("wrap(nil) is not nil")
	}
}

// A Secret Service that is available and refuses the request can hold the item, for example
// when the user refuses the prompt that unlocks the collection. Then the error is not
// ErrUnavailable. Thus the CLI does not make a new owner key in place of the item.
func TestARefusalOfTheSecretServiceIsNotUnavailable(t *testing.T) {
	g := goKeyring{service: "dev.mai.test.wrap"}
	for _, err := range []error{
		errors.New("failed to unlock correct collection '/org/freedesktop/secrets/aliases/login'"),
		errors.New("failed to unlock correct collection '/org/freedesktop/secrets/collection/login/42'"),
		dbus.Error{Name: "org.freedesktop.Secret.Error.IsLocked"},
	} {
		got := g.wrap(err)
		if got == nil || errors.Is(got, ErrUnavailable) || errors.Is(got, ErrNotFound) {
			t.Errorf("on %s, wrap(%v) = %v; want an error that is not ErrUnavailable", runtime.GOOS, err, got)
		}
	}
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		return
	}
	for _, err := range []error{
		dbus.Error{Name: "org.freedesktop.DBus.Error.ServiceUnknown"},
		dbus.Error{Name: "org.freedesktop.Secret.Error.NoSuchObject"},
		errors.New("dbus: couldn't determine address of session bus"),
	} {
		if got := g.wrap(err); !errors.Is(got, ErrUnavailable) {
			t.Errorf("on %s, wrap(%v) = %v; want ErrUnavailable", runtime.GOOS, err, got)
		}
	}
}

// A host with no session bus, for example a Linux server with no desktop, has no Secret
// Service. The test uses a bus address with no socket, thus it never uses a keychain.
func TestWithNoSessionBusTheKeychainIsNotAvailable(t *testing.T) {
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		t.Skip("a test of the Secret Service")
	}
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+filepath.Join(t.TempDir(), "no-bus"))
	g := goKeyring{service: "dev.mai.test.wrap"}
	if _, err := g.Get("owner@mai-test"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("get: %v, want ErrUnavailable", err)
	}
	if ok, err := g.Has("owner@mai-test"); ok || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("has = %v, %v, want ErrUnavailable", ok, err)
	}
}

// The test reads the keychain of the user, thus it runs only when MAI_TEST_LIVE_KEYCHAIN is 1.
// It uses a test service, never Service.
func TestSecurityHasOfNoItemIsFalse(t *testing.T) {
	if runtime.GOOS != "darwin" || os.Getenv("MAI_TEST_LIVE_KEYCHAIN") != "1" {
		t.Skip("set MAI_TEST_LIVE_KEYCHAIN=1 on macOS to run the test on the keychain of the user")
	}
	ok, err := securityHas("dev.mai.test.legacy", "test-none-"+strconv.FormatInt(time.Now().UnixNano(), 10))
	if ok || err != nil {
		t.Fatalf("securityHas = %v, %v", ok, err)
	}
}
