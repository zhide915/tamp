// Package enginetest provides the recording fake that stands in for the
// container engine.
//
// The engine boundary is tamp's only fake point, so this is the only fake in
// the codebase. It records every request rather than just answering them,
// because a test that checks the output tamp printed still would not catch
// tamp asking the engine for the wrong thing.
package enginetest

import (
	"context"

	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/exitcode"
)

// Fake is a scripted Engine. Set the answers, run the code under test, then
// assert on Calls.
type Fake struct {
	// Info is what Ping reports while PingErr is nil.
	Info    engine.Info
	PingErr error

	// Compose is what ComposeVersion reports while ComposeErr is nil.
	Compose    string
	ComposeErr error

	// Calls names each engine method tamp invoked, in order.
	Calls []string
}

// Running is an engine that is up: a plausible Docker found by probing, and a
// compose v2 alongside it. It is the default backdrop for tests about
// something other than the engine being broken.
func Running() *Fake {
	return &Fake{
		Info: engine.Info{
			Address: engine.Address{
				Host:   "unix:///var/run/docker.sock",
				Source: engine.SourceProbe,
			},
			Version: "29.7.2",
		},
		Compose: "2.39.1",
	}
}

// Unavailable is an engine tamp cannot reach at all — the exit-4 case.
func Unavailable() *Fake {
	return &Fake{
		PingErr: exitcode.New(exitcode.CodeEngineUnavailable,
			"no Docker engine found", "start Docker Desktop"),
		ComposeErr: exitcode.New(exitcode.CodeEngineUnavailable,
			"'docker compose' is not available", "install Docker Desktop"),
	}
}

func (f *Fake) Ping(context.Context) (engine.Info, error) {
	f.Calls = append(f.Calls, "Ping")
	if f.PingErr != nil {
		return engine.Info{}, f.PingErr
	}
	return f.Info, nil
}

func (f *Fake) ComposeVersion(context.Context) (string, error) {
	f.Calls = append(f.Calls, "ComposeVersion")
	if f.ComposeErr != nil {
		return "", f.ComposeErr
	}
	return f.Compose, nil
}

// A fake that has drifted from the interface would silently stop testing the
// thing it stands in for.
var _ engine.Engine = (*Fake)(nil)
