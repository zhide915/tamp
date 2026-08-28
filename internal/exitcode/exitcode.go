// Package exitcode carries tamp's exit codes on errors. The numbers are a
// public contract — scripts and agents branch on them — so they are
// additive-only: never renumbered, never reused.
package exitcode

import "errors"

// Code is a tamp process exit code.
type Code int

const (
	CodeOK                   Code = 0 // success
	CodeFailed               Code = 1 // understood, but the operation failed
	CodeUsage                Code = 2 // bad command line
	CodeNotFound             Code = 3 // named environment or site does not exist
	CodeEngineUnavailable    Code = 4 // no Docker socket, or compose v2 missing
	CodeConfirmationRequired Code = 5 // destructive action without --yes
)

// Error pairs an exit code with a message and a fix. Fix names the user's next
// action; leave it empty only when there is none.
type Error struct {
	Code Code
	Msg  string
	Fix  string

	// Set only by Reported.
	reported bool
}

func New(code Code, msg, fix string) *Error {
	return &Error{Code: code, Msg: msg, Fix: fix}
}

// Reported marks a failure the command has already explained in its own
// output. The mark is explicit — not just an empty message — so an ordinary
// Error without text still gets printed.
func Reported(code Code) *Error {
	return &Error{Code: code, reported: true}
}

// Silent reports whether err came from Reported and must not be printed again.
func Silent(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.reported
}

// Usage builds a CodeUsage Error.
func Usage(msg, fix string) *Error {
	return New(CodeUsage, msg, fix)
}

func (e *Error) Error() string {
	if e.Fix == "" {
		return e.Msg
	}
	return e.Msg + " — " + e.Fix
}

// Of maps err to an exit code: CodeOK for nil, a wrapped *Error's own code,
// CodeFailed for anything unclassified.
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
