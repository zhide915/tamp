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
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/exitcode"
)

// DefaultServices are the compose services a tamp environment contains. The
// fake answers Containers from this list; a test in internal/env checks the
// generated compose file against it, so the two cannot drift apart silently.
var DefaultServices = []string{"frappe", "mariadb", "redis-cache", "redis-queue", "mailpit"}

// Op is one compose operation the fake was asked to perform, in full.
//
// Calls answers "which engine methods, in what order"; Op answers "on which
// project, from which file, taking what with it" — the questions that catch
// tamp acting on the wrong environment.
type Op struct {
	Method  string
	Project engine.ComposeProject
	// Removal is meaningful only on ComposeDown.
	Removal engine.Removal
}

// Fake is a scripted Engine. Set the answers, run the code under test, then
// assert on Calls and Ops.
type Fake struct {
	// Info is what Ping reports while PingErr is nil.
	Info    engine.Info
	PingErr error

	// Compose is what ComposeVersion reports while ComposeErr is nil.
	Compose    string
	ComposeErr error

	// UpErr, StopErr and DownErr script a compose operation that fails —
	// the mid-create failure whose rollback tamp has to get right.
	UpErr   error
	StopErr error
	DownErr error

	// Services are the containers a project has once it has been brought up.
	Services []string

	// Calls names each engine method tamp invoked, in order.
	Calls []string
	// Ops records each compose operation in full.
	Ops []Op

	// running tracks, per project, whether its containers exist and whether
	// they are running — so that up, stop, down and Containers tell one
	// consistent story to the code under test.
	running map[string]bool
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
		Compose:  "2.39.1",
		Services: slices.Clone(DefaultServices),
	}
}

// Unavailable is an engine tamp cannot reach at all — the exit-4 case.
func Unavailable() *Fake {
	unreachable := exitcode.New(exitcode.CodeEngineUnavailable,
		"no Docker engine found", "start Docker Desktop")
	return &Fake{
		PingErr: unreachable,
		ComposeErr: exitcode.New(exitcode.CodeEngineUnavailable,
			"'docker compose' is not available", "install Docker Desktop"),
		UpErr:    unreachable,
		StopErr:  unreachable,
		DownErr:  unreachable,
		Services: slices.Clone(DefaultServices),
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

func (f *Fake) ComposeUp(_ context.Context, p engine.ComposeProject, out io.Writer) error {
	if err := f.record("ComposeUp", p, engine.KeepVolumes, out); err != nil {
		return err
	}
	if f.UpErr != nil {
		return f.UpErr
	}
	f.setRunning(p.Name, true)
	return nil
}

func (f *Fake) ComposeStop(_ context.Context, p engine.ComposeProject, out io.Writer) error {
	if err := f.record("ComposeStop", p, engine.KeepVolumes, out); err != nil {
		return err
	}
	if f.StopErr != nil {
		return f.StopErr
	}
	if _, exists := f.running[p.Name]; exists {
		f.setRunning(p.Name, false)
	}
	return nil
}

func (f *Fake) ComposeDown(_ context.Context, p engine.ComposeProject, removal engine.Removal, out io.Writer) error {
	if err := f.record("ComposeDown", p, removal, out); err != nil {
		return err
	}
	if f.DownErr != nil {
		return f.DownErr
	}
	delete(f.running, p.Name)
	return nil
}

func (f *Fake) Containers(_ context.Context, project string) ([]engine.Container, error) {
	f.Calls = append(f.Calls, "Containers")

	running, exists := f.running[project]
	if !exists {
		// A project that was never brought up, or that has been taken down,
		// has no containers. That is an answer, not a failure.
		return nil, nil
	}

	out := make([]engine.Container, 0, len(f.Services))
	for _, service := range f.Services {
		out = append(out, engine.Container{Service: service, Running: running})
	}
	return out, nil
}

// record logs the operation and enforces the one thing real compose would
// enforce for free: the generated file has to exist before tamp acts on it.
// Without this a test could not tell "tamp generated compose.yaml, then
// started it" from "tamp started something it never wrote".
func (f *Fake) record(method string, p engine.ComposeProject, removal engine.Removal, out io.Writer) error {
	f.Calls = append(f.Calls, method)
	f.Ops = append(f.Ops, Op{Method: method, Project: p, Removal: removal})

	if out != nil {
		// Real compose narrates what it is doing; tamp captures that stream
		// into create.log, and a silent fake would let a broken capture pass.
		fmt.Fprintf(out, "compose %s %s\n", method, p.Name)
	}

	if _, err := os.Stat(p.File); err != nil {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("compose file %s is missing", p.File),
			"this is a tamp bug: the file should have been generated first")
	}
	return nil
}

func (f *Fake) setRunning(project string, running bool) {
	if f.running == nil {
		f.running = map[string]bool{}
	}
	f.running[project] = running
}

// A fake that has drifted from the interface would silently stop testing the
// thing it stands in for.
var _ engine.Engine = (*Fake)(nil)
