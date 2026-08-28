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

// column is where the reader sees needle, counted in characters — measuring
// this in bytes would make the test agree with the bug it is looking for.
func column(line, needle string) int {
	return utf8.RuneCountInString(line[:strings.Index(line, needle)])
}
