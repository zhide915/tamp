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

// Labels compose stamps on what it owns; read instead of parsing CLI output.
const (
	composeProjectLabel = "com.docker.compose.project"
	composeServiceLabel = "com.docker.compose.service"
)

func (d *Docker) ComposeUp(ctx context.Context, p ComposeProject, out io.Writer) error {
	// --wait blocks until every declared healthcheck passes.
	return d.compose(ctx, p, out, "up", "--detach", "--wait")
}

func (d *Docker) ComposeStop(ctx context.Context, p ComposeProject, out io.Writer) error {
	return d.compose(ctx, p, out, "stop")
}

func (d *Docker) ComposeRestart(ctx context.Context, p ComposeProject, service string, out io.Writer) error {
	return d.compose(ctx, p, out, "restart", service)
}

func (d *Docker) ComposeDown(ctx context.Context, p ComposeProject, removal Removal, out io.Writer) error {
	// --remove-orphans cleans up services dropped from the regenerated file.
	args := []string{"down", "--remove-orphans"}
	if removal == RemoveVolumes {
		args = append(args, "--volumes")
	}
	return d.compose(ctx, p, out, args...)
}

// compose shells out to the docker compose v2 binary. All state-changing
// orchestration goes through it and the generated file, so tamp's actions
// match what the user could do by hand.
func (d *Docker) compose(ctx context.Context, p ComposeProject, out io.Writer, args ...string) error {
	addr, _, err := d.connect()
	if err != nil {
		return err
	}

	full := append([]string{"compose", "--project-name", p.Name, "--file", p.File}, args...)
	cmd := exec.CommandContext(ctx, "docker", full...)
	cmd.Dir = p.Dir
	// Pin compose to the endpoint tamp detected, not whatever it would pick.
	cmd.Env = append(os.Environ(), "DOCKER_HOST="+addr.Host)
	if out != nil {
		// Merge streams: compose puts progress on stderr, results on stdout.
		cmd.Stdout, cmd.Stderr = out, out
	}

	if err := cmd.Run(); err != nil {
		return composeError(args, err)
	}
	return nil
}

// composeError distinguishes a missing compose plugin from a failed
// operation; they carry different exit codes and fixes.
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

func (d *Docker) HasVolumes(ctx context.Context, project string) (bool, error) {
	_, api, err := d.connect()
	if err != nil {
		return false, err
	}
	res, err := api.VolumeList(ctx, client.VolumeListOptions{
		Filters: make(client.Filters).Add("label", composeProjectLabel+"="+project),
	})
	if err != nil {
		return false, exitcode.New(exitcode.CodeEngineUnavailable,
			fmt.Sprintf("cannot list Docker volumes: %v", rootCause(err)),
			"start Docker and try again")
	}
	return len(res.Items) > 0, nil
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
