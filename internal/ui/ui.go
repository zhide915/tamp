// Package ui implements tamp's terminal output conventions — numbered
// progress steps, ✓/!/✗ result prefixes, one-line errors — in one place, so
// that every command speaks the same way without a TTY of its own.
package ui

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/term"
)

// ANSI attributes. tamp only ever uses these four.
const (
	ansiReset  = "\x1b[0m"
	ansiDim    = "\x1b[2m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
)

// Printer writes tamp's output. The zero value is usable once Out and Err are
// set; it never inspects the process's real stdio, which is what makes every
// command testable in-process.
type Printer struct {
	Out io.Writer
	Err io.Writer
	// ColorOut and ColorErr are decided per stream, because a shell redirects
	// stdout and stderr independently: `tamp ... > out.txt` leaves stderr on
	// the terminal and must keep its colour.
	ColorOut bool
	ColorErr bool
	// Quiet drops progress and hints. Results and errors always survive it.
	Quiet bool
}

// NewPrinter builds a Printer whose colour is already decided from the
// environment and from whether each stream is a terminal. A later --no-color
// only ever turns that off, via DisableColor.
func NewPrinter(out, err io.Writer, lookupEnv func(string) (string, bool)) *Printer {
	return &Printer{
		Out:      out,
		Err:      err,
		ColorOut: ShouldColor(out, lookupEnv),
		ColorErr: ShouldColor(err, lookupEnv),
	}
}

// DisableColor turns colour off on both streams, whatever the terminal says.
func (p *Printer) DisableColor() {
	p.ColorOut, p.ColorErr = false, false
}

// Step prints one numbered step of a long operation: "[3/9] pulling images".
func (p *Printer) Step(n, total int, msg string) {
	if p.Quiet {
		return
	}
	fmt.Fprintln(p.Out, paint(p.ColorOut, ansiDim, fmt.Sprintf("[%d/%d] %s", n, total, msg)))
}

// Hint prints a dim follow-up line under a result, e.g.
// "next: tamp site new <host>". Callers write the whole line, prefix included.
func (p *Printer) Hint(msg string) {
	if p.Quiet {
		return
	}
	fmt.Fprintln(p.Out, paint(p.ColorOut, ansiDim, msg))
}

// Note writes a dim line belonging to the result above it.
//
// It looks like Hint and differs in the one way that matters: --quiet keeps
// it. A hint is an aside tamp can afford to drop, while a note is part of the
// answer — doctor's fix lines are worthless if the failures they explain print
// without them.
func (p *Printer) Note(msg string) {
	fmt.Fprintln(p.Out, paint(p.ColorOut, ansiDim, msg))
}

// Print writes a plain result line to stdout: data the caller asked for, with
// no prefix and no colour. --quiet never suppresses it.
func (p *Printer) Print(msg string) {
	fmt.Fprintln(p.Out, msg)
}

// Mark is the ✓/!/✗ prefix on a result line.
type Mark int

const (
	MarkOK Mark = iota
	MarkWarn
	MarkFail
)

func (m Mark) render() (symbol, color string) {
	switch m {
	case MarkWarn:
		return "!", ansiYellow
	case MarkFail:
		return "✗", ansiRed
	}
	return "✓", ansiGreen
}

// OK reports success on stdout — the thing the caller asked for.
func (p *Printer) OK(msg string) {
	p.Result(MarkOK, msg)
}

// Warn reports something the user should know but that did not stop the
// operation. Diagnostics go to stderr so a piped stdout stays clean.
func (p *Printer) Warn(msg string) {
	p.mark(p.Err, p.ColorErr, MarkWarn, msg)
}

// Fail reports a step that did not succeed.
func (p *Printer) Fail(msg string) {
	p.mark(p.Err, p.ColorErr, MarkFail, msg)
}

// Result writes a marked line to stdout. It is for commands whose whole output
// is a list of outcomes — doctor's report — where a ✗ is the answer the user
// asked for rather than a diagnostic about the command, and so belongs on the
// stream they would redirect to keep it.
func (p *Printer) Result(m Mark, msg string) {
	p.mark(p.Out, p.ColorOut, m, msg)
}

func (p *Printer) mark(w io.Writer, color bool, m Mark, msg string) {
	symbol, attr := m.render()
	fmt.Fprintln(w, paint(color, attr, symbol)+" "+msg)
}

// Error prints tamp's terminal error line: one line, "error:" prefix, and —
// for errors that carry one — the fix, appended by the error itself.
func (p *Printer) Error(err error) {
	fmt.Fprintln(p.Err, paint(p.ColorErr, ansiRed, "error:")+" "+err.Error())
}

func paint(on bool, attr, s string) string {
	if !on {
		return s
	}
	return attr + s + ansiReset
}

// ShouldColor reports whether w may carry ANSI attributes: only on a terminal,
// and only when NO_COLOR does not forbid it. The --no-color flag is applied
// separately, by DisableColor, because flags are parsed after this is decided.
// Pass os.LookupEnv for lookupEnv outside tests.
func ShouldColor(w io.Writer, lookupEnv func(string) (string, bool)) bool {
	// https://no-color.org — set and non-empty disables colour.
	if v, ok := lookupEnv("NO_COLOR"); ok && v != "" {
		return false
	}
	return isTerminal(w)
}

func isTerminal(w io.Writer) bool {
	// Writers may declare terminal-ness themselves; that is the seam tests use
	// instead of allocating a pty.
	if tw, ok := w.(interface{ IsTerminal() bool }); ok {
		return tw.IsTerminal()
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}
