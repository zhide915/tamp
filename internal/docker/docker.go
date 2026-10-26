// Package docker is tamp's boundary with Docker. Tests replace it with Fake;
// every other package runs for real.
package docker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/moby/moby/client"
	"github.com/zhide915/tamp/internal/exitcode"
)

// Client is all that tamp asks of Docker. An unreachable Docker is an
// *exitcode.Error with CodeDockerUnavailable, which a command returns as is.
type Client interface {
	// Ping confirms that the Docker daemon answers.
	Ping(ctx context.Context) error
}

// Daemon is the real Client.
type Daemon struct {
	detector Detector

	// Detection runs once per process, on first use, so `tamp --help` works
	// on a machine without Docker.
	once sync.Once
	addr Address
	api  *client.Client
	err  error
}

// New returns the real Client.
func New() *Daemon {
	return &Daemon{detector: NewDetector(os.LookupEnv)}
}

func (d *Daemon) connect() (Address, *client.Client, error) {
	d.once.Do(func() {
		d.addr, d.err = d.detector.Detect()
		if d.err != nil {
			return
		}
		// No pinned API version: the client negotiates with the user's engine.
		d.api, d.err = client.New(client.WithHost(d.addr.Host))
		if d.err != nil {
			d.err = exitcode.New(exitcode.CodeDockerUnavailable,
				fmt.Sprintf("cannot use the Docker endpoint %s: %v", d.addr, d.err),
				"check that DOCKER_HOST or `docker context ls` names an endpoint tamp can reach")
		}
	})
	return d.addr, d.api, d.err
}

func (d *Daemon) Ping(ctx context.Context) error {
	addr, api, err := d.connect()
	if err != nil {
		return err
	}
	if _, err := api.ServerVersion(ctx, client.ServerVersionOptions{}); err != nil {
		return exitcode.New(exitcode.CodeDockerUnavailable,
			fmt.Sprintf("Docker is not answering at %s: %v", addr, rootCause(err)),
			"start Docker, then run the command again")
	}
	return nil
}

// rootCause returns the innermost error: the client restates the endpoint
// at every layer, and only the innermost reason is worth printing.
func rootCause(err error) error {
	for {
		inner := errors.Unwrap(err)
		if inner == nil {
			return err
		}
		err = inner
	}
}
