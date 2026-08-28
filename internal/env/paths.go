package env

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/zhide915/tamp/internal/exitcode"
)

// HomeDirName is tamp's machine-global state directory, under the user's
// home.
const HomeDirName = ".tamp"

// StateDirName is the per-environment state directory: generated files the
// user should not commit, which is why it heads the generated .gitignore.
const StateDirName = ".tamp"

// Home resolves ~/.tamp, creating it on first use.
func Home() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot find your home directory: %v", err),
			"set HOME (or USERPROFILE on Windows) to a directory tamp can write to")
	}
	dir := filepath.Join(home, HomeDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot create %s: %v", dir, err),
			"check the permissions on your home directory")
	}
	return dir, nil
}

// StateDir is an environment's own .tamp directory.
func StateDir(dir string) string { return filepath.Join(dir, StateDirName) }
