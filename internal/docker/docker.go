// Package docker is tamp's boundary with Docker. Tests replace it with Fake;
// every other package runs for real.
package docker

import (
	"context"
	"errors"
	"fmt"
	"io"
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

	// ImageExists reports whether an image with the tag is on this machine.
	ImageExists(ctx context.Context, tag string) (bool, error)
	// BuildImage builds an image from a Dockerfile with no build context.
	BuildImage(ctx context.Context, b ImageBuild) error

	// EnsureVolume creates the volume unless one with its name exists. An
	// existing volume keeps the labels it was created with.
	EnsureVolume(ctx context.Context, v Volume) error

	// ComposeUp creates and starts the project's containers, and waits for
	// their healthchecks.
	ComposeUp(ctx context.Context, p Project) error

	// Exec runs a command in a running container as the image's user, in
	// the image's working directory, and waits. A non-zero exit is an error.
	Exec(ctx context.Context, e Exec) error
	// ReadFile returns a file from a container, or an error wrapping
	// ErrNotExist when the file is not there.
	ReadFile(ctx context.Context, container, path string) ([]byte, error)
	// WriteFile replaces a file in a container, owned by the image's user.
	WriteFile(ctx context.Context, container, path string, data []byte) error

	// Containers lists the running containers that carry the label.
	Containers(ctx context.Context, label string) ([]Container, error)
	// Inspect returns one container, running or not, or an error wrapping
	// ErrNoContainer.
	Inspect(ctx context.Context, name string) (Container, error)
	// ConnectNetwork attaches a container to a network, where it also
	// answers to the aliases. Aliases stay fixed until it is disconnected.
	ConnectNetwork(ctx context.Context, network, container string, aliases []string) error
	// DisconnectNetwork detaches a container from a network.
	DisconnectNetwork(ctx context.Context, network, container string) error
}

// ErrNotExist is ReadFile's answer for a missing file.
var ErrNotExist = errors.New("file does not exist")

// ErrNoContainer is Inspect's answer for a missing container.
var ErrNoContainer = errors.New("no such container")

// ErrPortTaken is wrapped by a ComposeUp that failed because a port it
// publishes is already in use on the host.
var ErrPortTaken = errors.New("a published port is taken")

// Container is one container as Docker reports it.
type Container struct {
	Name    string
	Running bool
	Labels  map[string]string
	// Networks maps each network the container is on to its aliases there.
	Networks map[string][]string
}

// ImageBuild is one image to build.
type ImageBuild struct {
	Tag        string
	Dockerfile string
	Args       map[string]string
	// Out receives the build's progress.
	Out io.Writer
}

// Volume is a named volume.
type Volume struct {
	Name   string
	Labels map[string]string
}

// Project is one compose project.
type Project struct {
	Name string
	// File is the compose file's absolute path. Compose resolves the file's
	// relative paths against its directory.
	File string
	// Out receives compose's progress.
	Out io.Writer
}

// Exec is one command run in a container.
type Exec struct {
	Container string
	// Cmd is argv; no shell runs it.
	Cmd []string
	// Stdout and Stderr receive the command's streams; nil discards.
	Stdout, Stderr io.Writer
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
			"start Docker")
	}
	return nil
}

func (d *Daemon) ImageExists(ctx context.Context, tag string) (bool, error) {
	_, api, err := d.connect()
	if err != nil {
		return false, err
	}
	res, err := api.ImageList(ctx, client.ImageListOptions{
		Filters: make(client.Filters).Add("reference", tag),
	})
	if err != nil {
		return false, unavailable("list Docker images", err)
	}
	return len(res.Items) > 0, nil
}

func (d *Daemon) EnsureVolume(ctx context.Context, v Volume) error {
	_, api, err := d.connect()
	if err != nil {
		return err
	}
	// The name filter matches substrings, so the exact match happens here.
	res, err := api.VolumeList(ctx, client.VolumeListOptions{
		Filters: make(client.Filters).Add("name", v.Name),
	})
	if err != nil {
		return unavailable("list Docker volumes", err)
	}
	for _, item := range res.Items {
		if item.Name == v.Name {
			return nil
		}
	}
	if _, err := api.VolumeCreate(ctx, client.VolumeCreateOptions{Name: v.Name, Labels: v.Labels}); err != nil {
		return unavailable("create the volume "+v.Name, err)
	}
	return nil
}

func unavailable(what string, err error) error {
	return exitcode.New(exitcode.CodeDockerUnavailable,
		fmt.Sprintf("cannot %s: %v", what, rootCause(err)),
		"start Docker")
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
