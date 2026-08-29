// Command tamp manages containerized Frappe Framework environments.
//
// This package is only the cobra layer: each command translates flags into a
// call on internal/..., keeping the core testable without a TTY and reusable
// by other frontends.
package main

import (
	"context"
	"io"
	"os"
	"os/signal"

	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/syncer"
	"github.com/zhide915/tamp/internal/ui"
)

// Set at release time via -ldflags -X; plain builds keep the placeholders.
var (
	version   = "dev"
	commit    = "none"
	buildDate = "unknown"
)

func main() {
	ui.EnableColorSupport()
	// Error ignored: commands that need a home report its absence themselves.
	home, _ := env.Home()
	os.Exit(int(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.LookupEnv, engine.New(), syncer.New(home))))
}

// run is the CLI without the process: tests call it directly, so all I/O,
// the environment and the engine arrive as parameters.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer, lookupEnv func(string) (string, bool), eng engine.Engine, sync syncer.Mutagen) exitcode.Code {
	p := ui.NewPrinter(stdout, stderr, lookupEnv)
	root := newRootCommand(p, eng, sync, stdin, lookupEnv)
	root.SetArgs(args)

	// Ctrl-C cancels the context rather than killing the process, so followed
	// logs flush and an interrupted create still rolls back.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := root.ExecuteContext(ctx); err != nil {
		// Silent errors were already explained by the command itself.
		if !exitcode.Silent(err) {
			p.Error(err)
		}
		return exitcode.Of(err)
	}
	return exitcode.CodeOK
}
