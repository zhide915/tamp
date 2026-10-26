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
	ansiReset = "\x1b[0m"
	ansiRed   = "\x1b[31m"
	ansiGreen = "\x1b[32m"
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
	// Quiet drops progress; results and errors still print.
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
	return isTerminal(w)
}

func isTerminal(w io.Writer) bool {
	// Tests claim a terminal through this method, without a pty.
	if tw, ok := w.(interface{ IsTerminal() bool }); ok {
		return tw.IsTerminal()
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}
