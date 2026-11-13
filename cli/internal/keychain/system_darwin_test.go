//go:build darwin && cgo

package keychain

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"
)

// testService is the only service that the tests use. It is never Service, thus the tests
// never touch an item of mai.
const testService = "dev.mai.test.legacy"

// liveStore returns a store of testService on the real keychain that never shows a prompt,
// and a new account. These tests use the keychain of the user, and macOS can show a dialog
// for them. Thus they run only when MAI_TEST_LIVE_KEYCHAIN is 1. The test stops when the
// default keychain is locked.
func liveStore(t *testing.T) (securityStore, string) {
	t.Helper()
	if os.Getenv("MAI_TEST_LIVE_KEYCHAIN") != "1" {
		t.Skip("set MAI_TEST_LIVE_KEYCHAIN=1 to run the test on the keychain of the user")
	}
	if testService == Service {
		t.Fatal("the test service is the service of mai")
	}
	if !defaultUnlocked() {
		t.Skip("the default keychain is locked")
	}
	account := fmt.Sprintf("test-%d-%d", os.Getpid(), time.Now().UnixNano())
	s := securityStore{service: testService, noUI: true}
	t.Cleanup(func() {
		if err := s.Delete(account); err != nil && !errors.Is(err, ErrNotFound) {
			t.Errorf("cleanup delete: %v", err)
		}
	})
	return s, account
}

// putItem adds an item of testService that each program can read, as a legacy item that
// macOS shows no prompt for. The package has no write, thus the tool security adds it.
func putItem(t *testing.T, account, secret string) {
	t.Helper()
	cmd := exec.Command("/usr/bin/security", "add-generic-password", "-A", "-s", testService, "-a", account, "-w", secret)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("security add-generic-password: %v: %s", err, out)
	}
}

func TestSecurityStoreReadsHasAndDeletesAnItem(t *testing.T) {
	s, account := liveStore(t)
	if _, err := s.Get(account); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get before the item: err = %v, want ErrNotFound", err)
	}
	if ok, err := s.Has(account); err != nil || ok {
		t.Fatalf("has before the item = %v, %v", ok, err)
	}
	const secret = "mai1:owner:legacy-test-value"
	putItem(t, account, secret)
	if ok, err := s.Has(account); err != nil || !ok {
		t.Fatalf("has = %v, %v", ok, err)
	}
	got, err := s.Get(account)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != secret {
		t.Fatal("get gave a different value")
	}
	if err := s.Delete(account); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if ok, err := s.Has(account); err != nil || ok {
		t.Fatalf("has after the delete = %v, %v", ok, err)
	}
	if err := s.Delete(account); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: err = %v, want ErrNotFound", err)
	}
}

func TestSecurityStoreAccountsAreSeparate(t *testing.T) {
	s, account := liveStore(t)
	other := account + "-other"
	t.Cleanup(func() { _ = s.Delete(other) })
	putItem(t, account, "value-a")
	if ok, err := s.Has(other); err != nil || ok {
		t.Fatalf("has of a different account = %v, %v", ok, err)
	}
	if err := s.Delete(other); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete of a different account: %v", err)
	}
	if got, err := s.Get(account); err != nil || got != "value-a" {
		t.Fatalf("get after a delete of a different account: err = %v, equal = %v", err, got == "value-a")
	}
}

func TestStatusErrorGivesTheErrorsOfThePackage(t *testing.T) {
	if err := statusError("get", errSecItemNotFound); !errors.Is(err, ErrNotFound) {
		t.Fatalf("not found: %v", err)
	}
	if err := statusError("get", errSecNoDefaultKeychain); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("no default keychain: %v", err)
	}
	err := statusError("get", -25293)
	if err == nil || err.Error() == "" || errors.Is(err, ErrNotFound) || errors.Is(err, ErrUnavailable) {
		t.Fatalf("a different status: %v", err)
	}
}

func TestSystemIsSecurityStoreOfService(t *testing.T) {
	s, ok := System().(securityStore)
	if !ok || s.service != Service || s.noUI {
		t.Fatalf("System() = %#v, want securityStore of %s", System(), Service)
	}
}
