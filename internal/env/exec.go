package env

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/frappe"
	"github.com/zhide915/tamp/internal/syncer"
	"github.com/zhide915/tamp/internal/toolchain"
)

// Terminal is the console tamp is attached to. An interface because driving a
// real console belongs to cmd/, not here.
type Terminal interface {
	// Size reports the console's dimensions in character cells.
	Size() (width, height uint)
	// Raw switches the console into raw mode so every keystroke reaches the
	// command; the returned function restores it.
	Raw() (restore func(), err error)
}

// ExecRequest is one command to run inside an environment's bench.
type ExecRequest struct {
	Name string
	// Cmd is the command line exactly as written after --.
	Cmd []string
	// Raw skips advise: no refusals, no warnings.
	Raw   bool
	Stdin io.Reader
	// Terminal is nil when tamp is not attached to a console — a pipe, CI, a
	// test.
	Terminal Terminal
}

// Exec runs a command in the bench container as the bench user; its output
// and exit code become tamp's. A stopped environment is never auto-started —
// that would hide minutes of startup inside a command meant to be boring.
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
		// The command's output is the point of exec — --quiet must not drop it.
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
		// Restore before tamp prints anything, including this call's error.
		defer restore()
	}

	if err := m.Engine.Exec(ctx, exec); err != nil {
		var exited *engine.ExitError
		if errors.As(err, &exited) {
			// Pass the exit code through unchanged; the command already said
			// why on its own streams.
			return exitcode.Reported(exitcode.Code(exited.Status))
		}
		return err
	}
	return nil
}

func isRunning(containers []engine.Container, service string) bool {
	for _, c := range containers {
		if c.Service == service {
			return c.Running
		}
	}
	return false
}

// advise warns about or refuses commands that fight tamp's model. It reads
// only the literal command line — a courtesy, not a sandbox — and --raw skips
// it entirely.
func (m *Manager) advise(e *Environment, cmd []string) error {
	apps := syncer.AppsDir(e.Dir)
	switch sub := benchSubcommand(cmd); {
	case sub == "start", sub == "serve":
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("%q is refused: this environment already runs the bench's processes", strings.Join(cmd, " ")),
			"use 'tamp restart' if they need reviving")

	case sub == "update":
		// A container-side pull writes to the same .git the host is syncing.
		// The hint is one bash -c so the && chain runs in the container rather
		// than binding in the user's shell.
		return exitcode.New(exitcode.CodeFailed,
			"'bench update' is refused: git belongs to the host, and a container-side pull would fight it",
			fmt.Sprintf(`pull in %s yourself, then run: tamp exec %s -- bash -c "bench setup requirements && bench build && bench migrate"`, apps, e.Name()))

	case sub == "new-site":
		m.Out.Warn("'tamp site new' creates the site and its route together — 'bench new-site' leaves the routing to you")

	// All git commands, not only writers: telling which files git touches
	// would mean asking the container.
	case len(cmd) > 0 && cmd[0] == "git":
		m.Out.Warn(fmt.Sprintf("the host owns git — run it in %s instead, so only one side of the sync ever writes to .git", apps))
	}
	return nil
}

// benchSubcommand returns the word after "bench", or "" for a non-bench
// command; bench's options follow their subcommand.
func benchSubcommand(cmd []string) string {
	if len(cmd) < 2 || cmd[0] != "bench" {
		return ""
	}
	return cmd[1]
}
