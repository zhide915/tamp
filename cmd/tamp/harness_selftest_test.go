package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A harness that stopped redirecting HOME would let every test write to the
// developer's real machine.
func TestSandboxIsolatesHomeAndWorkingDirectory(t *testing.T) {
	c := sandbox(t)

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	if home != c.home {
		t.Errorf("UserHomeDir() = %q, want the sandbox home %q", home, c.home)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if resolve(t, cwd) != resolve(t, c.dir) {
		t.Errorf("Getwd() = %q, want the sandbox dir %q", cwd, c.dir)
	}
}

// resolve follows symlinks: macOS temp dirs live under /var -> /private/var.
func resolve(t *testing.T, path string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", path, err)
	}
	return real
}
