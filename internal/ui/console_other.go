//go:build !windows

package ui

// EnableColorSupport is a no-op: every other console tamp supports interprets
// ANSI escapes without being asked.
func EnableColorSupport() {}
