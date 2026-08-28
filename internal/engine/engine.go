// Package engine is tamp's boundary with the container engine: finding
// Docker, asking the Docker API, and invoking compose.
//
// It is the only thing tamp fakes in tests. Everything above it — commands,
// diagnosis, later the whole environment lifecycle — is exercised against real
// code with a recording fake standing in here, so a test that passes says
// something about tamp rather than about the mock.
package engine

import (
	"context"
	"io"
)

// Info is what tamp learned by reaching the engine.
type Info struct {
	Address Address
	Version string // the Docker Engine version, e.g. "29.7.2"
}

// ComposeProject identifies one environment's compose project: the name every
// container, volume and network beneath it is prefixed with, and the generated
// file that describes it.
type ComposeProject struct {
	// Name is the -p project name, tamp-<env>-<hash>.
	Name string
	// File is the absolute path to the generated compose.yaml.
	File string
	// Dir is the environment directory, which compose resolves the file's
	// relative paths — the database secret, for one — against.
	Dir string
}

// Removal says how much of an environment a compose down takes with it.
//
// It is a named type rather than a bool because the call sites are `tamp
// stop` and `tamp rm --volumes`, and the difference between them is every
// site's database.
type Removal int

const (
	// KeepVolumes removes containers and the network. Volumes always survive
	// stop, and survive rm unless the user asks otherwise.
	KeepVolumes Removal = iota
	// RemoveVolumes additionally destroys the environment's storage layers.
	RemoveVolumes
)

// Container is one of an environment's containers, as the engine sees it.
type Container struct {
	// Service is the compose service name: frappe, mariadb, redis-cache...
	Service string
	Running bool
}

// Engine is the whole of tamp's dependency on Docker.
//
// Every method's failure is an *exitcode.Error carrying CodeEngineUnavailable
// and a fix, which is what makes exit 4 tamp's answer to an unreachable
// engine: a command that cannot proceed without Docker returns the error
// unchanged, and the user gets something to act on rather than a stack trace.
type Engine interface {
	// Ping resolves the engine's address and confirms the daemon answers.
	Ping(ctx context.Context) (Info, error)
	// ComposeVersion reports the version of the `docker compose` v2 plugin.
	// It deliberately does not need a reachable daemon: a user whose Docker is
	// merely stopped should still be told whether compose is installed.
	ComposeVersion(ctx context.Context) (string, error)

	// ComposeUp creates and starts the project's containers, pulling any
	// images the machine does not have, and waits for the healthchecks the
	// generated file declares.
	//
	// Every compose operation streams the engine's own output to out rather
	// than to a terminal, which is how create captures it into create.log and
	// how tests read what compose was told to do.
	ComposeUp(ctx context.Context, p ComposeProject, out io.Writer) error
	// ComposeStop stops the project's containers, leaving them — and every
	// volume — in place.
	ComposeStop(ctx context.Context, p ComposeProject, out io.Writer) error
	// ComposeDown removes the project's containers and network, and its
	// volumes only when asked.
	ComposeDown(ctx context.Context, p ComposeProject, removal Removal, out io.Writer) error

	// Containers reports the project's containers, running or not. An
	// environment that was never created, or whose containers were removed,
	// has none — that is an empty slice, not an error.
	Containers(ctx context.Context, project string) ([]Container, error)
}
