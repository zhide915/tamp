package doctor

import (
	"strings"
	"unicode/utf8"

	"github.com/zhide915/tamp/internal/ui"
)

// Print writes the report in tamp's output conventions: one ✓/!/✗ line per
// check, aligned so the details form a column, and each fix dim beneath the
// check it belongs to.
//
// Rendering lives here rather than in the command because it is a property of
// what a report is, and because down here it can be tested against a buffer.
func (r Report) Print(p *ui.Printer) {
	width := 0
	for _, c := range r.Checks {
		width = max(width, utf8.RuneCountInString(c.Name))
	}

	for _, c := range r.Checks {
		p.Result(c.Status.mark(), pad(c.Name, width)+"  "+c.Detail)
		if c.Fix != "" {
			// Indented past the name column so the eye reads the fix as
			// belonging to the check above it, not as another check.
			p.Note(strings.Repeat(" ", width+4) + "fix: " + c.Fix)
		}
	}
}

func (s Status) mark() ui.Mark {
	switch s {
	case Warn:
		return ui.MarkWarn
	case Fail:
		return ui.MarkFail
	}
	return ui.MarkOK
}

func pad(s string, width int) string {
	return s + strings.Repeat(" ", width-utf8.RuneCountInString(s))
}
