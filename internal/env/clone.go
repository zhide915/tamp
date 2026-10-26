package env

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/zhide915/tamp/internal/exitcode"
)

// FrappeURL is where Frappe is cloned from.
const FrappeURL = "https://github.com/frappe/frappe"

// AppsDir is the folder of an environment's apps, on the host.
const AppsDir = "apps"

// cloneApp clones url at branch into apps/<name> with host git, as bench
// get-app clones, unless the folder exists: tamp never touches a clone.
func cloneApp(ctx context.Context, dir, name, url, branch string, out io.Writer) error {
	target := filepath.Join(dir, AppsDir, name)
	if _, err := os.Stat(target); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return exitcode.New(exitcode.CodeFailed, fmt.Sprintf("cannot read %s: %v", target, err),
			"check that you can read the directory")
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return exitcode.New(exitcode.CodeFailed, fmt.Sprintf("cannot create %s: %v", filepath.Dir(target), err),
			"check that you can write to the directory")
	}

	args := []string{"clone", "--branch", branch, "--depth", "1", "--origin", "upstream"}
	if runtime.GOOS == "windows" {
		// Kept in the clone's config, so line endings survive the trip into
		// the Linux container on every later checkout too.
		args = append(args, "--config", "core.autocrlf=false")
	}
	args = append(args, "--", url, target)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return exitcode.New(exitcode.CodeFailed, "git is not on PATH",
				"install git")
		}
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot clone %s at %s: %v", url, branch, err),
			"fix what git says above")
	}
	return nil
}
