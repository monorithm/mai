//go:build presencefake

package keychain

import (
	"errors"
	"testing"
)

func TestTestServiceNeedsTheTestPrefix(t *testing.T) {
	cases := map[string]string{
		"":                   "",
		"dev.mai":            "",
		"dev.mai.test.":      "",
		"dev.mai.other":      "",
		"com.example.test.a": "",
		"dev.mai.test.e2e":   "dev.mai.test.e2e",
	}
	for value, want := range cases {
		t.Setenv(TestServiceVar, value)
		if got := movedService(); got != want {
			t.Errorf("movedService() with %q = %q, want %q", value, got, want)
		}
	}
}

func TestSystemUsesTheTestServiceWhenItIsSet(t *testing.T) {
	t.Setenv(TestServiceVar, "dev.mai.test.unit")
	ts, ok := System().(testStore)
	if !ok {
		t.Fatalf("System() = %T, want testStore", System())
	}
	if ts.service != "dev.mai.test.unit" {
		t.Fatalf("the test service is %q", ts.service)
	}
	t.Setenv(TestServiceVar, "")
	if _, ok := System().(testStore); ok {
		t.Fatal("System() with no test service gives a testStore")
	}
}

func TestTestStoreMovesOnlyTheItemsOfService(t *testing.T) {
	m := NewMemory()
	s := testStore{Store: m, service: "dev.mai.test.unit"}
	if err := s.Set(Service, "owner@x", "v1"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Get(Service, "owner@x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the item is in the service %s: %v", Service, err)
	}
	if got, err := m.Get("dev.mai.test.unit", "owner@x"); err != nil || got != "v1" {
		t.Fatalf("the item is not in the test service: %q %v", got, err)
	}
	if got, err := s.Get(Service, "owner@x"); err != nil || got != "v1" {
		t.Fatalf("Get = %q %v", got, err)
	}
	if err := s.Set("dev.mai.other", "a", "v2"); err != nil {
		t.Fatal(err)
	}
	if got, err := m.Get("dev.mai.other", "a"); err != nil || got != "v2" {
		t.Fatalf("a different service moved: %q %v", got, err)
	}
	if err := s.Delete(Service, "owner@x"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Get("dev.mai.test.unit", "owner@x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the item stays after Delete: %v", err)
	}
}
