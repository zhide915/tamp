// Package exitcode defines tamp's process exit contract and the error type
// that carries it.
//
// The numbers are part of tamp's public interface — humans and agents branch
// on them — so they are additive-only: never renumber, never reuse.
package exitcode

import "errors"

// Code is the value tamp exits with.
type Code int

const (
	CodeOK                   Code = 0 // the command did what was asked
	CodeFailed               Code = 1 // the operation was understood but did not succeed
	CodeUsage                Code = 2 // the command line itself was wrong
	CodeNotFound             Code = 3 // the named environment or site does not exist
	CodeEngineUnavailable    Code = 4 // no Docker socket, or compose v2 missing
	CodeConfirmationRequired Code = 5 // a destructive action was asked for without --yes
)

// Error is a tamp failure that knows both how to exit and how to be fixed.
//
// Fix is not decoration: tamp's error contract is one line that always tells
// the user what to do next, so callers are expected to supply it.
type Error struct {
	Code Code
	Msg  string
	Fix  string
}

// New builds an Error. Pass an empty Fix only when there is genuinely no
// action the caller could take.
func New(code Code, msg, fix string) *Error {
	return &Error{Code: code, Msg: msg, Fix: fix}
}

// Usage is the shorthand for a malformed command line.
func Usage(msg, fix string) *Error {
	return New(CodeUsage, msg, fix)
}

func (e *Error) Error() string {
	if e.Fix == "" {
		return e.Msg
	}
	return e.Msg + " — " + e.Fix
}

// Of reports the exit code an error should produce. Anything that is not an
// *Error is an operation that failed for a reason tamp did not classify.
func Of(err error) Code {
	if err == nil {
		return CodeOK
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return CodeFailed
}
