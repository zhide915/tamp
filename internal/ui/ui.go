// Package ui is tamp's terminal output: results on stdout, diagnostics on
// stderr, with color and --quiet decided in one place.
package ui

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/term"
)

const (
	ansiReset  = "\x1b[0m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
)

// Printer writes all of tamp's output. It never touches the process's own
// stdio, so commands run in-process under tests.
type Printer struct {
	Out io.Writer
	Err io.Writer
	// Color is per stream: a shell redirects stdout and stderr separately,
	// and the stream still on a terminal keeps its color.
	ColorOut bool
	ColorErr bool
	// Quiet drops progress; results, warnings and errors still print.
	Quiet bool
}

// NewPrinter decides color per stream from NO_COLOR and whether the stream
// is a terminal. --no-color applies later, through DisableColor.
func NewPrinter(out, err io.Writer, lookupEnv func(string) (string, bool)) *Printer {
	return &Printer{
		Out:      out,
		Err:      err,
		ColorOut: shouldColor(out, lookupEnv),
		ColorErr: shouldColor(err, lookupEnv),
	}
}

// DisableColor turns color off on both streams.
func (p *Printer) DisableColor() {
	p.ColorOut, p.ColorErr = false, false
}

// Print writes a plain result line to stdout.
func (p *Printer) Print(msg string) {
	fmt.Fprintln(p.Out, msg)
}

// OK writes a "✓" result line to stdout.
func (p *Printer) OK(msg string) {
	fmt.Fprintln(p.Out, paint(p.ColorOut, ansiGreen, "✓")+" "+msg)
}

// Caution writes a "!" result line to stdout: a check that warns.
func (p *Printer) Caution(msg string) {
	fmt.Fprintln(p.Out, paint(p.ColorOut, ansiYellow, "!")+" "+msg)
}

// Fail writes a "✗" result line to stdout: a check that fails.
func (p *Printer) Fail(msg string) {
	fmt.Fprintln(p.Out, paint(p.ColorOut, ansiRed, "✗")+" "+msg)
}

// Step writes a progress line to stderr; --quiet drops it.
func (p *Printer) Step(msg string) {
	if !p.Quiet {
		fmt.Fprintln(p.Err, msg)
	}
}

// Progress is the stream for the output of the tools tamp runs, such as
// docker and bench: stderr, or nothing under --quiet.
func (p *Printer) Progress() io.Writer {
	if p.Quiet {
		return io.Discard
	}
	return p.Err
}

// Warn writes a "warning:" line to stderr. --quiet keeps it: a warning
// names something that still needs the user.
func (p *Printer) Warn(msg string) {
	fmt.Fprintln(p.Err, paint(p.ColorErr, ansiYellow, "warning:")+" "+msg)
}

// Error writes the one-line "error:" form to stderr. An exitcode.Error
// carries its fix in err.Error().
func (p *Printer) Error(err error) {
	fmt.Fprintln(p.Err, paint(p.ColorErr, ansiRed, "error:")+" "+err.Error())
}

func paint(on bool, attr, s string) string {
	if !on {
		return s
	}
	return attr + s + ansiReset
}

// shouldColor allows color only on a terminal, and only while NO_COLOR is
// unset or empty, as https://no-color.org asks.
func shouldColor(w io.Writer, lookupEnv func(string) (string, bool)) bool {
	if v, ok := lookupEnv("NO_COLOR"); ok && v != "" {
		return false
	}
	return IsTerminal(w)
}

// IsTerminal reports whether a stream, input or output, is a terminal.
func IsTerminal(stream any) bool {
	// Tests claim a terminal through this method, without a pty.
	if tw, ok := stream.(interface{ IsTerminal() bool }); ok {
		return tw.IsTerminal()
	}
	f, ok := stream.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}
