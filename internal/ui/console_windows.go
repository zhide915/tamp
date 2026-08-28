//go:build windows

package ui

import (
	"os"

	"golang.org/x/sys/windows"
)

// EnableColorSupport makes the process's console interpret ANSI escapes rather
// than echo them. Windows Terminal sets this mode already; legacy conhost does
// not, and without it tamp's colours would print as literal garbage on the
// platform tamp targets first.
//
// Call it once, from main, before anything is written.
func EnableColorSupport() {
	for _, f := range []*os.File{os.Stdout, os.Stderr} {
		h := windows.Handle(f.Fd())
		var mode uint32
		if err := windows.GetConsoleMode(h, &mode); err != nil {
			continue // not a console — nothing to enable
		}
		_ = windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
	}
}
