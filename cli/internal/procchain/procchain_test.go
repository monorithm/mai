package procchain

import (
	"os"
	"testing"
)

func TestSelfGivesThisProcessAndItsParent(t *testing.T) {
	chain, err := Self()
	if err != nil {
		t.Fatalf("Self: %v", err)
	}
	if len(chain) < 2 {
		t.Fatalf("chain has %d items, want 2 or more: %+v", len(chain), chain)
	}
	if chain[0].PID != os.Getpid() {
		t.Errorf("first PID = %d, want %d", chain[0].PID, os.Getpid())
	}
	if chain[0].PPID != os.Getppid() || chain[1].PID != os.Getppid() {
		t.Errorf("parent = %d and %d, want %d", chain[0].PPID, chain[1].PID, os.Getppid())
	}
	if chain[0].Exe == "" || chain[0].Name() == "" {
		t.Errorf("no executable for this process: %+v", chain[0])
	}
}

func TestTopSkipsTheFirstProcessOfTheOS(t *testing.T) {
	chain := []Proc{
		{PID: 30, PPID: 20, Exe: "/usr/local/bin/mai"},
		{PID: 20, PPID: 1, Exe: "/Applications/Claude.app/Contents/MacOS/Claude"},
		{PID: 1, PPID: 0, Exe: "/sbin/launchd"},
	}
	top, ok := Top(chain)
	if !ok || top.Name() != "Claude" {
		t.Fatalf("Top = %+v %v, want Claude", top, ok)
	}
	if _, ok := Top(nil); ok {
		t.Fatal("Top of an empty chain gives a process")
	}
}

func TestNameRemovesTheFolderAndTheExeSuffix(t *testing.T) {
	if n := (Proc{Exe: `C:\Program Files\Cursor\Cursor.exe`}).Name(); n != "Cursor" {
		t.Errorf("Name = %q, want Cursor", n)
	}
}
