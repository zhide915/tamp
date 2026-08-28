package doctor

import (
	"strings"
	"unicode/utf8"

	"github.com/zhide915/tamp/internal/ui"
)

// Print renders one marked line per check with details aligned in a column,
// and each fix dim beneath its check. It lives on Report so it renders into
// any Printer, buffers included.
func (r Report) Print(p *ui.Printer) {
	width := 0
	for _, c := range r.Checks {
		width = max(width, utf8.RuneCountInString(c.Name))
	}

	for _, c := range r.Checks {
		p.Result(c.Status.mark(), pad(c.Name, width)+"  "+c.Detail)
		if c.Fix != "" {
			// Indented so the fix reads as part of the check above, not as
			// another check.
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
