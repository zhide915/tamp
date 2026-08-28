package env

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/frappe"
	"github.com/zhide915/tamp/internal/toolchain"
)

// Terminal is the console tamp itself is attached to, when it is attached to
// one.
//
// It is an interface because driving a real console is a process-level concern
// with no place below cmd/: everything here only ever asks it how big it is
// and to stand aside for the duration of a command.
type Terminal interface {
	// Size reports the console's dimensions in character cells.
	Size() (width, height uint)
	// Raw switches the console into raw mode — no local echo, no line
	// buffering, no signal keys — so that every keystroke reaches the command
	// rather than the shell tamp was started from. The returned function puts
	// the console back the way it was.
	Raw() (restore func(), err error)
}

// ExecRequest is one command to run inside an environment's bench.
type ExecRequest struct {
	Name string
	// Cmd is the command line, exactly as the user wrote it after --.
	Cmd []string
	// Raw drops every refusal, warning and hint below. It is the escape hatch
	// for a caller who means what they typed.
	Raw bool
	// Stdin is the caller's own input, attached to the command.
	Stdin io.Reader
	// Terminal is the console to hand the command, or nil when tamp is not
	// attached to one — a pipe, a CI job, a test.
	Terminal Terminal
}

// Exec runs a command inside the environment's bench container.
//
// It is the bridge an agent drives tamp through, so its contract is narrow
// and unchanging: the command lands on the bench as the bench's own user, its
// output is tamp's output, and its exit code is tamp's exit code. tamp
// never starts a stopped environment to serve it — that would hide minutes of
// containers coming up inside a command that promises to be boring.
func (m *Manager) Exec(ctx context.Context, req ExecRequest) error {
	e, err := m.resolve(req.Name)
	if err != nil {
		return err
	}
	if !req.Raw {
		if err := m.advise(e, req.Cmd); err != nil {
			return err
		}
	}

	if err := m.requireRunning(ctx, e, "so there is nothing to run the command in"); err != nil {
		return err
	}

	exec := engine.ExecRequest{
		Container: e.Resources.Container(FrappeService),
		Cmd:       req.Cmd,
		WorkDir:   frappe.BenchDir,
		User:      toolchain.User,
		Stdin:     req.Stdin,
		// The command's own output is the whole point of exec, so it goes to
		// tamp's streams unchanged — --quiet has no business dropping it.
		Stdout: m.Out.Out,
		Stderr: m.Out.Err,
	}
	if req.Terminal != nil {
		width, height := req.Terminal.Size()
		exec.TTY = true
		exec.Size = engine.ConsoleSize{Width: width, Height: height}

		restore, err := req.Terminal.Raw()
		if err != nil {
			return err
		}
		// The console is back to normal before tamp prints anything of its
		// own, including the error this call may return.
		defer restore()
	}

	if err := m.Engine.Exec(ctx, exec); err != nil {
		var exited *engine.ExitError
		if errors.As(err, &exited) {
			// The command's exit code is tamp's, whatever it is: a caller on
			// the other side of the bridge branches on what it asked for, not
			// on tamp. And the command has already said why on its own
			// streams, so tamp adds nothing to them.
			return exitcode.Reported(exitcode.Code(exited.Status))
		}
		return err
	}
	return nil
}

// isRunning reports whether one of the environment's services is up.
func isRunning(containers []engine.Container, service string) bool {
	for _, c := range containers {
		if c.Service == service {
			return c.Running
		}
	}
	return false
}

// advise says what tamp has to say about a command before running it, and
// returns an error for the ones it will not run at all.
//
// These rules are a courtesy rather than a sandbox: they read the command line
// tamp was handed, so anything wrapped in a shell passes untouched. That is
// the right trade for a bridge whose contract is to be boring — tamp catches
// the mistakes that are easy to make by accident and otherwise gets out of the
// way, and --raw removes even that.
func (m *Manager) advise(e *Environment, cmd []string) error {
	apps := filepath.Join(e.Dir, "apps")
	switch sub := benchSubcommand(cmd); {
	case sub == "start", sub == "serve":
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("%q is refused: this environment already runs the bench's processes", strings.Join(cmd, " ")),
			"use 'tamp restart' if they need reviving")

	case sub == "update":
		// A pull inside the container would write to the same .git the host is
		// writing to, from the far side of the sync.
		//
		// The rebuild is one bash -c rather than three commands joined by &&,
		// because && binds in the shell the user is typing at: written the
		// other way, only the first of the three would reach the container.
		return exitcode.New(exitcode.CodeFailed,
			"'bench update' is refused: git belongs to the host, and a container-side pull would fight it",
			fmt.Sprintf(`pull in %s yourself, then run: tamp exec %s -- bash -c "bench setup requirements && bench build && bench migrate"`, apps, e.Name()))

	case sub == "new-site":
		m.Out.Warn("'tamp site new' creates the site and its route together — 'bench new-site' leaves the routing to you")

	// Every git command, not only the ones that write: which files a git
	// command touches is a question tamp would have to ask the container to
	// answer, and a warning that is occasionally redundant beats one that
	// misses the pull it exists to catch.
	case len(cmd) > 0 && cmd[0] == "git":
		m.Out.Warn(fmt.Sprintf("the host owns git — run it in %s instead, so only one side of the sync ever writes to .git", apps))
	}
	return nil
}

// benchSubcommand names what a bench command line asks bench to do, or "" when
// it is not a bench command at all.
//
// The subcommand is taken to be the word straight after bench, which is where
// all four of the commands above put it. bench's own options follow their
// subcommand rather than precede it.
func benchSubcommand(cmd []string) string {
	if len(cmd) < 2 || cmd[0] != "bench" {
		return ""
	}
	return cmd[1]
}
