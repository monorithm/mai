// Command mai is the host CLI of mai 3. It runs the container of the mail server through the
// Engine API, keeps the keys of the owner and of the agents in its key file, and bridges the
// MCP stdio of an agent app to the HTTP endpoint of the server.
//
// Each command registers itself in a file cmd_<area>.go with register().
package main

import (
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/monorithm/mai/cli/v3/internal/version"
)

// command is one subcommand. run gets the arguments after the name and returns the exit code.
type command struct {
	name    string
	summary string
	run     func(args []string) int
}

var commands = map[string]command{}

// register adds a subcommand. Call it from an init function.
func register(name, summary string, run func(args []string) int) {
	if _, dup := commands[name]; dup {
		panic("mai: two commands have the name " + name)
	}
	commands[name] = command{name: name, summary: summary, run: run}
}

// agentLine is the first line of the usage. An AI agent that runs mai with no command finds
// the command for it.
const agentLine = "An AI agent: run mai guide."

func usage() { writeUsage(os.Stderr) }

func writeUsage(w io.Writer) {
	fmt.Fprintf(w, "%s\nmai %s\n\nusage: mai COMMAND [ARGS]\n\ncommands:\n", agentLine, version.Version)
	names := make([]string, 0, len(commands))
	for n := range commands {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(w, "  %-12s %s\n", n, commands[n].summary)
	}
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "-h", "--help", "help":
		usage()
		return
	case "--version", "version":
		fmt.Println("mai", version.Version)
		return
	}
	c, ok := commands[os.Args[1]]
	if !ok {
		fmt.Fprintf(os.Stderr, "mai: unknown command %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	os.Exit(c.run(os.Args[2:]))
}
