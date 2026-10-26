// Command tamp is an environment manager for Frappe Framework. This package
// is the cobra layer only: each command turns its flags into one call on
// the manager in internal/env.
package main

import (
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/zhide915/tamp/internal/docker"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/ui"
)

// A release build sets these with -ldflags -X.
var (
	version   = "dev"
	commit    = "none"
	buildDate = "unknown"
)

func main() {
	ui.EnableColorSupport()
	os.Exit(int(run(os.Args[1:], os.Stdout, os.Stderr, os.LookupEnv, docker.New())))
}

// run is tamp without the process, so tests drive it in-process.
func run(args []string, stdout, stderr io.Writer, lookupEnv func(string) (string, bool), dc docker.Client) exitcode.Code {
	p := ui.NewPrinter(stdout, stderr, lookupEnv)
	if noColor(args) {
		p.DisableColor()
	}
	root := newRootCommand(p, dc)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		p.Error(err)
		return exitcode.Of(err)
	}
	return exitcode.CodeOK
}

// noColor reads --no-color from the raw arguments, before cobra parses them:
// pflag stops at the first unknown flag, and the error it returns must
// honor a --no-color that comes after it. The last value wins, as in pflag.
func noColor(args []string) bool {
	on := false
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if arg == "--no-color" {
			on = true
		} else if v, ok := strings.CutPrefix(arg, "--no-color="); ok {
			on, _ = strconv.ParseBool(v)
		}
	}
	return on
}
