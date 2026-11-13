package keychain

import (
	"errors"
	"os"
	"runtime"
	"strconv"
	"testing"
	"time"

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
