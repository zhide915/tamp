// Package exitcode holds tamp's exit codes and the error that carries one.
// Scripts and agents branch on the numbers, so a code never changes its
// number or takes a new meaning. `tamp shell -- <command>` alone exits with
// the command's own code instead.
package exitcode

import (
	"errors"
	"fmt"
	"strings"
)

// Code is a tamp process exit code.
type Code int

const (
	// CodeOK: the command did what was asked.
	CodeOK Code = 0
	// CodeFailed: the command line was right, but the command failed,
	// including any conflict with the current state.
	CodeFailed Code = 1
	// CodeUsage: the command line is wrong, and only that. For example a
	// bare noun, a missing value, an unknown flag, an unsupported --frappe,
	// an invalid `new <path>`, no environment named, `open` with several
	// sites, or a bare `shell` without a terminal.
	CodeUsage Code = 2
	// CodeNotFound: the named environment, site, or app does not exist.
	CodeNotFound Code = 3
	// CodeDockerUnavailable: tamp cannot reach Docker.
	CodeDockerUnavailable Code = 4
	// CodeNeedsYes: the command removes data and was run without --yes.
	CodeNeedsYes Code = 5
)

// Error is a failure the user can act on: an exit code, a message, and a
// one-line fix.
type Error struct {
	Code Code
	Msg  string
	Fix  string
}

// New builds an Error.
func New(code Code, msg, fix string) *Error {
	return &Error{Code: code, Msg: msg, Fix: fix}
}

// Usage builds a CodeUsage Error.
func Usage(msg, fix string) *Error {
	return New(CodeUsage, msg, fix)
}

func (e *Error) Error() string {
	if e.Fix == "" {
		return e.Msg
	}
	// Windows ends its error text in a period, which would print as ".:".
	return strings.TrimSuffix(e.Msg, ".") + ": " + e.Fix
}

// Exit is the non-zero exit of a command that `tamp shell` ran. tamp exits
// with the command's own code, outside this contract, and prints nothing.
type Exit struct{ Code Code }

func (e *Exit) Error() string { return fmt.Sprintf("the command exited %d", e.Code) }

// Of maps err to its exit code: CodeOK for nil, the code of a wrapped
// *Error or *Exit, and CodeFailed for any other error.
func Of(err error) Code {
	if err == nil {
		return CodeOK
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	var exit *Exit
	if errors.As(err, &exit) {
		return exit.Code
	}
	return CodeFailed
}
