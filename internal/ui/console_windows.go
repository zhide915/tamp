//go:build windows

package ui

import (
	"os"

	"golang.org/x/sys/windows"
)

// EnableColorSupport turns on VT processing so legacy conhost interprets ANSI
// escapes instead of echoing them; Windows Terminal already does. Call once
// from main, before any output.
func EnableColorSupport() {
	for _, f := range []*os.File{os.Stdout, os.Stderr} {
		h := windows.Handle(f.Fd())
		var mode uint32
		if err := windows.GetConsoleMode(h, &mode); err != nil {
			continue // not a console
		}
		_ = windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
	}
}
