//go:build darwin && cgo

package keychain

import (
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

// testService is the only service that the tests write. The tests remove each item.
const testService = "dev.mai.test.keychain"

// liveStore returns a store on the real keychain that never shows a prompt. The test
// stops when the default keychain is locked, because a write then shows a prompt.
func liveStore(t *testing.T) (securityStore, string) {
	t.Helper()
	if os.Getenv("MAI_TEST_NO_KEYCHAIN") != "" {
		t.Skip("MAI_TEST_NO_KEYCHAIN is set")
	}
	if !defaultUnlocked() {
		t.Skip("the default keychain is locked")
	}
	account := fmt.Sprintf("test-%d-%d", os.Getpid(), time.Now().UnixNano())
	s := securityStore{noUI: true}
	t.Cleanup(func() {
		if err := s.Delete(testService, account); err != nil && !errors.Is(err, ErrNotFound) {
			t.Errorf("cleanup delete: %v", err)
		}
	})
	return s, account
}

func TestSecurityStoreRoundTrip(t *testing.T) {
	s, account := liveStore(t)

	if _, err := s.Get(testService, account); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get before set: err = %v, want ErrNotFound", err)
	}

	first := "mai1:owner:first-secret-value"
	if err := s.Set(testService, account, first); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, err := s.Get(testService, account)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != first {
		t.Fatalf("get returned a different value")
	}

	second := "mai1:owner:second-secret-value"
	if err := s.Set(testService, account, second); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	got, err = s.Get(testService, account)
	if err != nil {
		t.Fatalf("get after overwrite: %v", err)
	}
	if got != second {
		t.Fatalf("get after overwrite returned a different value")
	}

	if err := s.Delete(testService, account); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.Get(testService, account); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete: err = %v, want ErrNotFound", err)
	}
	if err := s.Delete(testService, account); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: err = %v, want ErrNotFound", err)
	}
}

func TestSecurityStoreAccountsAreSeparate(t *testing.T) {
	s, account := liveStore(t)
	other := account + "-other"
	t.Cleanup(func() { _ = s.Delete(testService, other) })

	if err := s.Set(testService, account, "value-a"); err != nil {
		t.Fatalf("set a: %v", err)
	}
	if err := s.Set(testService, other, "value-b"); err != nil {
		t.Fatalf("set b: %v", err)
	}
	a, err := s.Get(testService, account)
	if err != nil || a != "value-a" {
		t.Fatalf("get a: err = %v, equal = %v", err, a == "value-a")
	}
	b, err := s.Get(testService, other)
	if err != nil || b != "value-b" {
		t.Fatalf("get b: err = %v, equal = %v", err, b == "value-b")
	}
}

func TestSecurityStoreAccessListHoldsOnlyThisProgram(t *testing.T) {
	s, account := liveStore(t)
	if got := decryptApps(testService, account); got != -1 {
		t.Fatalf("access list before set = %d, want -1", got)
	}
	if err := s.Set(testService, account, "value-acl"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if got := decryptApps(testService, account); got != 1 {
		t.Fatalf("applications that can decrypt = %d, want 1", got)
	}
	if err := s.Set(testService, account, "value-acl-2"); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if got := decryptApps(testService, account); got != 1 {
		t.Fatalf("applications that can decrypt after overwrite = %d, want 1", got)
	}
}

func TestStatusErrorHasNoSecret(t *testing.T) {
	err := statusError("get", -25293)
	if err == nil || err.Error() == "" {
		t.Fatal("statusError returned no text")
	}
}

func TestSystemIsSecurityStore(t *testing.T) {
	if _, ok := System().(securityStore); !ok {
		t.Fatalf("System() = %T, want securityStore", System())
	}
}
