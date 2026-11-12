package presence

import (
	"errors"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
)

// polkitAction is the polkit action that the Linux package installs from dev.mai.policy.
const polkitAction = "dev.mai.lease"

// polkitPolicyFile is the name of the file that holds the action.
const polkitPolicyFile = "dev.mai.policy"

// polkitActionDirs are the folders where polkit finds the file of an action.
var polkitActionDirs = []string{"/usr/share/polkit-1/actions", "/etc/polkit-1/actions"}

// polkit asks polkit of Linux for a presence check through pkcheck. Each field is one OS
// call. A test replaces them.
type polkit struct {
	// lookPath finds a program in PATH.
	lookPath func(file string) (string, error)
	// run starts a program and returns its exit code. The error is not nil only when the
	// program did not start or did not stop normally.
	run func(name string, args ...string) (int, error)
	// exists tells if a file exists.
	exists func(path string) bool
	// subject is the value of the pkcheck option --process for this process.
	subject func() string
}

// actionInstalled tells if polkit knows the action of mai.
func (p polkit) actionInstalled() bool {
	if pkaction, err := p.lookPath("pkaction"); err == nil {
		code, err := p.run(pkaction, "--action-id", polkitAction)
		return err == nil && code == 0
	}
	for _, dir := range polkitActionDirs {
		if p.exists(path.Join(dir, polkitPolicyFile)) {
			return true
		}
	}
	return false
}

func (p polkit) available() bool {
	if _, err := p.lookPath("pkcheck"); err != nil {
		return false
	}
	return p.actionInstalled()
}

// check runs pkcheck. Exit code 0 means that the user agrees. Each other result is a
// refusal.
func (p polkit) check(reason string) error {
	pkcheck, err := p.lookPath("pkcheck")
	if err != nil || !p.actionInstalled() {
		return ErrUnavailable
	}
	args := []string{
		"--action-id", polkitAction,
		"--process", p.subject(),
		"--allow-user-interaction",
	}
	if reason != "" {
		args = append(args, "--detail", "reason", reason)
	}
	code, err := p.run(pkcheck, args...)
	if err != nil || code != 0 {
		return ErrRefused
	}
	return nil
}

// runExit runs a program with no stdin and no output. The program must not use the stdio
// of mai, because the stdio of a bridge carries MCP.
func runExit(name string, args ...string) (int, error) {
	cmd := exec.Command(name, args...)
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.Exited() {
		return exit.ExitCode(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// processSubject returns "pid,start-time,uid" for pkcheck. stat is the text of
// /proc/<pid>/stat. When the start time is not in stat, the result is only the pid.
func processSubject(pid, uid int, stat string) string {
	p := strconv.Itoa(pid)
	start, ok := startTime(stat)
	if !ok {
		return p
	}
	return p + "," + start + "," + strconv.Itoa(uid)
}

// startTime returns field 22 of /proc/<pid>/stat. The name of the program in field 2 can
// hold spaces and parentheses, thus the fields start after the last ")".
func startTime(stat string) (string, bool) {
	i := strings.LastIndexByte(stat, ')')
	if i < 0 {
		return "", false
	}
	// After ")" the first field is field 3, thus field 22 has index 19.
	fields := strings.Fields(stat[i+1:])
	if len(fields) < 20 {
		return "", false
	}
	if _, err := strconv.ParseUint(fields[19], 10, 64); err != nil {
		return "", false
	}
	return fields[19], true
}
