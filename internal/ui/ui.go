// Package ui centralizes tamp's terminal output: numbered progress steps,
// ✓/!/✗ result lines, and one-line errors, with color and quiet handled in
// one place.
package ui

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"golang.org/x/term"
)

const (
	ansiReset  = "\x1b[0m"
	ansiDim    = "\x1b[2m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
)

// Printer writes all of tamp's output. It never touches the process's real
// stdio, so commands are testable in-process.
type Printer struct {
	Out io.Writer
	Err io.Writer
	// Color is per stream: a shell redirects stdout and stderr independently,
	// and the stream still on a terminal keeps its color.
	ColorOut bool
	ColorErr bool
	// Quiet drops progress and hints; results and errors still print.
	Quiet bool
}

// NewPrinter decides color per stream from the environment and terminal-ness.
// --no-color is applied afterwards, via DisableColor.
func NewPrinter(out, err io.Writer, lookupEnv func(string) (string, bool)) *Printer {
	return &Printer{
		Out:      out,
		Err:      err,
		ColorOut: ShouldColor(out, lookupEnv),
		ColorErr: ShouldColor(err, lookupEnv),
	}
}

// DisableColor turns color off on both streams unconditionally.
func (p *Printer) DisableColor() {
	p.ColorOut, p.ColorErr = false, false
}

// Step prints one "[n/total] msg" progress line.
func (p *Printer) Step(n, total int, msg string) {
	if p.Quiet {
		return
	}
	fmt.Fprintln(p.Out, paint(p.ColorOut, ansiDim, fmt.Sprintf("[%d/%d] %s", n, total, msg)))
}

// Stepper counts steps itself, so callers never maintain [n/total] numbers by
// hand.
type Stepper struct {
	p     *Printer
	n     int
	total int
}

// Steps starts a numbered sequence of total steps.
func (p *Printer) Steps(total int) *Stepper {
	return &Stepper{p: p, total: total}
}

// Step prints the next step and returns the numbers used, for callers that
// mirror the line elsewhere.
func (s *Stepper) Step(msg string) (n, total int) {
	s.n++
	s.p.Step(s.n, s.total, msg)
	return s.n, s.total
}

// Hint prints a dim aside on stdout. Quiet drops it — unlike Note.
func (p *Printer) Hint(msg string) {
	if p.Quiet {
		return
	}
	fmt.Fprintln(p.Out, paint(p.ColorOut, ansiDim, msg))
}

// Note prints a dim line that is part of the answer — doctor's fix lines —
// so Quiet keeps it.
func (p *Printer) Note(msg string) {
	fmt.Fprintln(p.Out, paint(p.ColorOut, ansiDim, msg))
}

// Print writes a plain data line to stdout, never suppressed.
func (p *Printer) Print(msg string) {
	fmt.Fprintln(p.Out, msg)
}

// Stream is where a subprocess's own output goes: stdout, or discarded under
// Quiet, so shelling-out commands honor the flag automatically.
func (p *Printer) Stream() io.Writer {
	if p.Quiet {
		return io.Discard
	}
	return p.Out
}

// Table prints an aligned table to stdout, one Print per line.
func (p *Printer) Table(header []string, rows [][]string) {
	var table bytes.Buffer
	w := tabwriter.NewWriter(&table, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, strings.Join(header, "\t"))
	for _, row := range rows {
		fmt.Fprintln(w, strings.Join(row, "\t"))
	}
	if err := w.Flush(); err != nil {
		p.Warn(fmt.Sprintf("could not lay out the table: %v", err))
		return
	}
	for line := range strings.SplitSeq(strings.TrimRight(table.String(), "\n"), "\n") {
		p.Print(line)
	}
}

// Mark selects the ✓/!/✗ prefix on a result line.
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

// OK reports success on stdout.
func (p *Printer) OK(msg string) {
	p.Result(MarkOK, msg)
}

// Warn and Fail are diagnostics: they go to stderr so a piped stdout stays
// clean.
func (p *Printer) Warn(msg string) {
	p.mark(p.Err, p.ColorErr, MarkWarn, msg)
}

func (p *Printer) Fail(msg string) {
	p.mark(p.Err, p.ColorErr, MarkFail, msg)
}

// Result writes a marked line to stdout — for commands whose output is the
// outcomes themselves (doctor), where a ✗ is the answer, not a diagnostic.
func (p *Printer) Result(m Mark, msg string) {
	p.mark(p.Out, p.ColorOut, m, msg)
}

func (p *Printer) mark(w io.Writer, color bool, m Mark, msg string) {
	symbol, attr := m.render()
	fmt.Fprintln(w, paint(color, attr, symbol)+" "+msg)
}

// Error prints tamp's one-line "error:" form; a fix rides along in
// err.Error().
func (p *Printer) Error(err error) {
	fmt.Fprintln(p.Err, paint(p.ColorErr, ansiRed, "error:")+" "+err.Error())
}

func paint(on bool, attr, s string) string {
	if !on {
		return s
	}
	return attr + s + ansiReset
}

// ShouldColor allows color only on a terminal and only when NO_COLOR permits.
// --no-color comes later, via DisableColor, since flags parse after this runs.
// Pass os.LookupEnv outside tests.
func ShouldColor(w io.Writer, lookupEnv func(string) (string, bool)) bool {
	// Per https://no-color.org: set and non-empty disables color.
	if v, ok := lookupEnv("NO_COLOR"); ok && v != "" {
		return false
	}
	return isTerminal(w)
}

func isTerminal(w io.Writer) bool {
	// The IsTerminal seam lets tests claim terminal-ness without a pty.
	if tw, ok := w.(interface{ IsTerminal() bool }); ok {
		return tw.IsTerminal()
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}
