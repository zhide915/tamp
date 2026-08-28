package ui_test

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/ui"
)

func newPrinter() (*ui.Printer, *bytes.Buffer, *bytes.Buffer) {
	var out, errOut bytes.Buffer
	return &ui.Printer{Out: &out, Err: &errOut}, &out, &errOut
}

func TestStepIsNumberedOnStdout(t *testing.T) {
	p, out, _ := newPrinter()
	p.Step(1, 9, "detecting engine")
	if got, want := out.String(), "[1/9] detecting engine\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestResultPrefixes(t *testing.T) {
	p, out, errOut := newPrinter()
	p.OK("erp15 ready")
	p.Warn("port 80 is taken")
	p.Fail("bench init failed")

	if got, want := out.String(), "✓ erp15 ready\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	// Warnings and failures are diagnostics, so they belong on stderr where
	// they never pollute a piped stdout.
	if got, want := errOut.String(), "! port 80 is taken\n✗ bench init failed\n"; got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

func TestErrorPrintsOneLineWithTheFix(t *testing.T) {
	p, out, errOut := newPrinter()
	p.Error(exitcode.New(exitcode.CodeNotFound, "environment 'x' not found", "see 'tamp list'"))

	if got, want := errOut.String(), "error: environment 'x' not found — see 'tamp list'\n"; got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want empty", out.String())
	}
}

func TestErrorHandlesPlainErrors(t *testing.T) {
	p, _, errOut := newPrinter()
	p.Error(errors.New("boom"))
	if got, want := errOut.String(), "error: boom\n"; got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

func TestQuietSuppressesProgressButNotResults(t *testing.T) {
	p, out, errOut := newPrinter()
	p.Quiet = true
	p.Step(1, 2, "detecting engine")
	p.Hint("next: tamp site new <host>")
	p.OK("erp15 ready")
	p.Warn("port 80 is taken")
	p.Fail("bench init failed")
	p.Error(errors.New("boom"))

	if got, want := out.String(), "✓ erp15 ready\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if got, want := errOut.String(), "! port 80 is taken\n✗ bench init failed\nerror: boom\n"; got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

func TestColorWrapsOutputInAnsiOnlyWhenEnabled(t *testing.T) {
	p, out, _ := newPrinter()
	p.OK("plain")
	if strings.Contains(out.String(), "\x1b[") {
		t.Errorf("stdout = %q, want no escape sequences", out.String())
	}

	p.ColorOut = true
	out.Reset()
	p.OK("colored")
	if !strings.Contains(out.String(), "\x1b[") {
		t.Errorf("stdout = %q, want escape sequences", out.String())
	}
	if !strings.Contains(out.String(), "colored") {
		t.Errorf("stdout = %q, want the message intact", out.String())
	}
}

// fakeTerminal is a writer that claims to be a terminal, so the colour rules
// can be exercised without allocating a real pty.
type fakeTerminal struct{ bytes.Buffer }

func (fakeTerminal) IsTerminal() bool { return true }

func TestShouldColorOnlyOnATerminal(t *testing.T) {
	for name, tc := range map[string]struct {
		w    io.Writer
		env  map[string]string
		want bool
	}{
		"terminal":               {&fakeTerminal{}, nil, true},
		"pipe":                   {&bytes.Buffer{}, nil, false},
		"terminal with NO_COLOR": {&fakeTerminal{}, map[string]string{"NO_COLOR": "1"}, false},
		// An empty NO_COLOR means "unset" per the NO_COLOR convention.
		"terminal with empty NO_COLOR": {&fakeTerminal{}, map[string]string{"NO_COLOR": ""}, true},
	} {
		if got := ui.ShouldColor(tc.w, fakeEnv(tc.env)); got != tc.want {
			t.Errorf("%s: ShouldColor() = %v, want %v", name, got, tc.want)
		}
	}
}

func fakeEnv(pairs map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		v, ok := pairs[key]
		return v, ok
	}
}

// stdout and stderr are redirected independently, so a run with a piped stdout
// and a live stderr must still colour its error lines.
func TestColorIsDecidedPerStream(t *testing.T) {
	var piped bytes.Buffer
	tty := &fakeTerminal{}
	p := ui.NewPrinter(&piped, tty, fakeEnv(nil))

	if p.ColorOut {
		t.Error("ColorOut = true for a piped stdout, want false")
	}
	if !p.ColorErr {
		t.Error("ColorErr = false for a terminal stderr, want true")
	}

	p.OK("done")
	p.Fail("broken")
	if strings.Contains(piped.String(), "\x1b[") {
		t.Errorf("stdout = %q, want no escape sequences", piped.String())
	}
	if !strings.Contains(tty.String(), "\x1b[") {
		t.Errorf("stderr = %q, want escape sequences", tty.String())
	}
}

// --no-color overrides a terminal on both streams.
func TestDisableColorSilencesBothStreams(t *testing.T) {
	out, errOut := &fakeTerminal{}, &fakeTerminal{}
	p := ui.NewPrinter(out, errOut, fakeEnv(nil))
	p.DisableColor()

	p.OK("done")
	p.Fail("broken")
	if strings.Contains(out.String()+errOut.String(), "\x1b[") {
		t.Errorf("output = %q %q, want no escape sequences", out.String(), errOut.String())
	}
}

func TestPrintIsPlainAndSurvivesQuiet(t *testing.T) {
	p, out, _ := newPrinter()
	p.ColorOut, p.Quiet = true, true
	p.Print("tamp 1.2.3")

	if got, want := out.String(), "tamp 1.2.3\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

// doctor's ✗ lines are the answer to the question the user asked, not a
// diagnostic about tamp failing, so they go to stdout — `tamp doctor >
// report.txt` has to capture the failures, which is the whole point of it.
func TestResultWritesMarkedLinesToStdout(t *testing.T) {
	p, out, errOut := newPrinter()
	p.Result(ui.MarkOK, "Docker         29.7.2")
	p.Result(ui.MarkWarn, "Router         not running")
	p.Result(ui.MarkFail, "Docker         not answering")

	want := "✓ Docker         29.7.2\n! Router         not running\n✗ Docker         not answering\n"
	if got := out.String(); got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if errOut.Len() != 0 {
		t.Errorf("stderr = %q, want empty", errOut.String())
	}
}

// A report is a result, so --quiet must not swallow it; there is nothing left
// to print if it does.
func TestResultSurvivesQuiet(t *testing.T) {
	p, out, _ := newPrinter()
	p.Quiet = true
	p.Result(ui.MarkFail, "Docker not answering")

	if out.Len() == 0 {
		t.Error("stdout is empty under --quiet, want the result line")
	}
}

func TestResultIsColouredLikeItsStderrTwin(t *testing.T) {
	p, out, _ := newPrinter()
	p.ColorOut = true
	p.Result(ui.MarkFail, "Docker not answering")

	if got, want := out.String(), "\x1b[31m✗\x1b[0m Docker not answering\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

// Note and Hint look identical; the difference is that a note is part of the
// answer. doctor's fix lines print under a failing check, and a --quiet that
// swallowed them would leave the user with a failure and no remedy.
func TestNoteIsDimOnStdoutAndSurvivesQuiet(t *testing.T) {
	p, out, errOut := newPrinter()
	p.ColorOut = true
	p.Quiet = true
	p.Note("fix: start Docker Desktop")

	if got, want := out.String(), "\x1b[2mfix: start Docker Desktop\x1b[0m\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	if errOut.Len() != 0 {
		t.Errorf("stderr = %q, want empty", errOut.String())
	}
}

// The distinction only means something if Hint really is the droppable one.
func TestHintIsStillSuppressedByQuiet(t *testing.T) {
	p, out, _ := newPrinter()
	p.Quiet = true
	p.Hint("next: tamp site new <host>")

	if out.Len() != 0 {
		t.Errorf("stdout = %q, want empty", out.String())
	}
}
