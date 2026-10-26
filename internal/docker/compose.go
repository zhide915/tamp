package docker

import (
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
	return d.cli(ctx, filepath.Dir(p.File), nil, p.Out,
		"compose", "--project-name", p.Name, "--file", p.File, "up", "--detach", "--wait")
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
	// Both streams are progress: compose and build write it to stderr.
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return exitcode.New(exitcode.CodeDockerUnavailable,
				"the docker command is not on PATH",
				"install Docker Desktop, or add the docker CLI with its compose plugin to PATH")
		}
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("docker %s failed: %v", args[0], err),
			"fix what the docker output above names")
	}
	return nil
}
