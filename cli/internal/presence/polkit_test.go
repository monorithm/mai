package presence

import (
	"errors"
	"os/exec"
	"slices"
	"testing"
)

// fakePolkit records the calls of a polkit check and gives fixed answers.
type fakePolkit struct {
	programs    map[string]bool
	files       map[string]bool
	actionCode  int
	pkcheckCode int
	pkcheckErr  error
	calls       [][]string
}

func (f *fakePolkit) polkit() polkit {
	return polkit{
		lookPath: func(file string) (string, error) {
			if f.programs[file] {
				return "/usr/bin/" + file, nil
			}
			return "", exec.ErrNotFound
		},
		run: func(name string, args ...string) (int, error) {
			f.calls = append(f.calls, append([]string{name}, args...))
			switch name {
			case "/usr/bin/pkaction":
				return f.actionCode, nil
			case "/usr/bin/pkcheck":
				return f.pkcheckCode, f.pkcheckErr
			}
			return 127, nil
		},
		exists:  func(path string) bool { return f.files[path] },
		subject: func() string { return "4242,99,1000" },
	}
}

func tools() map[string]bool { return map[string]bool{"pkcheck": true, "pkaction": true} }

func TestPolkitAgrees(t *testing.T) {
	f := &fakePolkit{programs: tools()}
	if err := f.polkit().check("cursor in /work"); err != nil {
		t.Fatalf("check: %v", err)
	}
	last := f.calls[len(f.calls)-1]
	want := []string{
		"/usr/bin/pkcheck", "--action-id", "dev.mai.lease", "--process", "4242,99,1000",
		"--allow-user-interaction", "--detail", "reason", "cursor in /work",
	}
	if !slices.Equal(last, want) {
		t.Fatalf("pkcheck args = %q, want %q", last, want)
	}
	if !f.polkit().available() {
		t.Fatal("available = false, want true")
	}
}

func TestPolkitRefuses(t *testing.T) {
	for _, code := range []int{1, 2, 3, 4} {
		f := &fakePolkit{programs: tools(), pkcheckCode: code}
		if err := f.polkit().check("x"); !errors.Is(err, ErrRefused) {
			t.Fatalf("exit %d: err = %v, want ErrRefused", code, err)
		}
	}
	f := &fakePolkit{programs: tools(), pkcheckErr: errors.New("signal")}
	if err := f.polkit().check("x"); !errors.Is(err, ErrRefused) {
		t.Fatalf("start failure: err = %v, want ErrRefused", err)
	}
}

func TestPolkitWithNoPkcheckIsUnavailable(t *testing.T) {
	f := &fakePolkit{programs: map[string]bool{"pkaction": true}}
	if err := f.polkit().check("x"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if f.polkit().available() {
		t.Fatal("available = true, want false")
	}
}

func TestPolkitWithNoActionIsUnavailable(t *testing.T) {
	f := &fakePolkit{programs: tools(), actionCode: 1}
	if err := f.polkit().check("x"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	for _, c := range f.calls {
		if c[0] == "/usr/bin/pkcheck" {
			t.Fatal("pkcheck ran with no action")
		}
	}
}

func TestPolkitWithNoPkactionUsesThePolicyFile(t *testing.T) {
	f := &fakePolkit{programs: map[string]bool{"pkcheck": true}}
	if err := f.polkit().check("x"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("no file: err = %v, want ErrUnavailable", err)
	}
	f.files = map[string]bool{"/usr/share/polkit-1/actions/dev.mai.policy": true}
	if err := f.polkit().check("x"); err != nil {
		t.Fatalf("with file: err = %v", err)
	}
}

func TestProcessSubject(t *testing.T) {
	// The name of the program holds a space and a ")".
	stat := "4242 (mai mcp) x) S 1 4242 4242 0 -1 4194560 100 0 0 0 1 2 0 0 20 0 1 0 " +
		"987654 1000 200 18446744073709551615 1 1 0 0 0 0 0 0 0 0 0 0 17 3 0 0 0 0 0"
	if got := processSubject(4242, 1000, stat); got != "4242,987654,1000" {
		t.Fatalf("subject = %q", got)
	}
	if got := processSubject(7, 1000, "garbage"); got != "7" {
		t.Fatalf("subject of bad stat = %q, want 7", got)
	}
}

func TestRunExitGivesTheExitCode(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	code, err := runExit(sh, "-c", "exit 3")
	if err != nil || code != 3 {
		t.Fatalf("code = %d, err = %v, want 3", code, err)
	}
	code, err = runExit(sh, "-c", "exit 0")
	if err != nil || code != 0 {
		t.Fatalf("code = %d, err = %v, want 0", code, err)
	}
}
