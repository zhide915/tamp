// Command tamp is the tamp CLI — an environment manager for Frappe Framework.
//
// This package holds the cobra command layer and nothing else: every command
// is a thin translation from flags to a call into internal/..., so the core
// stays testable without a TTY and a future MCP server can reuse it.
package main

import (
	"io"
	"os"

	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/ui"
)

// Overridden at release time with -ldflags -X main.version=... (and commit,
// buildDate). A plain `go build` leaves the placeholders below.
var (
	version   = "dev"
	commit    = "none"
	buildDate = "unknown"
)

func main() {
	ui.EnableColorSupport()
	os.Exit(int(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.LookupEnv, engine.New())))
}

// run is the whole CLI minus the process. Tests drive this directly, which is
// why nothing below it reads os.Args, os.Stdin, os.Stdout, the real
// environment — or reaches Docker except through the engine handed to it.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer, lookupEnv func(string) (string, bool), eng engine.Engine) exitcode.Code {
	p := ui.NewPrinter(stdout, stderr, lookupEnv)
	root := newRootCommand(p, eng, stdin)
	root.SetArgs(args)

	if err := root.Execute(); err != nil {
		// A reported failure was already explained by the command that
		// returned it, in more detail than one line could carry.
		if !exitcode.Silent(err) {
			p.Error(err)
		}
		return exitcode.Of(err)
	}
	return exitcode.CodeOK
}
