package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/zhide915/tamp/internal/exitcode"
)

// composeProjectLabel and composeServiceLabel are how compose marks what it
// owns. tamp reads them rather than parsing `docker compose ps`, so that
// finding an environment's containers does not depend on a CLI output format.
const (
	composeProjectLabel = "com.docker.compose.project"
	composeServiceLabel = "com.docker.compose.service"
)

func (d *Docker) ComposeUp(ctx context.Context, p ComposeProject, out io.Writer) error {
	// --wait holds until every healthcheck in the generated file passes, so a
	// create that returns has actually produced a working environment rather
	// than five containers that are still deciding.
	return d.compose(ctx, p, out, "up", "--detach", "--wait")
}

func (d *Docker) ComposeStop(ctx context.Context, p ComposeProject, out io.Writer) error {
	return d.compose(ctx, p, out, "stop")
}

// ComposeRestart restarts one service in place. The container keeps the
// command it was created with, which is the point: tamp's bench container
// decides at boot whether there is a bench to run, and restarting it is how
// that decision gets taken again once tamp has made one.
func (d *Docker) ComposeRestart(ctx context.Context, p ComposeProject, service string, out io.Writer) error {
	return d.compose(ctx, p, out, "restart", service)
}

func (d *Docker) ComposeDown(ctx context.Context, p ComposeProject, removal Removal, out io.Writer) error {
	// --remove-orphans clears containers left behind by an older generated
	// file: tamp's compose file is rewritten on every start, and a service
	// that has since been dropped is otherwise never cleaned up.
	args := []string{"down", "--remove-orphans"}
	if removal == RemoveVolumes {
		args = append(args, "--volumes")
	}
	return d.compose(ctx, p, out, args...)
}

// compose runs the real `docker compose` v2 binary against the detected
// endpoint. All state-changing orchestration goes through it and the
// generated file, so what tamp does to an environment is exactly what the
// user could do by hand in that directory.
func (d *Docker) compose(ctx context.Context, p ComposeProject, out io.Writer, args ...string) error {
	addr, _, err := d.connect()
	if err != nil {
		return err
	}

	full := append([]string{"compose", "--project-name", p.Name, "--file", p.File}, args...)
	cmd := exec.CommandContext(ctx, "docker", full...)
	cmd.Dir = p.Dir
	// Compose resolves the endpoint itself unless told; tamp tells it, so
	// that the socket it acts on is the one tamp reported in doctor.
	cmd.Env = append(os.Environ(), "DOCKER_HOST="+addr.Host)
	if out != nil {
		// Compose reports progress on stderr and results on stdout; tamp
		// wants the whole narration in one place.
		cmd.Stdout, cmd.Stderr = out, out
	}

	if err := cmd.Run(); err != nil {
		return composeError(args, err)
	}
	return nil
}

// composeError separates "compose is not installed" from "compose ran and the
// operation failed", because those are different exit codes and different
// fixes.
func composeError(args []string, err error) error {
	if errors.Is(err, exec.ErrNotFound) {
		return exitcode.New(exitcode.CodeEngineUnavailable,
			"'docker compose' is not available",
			"install Docker Desktop, or add the compose v2 plugin to your docker CLI")
	}
	return exitcode.New(exitcode.CodeFailed,
		fmt.Sprintf("docker compose %s failed: %v", strings.Join(args, " "), err),
		"the compose output above says why")
}

func (d *Docker) Containers(ctx context.Context, project string) ([]Container, error) {
	_, api, err := d.connect()
	if err != nil {
		return nil, err
	}

	res, err := api.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: make(client.Filters).Add("label", composeProjectLabel+"="+project),
	})
	if err != nil {
		return nil, exitcode.New(exitcode.CodeEngineUnavailable,
			fmt.Sprintf("cannot list Docker containers: %v", rootCause(err)),
			"start Docker and try again")
	}

	out := make([]Container, 0, len(res.Items))
	for _, item := range res.Items {
		out = append(out, Container{
			Service: item.Labels[composeServiceLabel],
			Running: item.State == container.StateRunning,
		})
	}
	return out, nil
}
