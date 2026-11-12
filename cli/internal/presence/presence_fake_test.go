//go:build presencefake

package presence

import (
	"errors"
	"testing"
)

func TestFakeAgreesByDefault(t *testing.T) {
	t.Setenv("MAI_TEST_PRESENCE", "")
	if err := Check("cursor"); err != nil {
		t.Fatalf("check: %v", err)
	}
	if !Available() {
		t.Fatal("available = false, want true")
	}
}

func TestFakeRefuses(t *testing.T) {
	t.Setenv("MAI_TEST_PRESENCE", "refuse")
	if err := Check("cursor"); !errors.Is(err, ErrRefused) {
		t.Fatalf("err = %v, want ErrRefused", err)
	}
	if !Available() {
		t.Fatal("available = false, want true")
	}
}

func TestFakeUnavailable(t *testing.T) {
	t.Setenv("MAI_TEST_PRESENCE", "unavailable")
	if err := Check("cursor"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if Available() {
		t.Fatal("available = true, want false")
	}
}
