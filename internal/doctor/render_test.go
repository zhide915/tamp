package doctor_test

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/zhide915/tamp/internal/doctor"
	"github.com/zhide915/tamp/internal/ui"
)

func render(r doctor.Report) (stdout, stderr string) {
	var out, errOut bytes.Buffer
	r.Print(&ui.Printer{Out: &out, Err: &errOut})
	return out.String(), errOut.String()
}

// The report is what the user asked for, so all of it — failures included —
// goes to stdout. A doctor whose ✗ lines went to stderr would write an empty
// file when redirected, which is exactly when a user wants to keep it.
func TestPrintPutsTheWholeReportOnStdout(t *testing.T) {
	out, errOut := render(doctor.Report{Checks: []doctor.Check{
		{Name: "Docker", Status: doctor.Fail, Detail: "not answering", Fix: "start Docker"},
	}})

	if !strings.Contains(out, "✗ Docker") || !strings.Contains(out, "start Docker") {
		t.Errorf("stdout %q is missing the failing check or its fix", out)
	}
	if errOut != "" {
		t.Errorf("stderr = %q, want empty", errOut)
	}
}

// Details line up in a column so a report can be read down rather than across.
func TestPrintAlignsDetailsPastTheLongestName(t *testing.T) {
	out, _ := render(doctor.Report{Checks: []doctor.Check{
		{Name: "Docker", Status: doctor.Pass, Detail: "29.7.2"},
		{Name: "Docker Compose", Status: doctor.Pass, Detail: "v2.39.1"},
	}})

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2:\n%s", len(lines), out)
	}
	if a, b := column(lines[0], "29.7.2"), column(lines[1], "v2.39.1"); a != b {
		t.Errorf("details start at columns %d and %d, want them aligned:\n%s", a, b, out)
	}
}

// Alignment is measured in what the reader sees, not in bytes: a name with a
// non-ASCII character would otherwise push its own column out of true.
func TestPrintAlignsByCharactersNotBytes(t *testing.T) {
	out, _ := render(doctor.Report{Checks: []doctor.Check{
		{Name: "Café", Status: doctor.Pass, Detail: "one"},
		{Name: "Abcd", Status: doctor.Pass, Detail: "two"},
	}})

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if a, b := column(lines[0], "one"), column(lines[1], "two"); a != b {
		t.Errorf("details start at columns %d and %d, want them aligned:\n%s", a, b, out)
	}
}

// column is where the reader sees needle, counted in characters — measuring
// this in bytes would make the test agree with the bug it is looking for.
func column(line, needle string) int {
	return utf8.RuneCountInString(line[:strings.Index(line, needle)])
}

func TestPrintMarksEachStatusDistinctly(t *testing.T) {
	out, _ := render(doctor.Report{Checks: []doctor.Check{
		{Name: "a", Status: doctor.Pass, Detail: "fine"},
		{Name: "b", Status: doctor.Warn, Detail: "iffy"},
		{Name: "c", Status: doctor.Fail, Detail: "broken"},
	}})

	for mark, detail := range map[string]string{"✓": "fine", "!": "iffy", "✗": "broken"} {
		if !strings.Contains(out, mark) {
			t.Errorf("output %q is missing the %q mark for %q", out, mark, detail)
		}
	}
}

// A passing check has nothing to fix, and printing an empty fix line would put
// a blank gap between every row.
func TestPrintOmitsTheFixLineWhenThereIsNoFix(t *testing.T) {
	out, _ := render(doctor.Report{Checks: []doctor.Check{
		{Name: "Docker", Status: doctor.Pass, Detail: "29.7.2"},
	}})

	if lines := strings.Count(strings.TrimSpace(out), "\n"); lines != 0 {
		t.Errorf("output spans %d lines, want one:\n%s", lines+1, out)
	}
}

// The fix is dim, per tamp's convention for a follow-up line — but printed
// with Note, not Hint, so --quiet cannot take it away from a failure.
func TestPrintDimsTheFixAndKeepsItUnderQuiet(t *testing.T) {
	var out, errOut bytes.Buffer
	p := &ui.Printer{Out: &out, Err: &errOut, ColorOut: true, Quiet: true}

	doctor.Report{Checks: []doctor.Check{
		{Name: "Docker", Status: doctor.Fail, Detail: "not answering", Fix: "start Docker"},
	}}.Print(p)

	if !strings.Contains(out.String(), "start Docker") {
		t.Errorf("--quiet swallowed the fix:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "\x1b[2m") {
		t.Errorf("the fix is not dim:\n%q", out.String())
	}
}
