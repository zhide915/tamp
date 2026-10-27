package docker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/zhide915/tamp/internal/exitcode"
)

func (d *Daemon) ComposeUp(ctx context.Context, p Project) error {
	// --wait blocks until every healthcheck passes.
	return upError(d.cli(ctx, filepath.Dir(p.File), nil, p.Out,
		"compose", "--project-name", p.Name, "--file", p.File, "up", "--detach", "--wait"), p.Publishes)
}

// upError reads a failed compose up for a taken port only when the project
// publishes one: a build log can say "address already in use" too.
func upError(err error, publishes bool) error {
	var failed *cliError
	if publishes && errors.As(err, &failed) && portTaken(failed.output) {
		return fmt.Errorf("docker compose failed: %w", ErrPortTaken)
	}
	return err
}

func (d *Daemon) ComposeVersion(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "docker", "compose", "version", "--short").CombinedOutput()
	switch {
	case errors.Is(err, exec.ErrNotFound):
		return "", exitcode.New(exitcode.CodeDockerUnavailable,
			"the docker command is not on PATH",
			"install Docker Desktop, or add the docker CLI with its compose plugin to PATH")
	case err != nil:
		return "", exitcode.New(exitcode.CodeFailed,
			"docker compose does not run: "+said(out, err),
			"install Docker Desktop, or the Docker Compose plugin")
	}
	return strings.TrimSpace(string(out)), nil
}

// said is the last line a failed command printed, or its error.
func said(out []byte, err error) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if last := strings.TrimSpace(lines[len(lines)-1]); last != "" {
		return last
	}
	return err.Error()
}

func (d *Daemon) BuildImage(ctx context.Context, b ImageBuild) error {
	args := []string{"build", "--tag", b.Tag}
	for _, k := range sortedKeys(b.Args) {
		args = append(args, "--build-arg", k+"="+b.Args[k])
	}
	// "-" reads the Dockerfile from stdin and sends no build context.
	args = append(args, "-")
	return d.cli(ctx, "", strings.NewReader(b.Dockerfile), b.Out, args...)
}

// cli runs the docker CLI against the endpoint tamp detected, so it acts on
// the same engine as the API calls.
func (d *Daemon) cli(ctx context.Context, dir string, stdin io.Reader, out io.Writer, args ...string) error {
	addr, _, err := d.connect()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "DOCKER_HOST="+addr.Host)
	cmd.Stdin = stdin
	if out == nil {
		out = io.Discard
	}
	// Both streams are progress: compose and build write it to stderr. The
	// copy is kept for a caller that reads the failure.
	var seen bytes.Buffer
	cmd.Stdout = io.MultiWriter(out, &seen)
	cmd.Stderr = cmd.Stdout
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return exitcode.New(exitcode.CodeDockerUnavailable,
				"the docker command is not on PATH",
				"install Docker Desktop, or add the docker CLI with its compose plugin to PATH")
		}
		return &cliError{
			err: exitcode.New(exitcode.CodeFailed, fmt.Sprintf("docker %s failed: %v", args[0], err),
				"fix what the docker output above names"),
			output: seen.String(),
		}
	}
	return nil
}

// cliError is a failed docker command with what it printed.
type cliError struct {
	err    *exitcode.Error
	output string
}

func (e *cliError) Error() string { return e.err.Error() }
func (e *cliError) Unwrap() error { return e.err }

// portTaken reads Docker's wording for a publish that could not bind: the
// engine's own, and Docker Desktop's, whose port forwarder binds on the host.
func portTaken(output string) bool {
	for _, s := range []string{"port is already allocated", "address already in use", "ports are not available"} {
		if strings.Contains(output, s) {
			return true
		}
	}
	return false
}
