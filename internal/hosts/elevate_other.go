//go:build !windows

package hosts

import (
	"context"
	"io"
	"os"
	"os/exec"
)

// Elevate re-runs tamp under sudo. Its stdin is the real terminal because
// sudo's password prompt has nowhere else to read from; everything the child
// writes goes to the caller's stream, like any other subprocess tamp runs.
func Elevate(ctx context.Context, exe string, args []string, out io.Writer) error {
	cmd := exec.CommandContext(ctx, "sudo", append([]string{"--", exe}, args...)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		return elevationFailed(err)
	}
	return nil
}
