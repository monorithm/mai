package uninstall

import (
	"errors"
	"os"
	"strings"
)

// Binary removes the executable of the CLI as the last step.
type Binary struct {
	Path string // the executable, with the symbolic links resolved
	GOOS string
	// Run runs a package manager and waits for it.
	Run func(name string, args ...string) error
	// Start starts a process that continues after the exit of mai.
	Start func(name string, args ...string) error
}

// Method tells how the binary goes: "brew", "scoop", "after-exit", or "file".
func (b *Binary) Method() string {
	p := strings.ReplaceAll(b.Path, `\`, "/")
	lower := strings.ToLower(p)
	switch {
	case strings.Contains(p, "/Cellar/mai/"):
		return "brew"
	case strings.Contains(lower, "/scoop/apps/mai/"):
		return "scoop"
	case b.GOOS == "windows":
		return "after-exit"
	}
	return "file"
}

// Remove removes the binary and gives the result.
func (b *Binary) Remove() Result {
	item := "binary " + b.Path
	switch b.Method() {
	case "brew":
		return b.runManager(item, "brew", "uninstall", "mai")
	case "scoop":
		return b.runManager(item, "scoop", "uninstall", "mai")
	case "after-exit":
		if b.Start == nil {
			return Result{Item: item, State: Failure, Detail: "no way to start the remove"}
		}
		if err := b.Start("cmd", "/c", "ping -n 3 127.0.0.1 >nul & del \""+b.Path+"\""); err != nil {
			return Result{Item: item, State: Failure, Detail: err.Error()}
		}
		return Result{Item: item, State: Gone, Detail: "the file goes after mai exits"}
	}
	err := os.Remove(b.Path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Result{Item: item, State: Failure, Detail: err.Error()}
	}
	if _, err := os.Lstat(b.Path); err == nil {
		return Result{Item: item, State: Failure, Detail: "it still exists"}
	}
	return Result{Item: item, State: Gone}
}

func (b *Binary) runManager(item, name string, args ...string) Result {
	if b.Run == nil {
		return Result{Item: item, State: Failure, Detail: "no way to run " + name}
	}
	if err := b.Run(name, args...); err != nil {
		return Result{Item: item, State: Failure, Detail: name + " uninstall mai: " + err.Error()}
	}
	return Result{Item: item, State: Gone, Detail: "removed by " + name}
}
