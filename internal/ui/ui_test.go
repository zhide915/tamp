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

// Warn and Fail are diagnostics and belong on stderr.
func TestResultPrefixes(t *testing.T) {
	p, out, errOut := newPrinter()
	p.OK("erp15 ready")
	p.Warn("port 80 is taken")
	p.Fail("bench init failed")

	if got, want := out.String(), "✓ erp15 ready\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
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

// fakeTerminal claims terminal-ness so color rules run without a pty.
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
		// Empty NO_COLOR counts as unset.
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

// A piped stdout must not strip color from a stderr still on the terminal.
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

// Notes carry doctor's fix lines; --quiet must not drop them.
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
