//go:build !windows

package ui

// EnableColorSupport is a no-op outside Windows.
func EnableColorSupport() {}
