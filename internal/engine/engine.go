// Package engine is tamp's boundary with the container engine: finding
// Docker, asking the Docker API, and invoking compose.
//
// It is the only thing tamp fakes in tests. Everything above it — commands,
// diagnosis, later the whole environment lifecycle — is exercised against real
// code with a recording fake standing in here, so a test that passes says
// something about tamp rather than about the mock.
package engine

import "context"

// Info is what tamp learned by reaching the engine.
type Info struct {
	Address Address
	Version string // the Docker Engine version, e.g. "29.7.2"
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
}
