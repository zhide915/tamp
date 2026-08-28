package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The harness is a contract of its own: if it ever stopped redirecting HOME,
// every later command test would start writing to the developer's machine.
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

// macOS temp dirs are symlinked (/var -> /private/var), so compare real paths.
func resolve(t *testing.T, path string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", path, err)
	}
	return real
}
