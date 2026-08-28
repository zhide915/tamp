// Package env is tamp's environment model: the tamp.toml on disk, the global
// registry that indexes every environment on the machine, the names tamp
// gives Docker resources, and the lifecycle operations built on top of them.
//
// Nothing here touches Docker directly — the engine is passed in — so the
// whole environment lifecycle is exercised in tests against a temp HOME and a
// recording fake.
package env

import (
	"context"
	"fmt"
	"os"

	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/ui"
)

// Manager performs tamp's environment lifecycle operations.
//
// It holds the four things every one of them needs — the machine's tamp home,
// the directory the user ran tamp in, the engine, and somewhere to narrate to
// — so that cmd/ stays a translation from flags to one call, and so that the
// whole lifecycle runs in a test against a temp home and the recording fake.
type Manager struct {
	Home   string
	Cwd    string
	Engine engine.Engine
	Out    *ui.Printer
}

// NewManager resolves the machine-global paths a lifecycle operation needs.
func NewManager(eng engine.Engine, out *ui.Printer) (*Manager, error) {
	home, err := Home()
	if err != nil {
		return nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot determine the current directory: %v", err),
			"run tamp from a directory that still exists")
	}
	return &Manager{Home: home, Cwd: cwd, Engine: eng, Out: out}, nil
}

// resolve finds the environment a command was pointed at, and reports anything
// noticed while reading its config.
func (m *Manager) resolve(name string) (*Environment, error) {
	e, err := Resolve(m.Home, m.Cwd, name)
	if err != nil {
		return nil, err
	}
	for _, w := range e.Warnings {
		m.Out.Warn(w)
	}
	return e, nil
}

// project describes an environment to the compose runner.
func (e *Environment) project() engine.ComposeProject {
	return engine.ComposeProject{
		Name: e.Resources.Project(),
		File: ComposePath(e.Dir),
		Dir:  e.Dir,
	}
}

// State is what tamp reports an environment is doing.
type State string

const (
	// StateRunning — every container is up.
	StateRunning State = "running"
	// StateStopped — the environment has no containers, or none are running.
	StateStopped State = "stopped"
	// StateDegraded — some containers are running and some are not. It is a
	// state of its own rather than a rounding of the other two because it is
	// the one that needs looking at.
	StateDegraded State = "degraded"
	// StateUnknown — tamp could not ask, because the engine is unreachable.
	StateUnknown State = "unknown"
)

// stateOf reads an environment's containers and reduces them to one word.
func stateOf(containers []engine.Container) State {
	running := 0
	for _, c := range containers {
		if c.Running {
			running++
		}
	}
	switch running {
	case 0:
		// Also the answer for an environment with no containers at all, which
		// is what a removed or never-created project looks like.
		return StateStopped
	case len(containers):
		return StateRunning
	default:
		return StateDegraded
	}
}

// Start brings an environment up, regenerating its generated files first.
//
// The regeneration is the point, not a detail: tamp.toml is the source
// of truth, so every start reconciles the containers with it — including after
// tamp itself was upgraded and the templates underneath changed.
func (m *Manager) Start(ctx context.Context, name string) error {
	e, err := m.resolve(name)
	if err != nil {
		return err
	}
	if _, err := m.Engine.Ping(ctx); err != nil {
		return err
	}

	// Regeneration comes before the running check, not after it: tamp.toml is
	// the source of truth on every start, not only on the ones that go on to
	// touch a container. A hand-edited compose.yaml has to disappear whether
	// or not the environment happens to be up.
	m.Out.Step(1, startSteps, "regenerating "+ComposeFile+" from "+ConfigFile)
	if err := m.regenerate(e); err != nil {
		return err
	}

	containers, err := m.Engine.Containers(ctx, e.Resources.Project())
	if err != nil {
		return err
	}
	if stateOf(containers) == StateRunning {
		// Starting a running environment is a no-op with a notice, exit 0:
		// scripts and agents run start defensively, and that must not fail.
		m.Out.OK(fmt.Sprintf("%s is already running", e.Name()))
		return nil
	}

	m.Out.Step(2, startSteps, "starting containers")
	if err := m.Engine.ComposeUp(ctx, e.project(), m.Out.Stream()); err != nil {
		return err
	}

	m.Out.OK(fmt.Sprintf("%s started", e.Name()))
	return nil
}

// startSteps is how many numbered steps a start prints. It grows alongside
// create's, as tamp learns to revive more of an environment.
const startSteps = 2

// regenerate rewrites the environment's generated files from tamp.toml.
func (m *Manager) regenerate(e *Environment) error {
	if err := EnsureDBRootPassword(e.Dir); err != nil {
		return err
	}
	return e.Generate()
}

// Stop stops an environment's containers. Volumes always survive it — there is
// no flag on stop that destroys data, deliberately.
func (m *Manager) Stop(ctx context.Context, name string) error {
	return m.stop(ctx, name, true)
}

// stop does the work for both Stop and Restart. final says whether stopping is
// the whole of what the user asked for: mid-restart, "tamp start brings it
// back" is a next step tamp is about to take itself, and printing it there
// would read as the end of an operation that has not finished.
func (m *Manager) stop(ctx context.Context, name string, final bool) error {
	e, err := m.resolve(name)
	if err != nil {
		return err
	}
	if _, err := m.Engine.Ping(ctx); err != nil {
		return err
	}

	containers, err := m.Engine.Containers(ctx, e.Resources.Project())
	if err != nil {
		return err
	}
	if stateOf(containers) == StateStopped {
		m.Out.OK(fmt.Sprintf("%s is already stopped", e.Name()))
		return nil
	}

	if err := m.Engine.ComposeStop(ctx, e.project(), m.Out.Stream()); err != nil {
		return err
	}
	m.Out.OK(fmt.Sprintf("%s stopped", e.Name()))
	if final {
		m.Out.Hint("volumes are untouched — 'tamp start' brings it back with its data")
	}
	return nil
}

// Restart is stop then start. It is spelled out rather than delegating so that
// an environment which is already stopped still starts.
func (m *Manager) Restart(ctx context.Context, name string) error {
	if err := m.stop(ctx, name, false); err != nil {
		return err
	}
	return m.Start(ctx, name)
}
