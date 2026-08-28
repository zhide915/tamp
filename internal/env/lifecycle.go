// Package env models tamp environments: tamp.toml on disk, the machine-global
// registry, the Docker resource names, and the lifecycle operations over
// them. The engine is injected — nothing here touches Docker directly — so
// the whole lifecycle runs in tests against a temp HOME and a recording fake.
package env

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/frappe"
	"github.com/zhide915/tamp/internal/router"
	"github.com/zhide915/tamp/internal/syncer"
	"github.com/zhide915/tamp/internal/toolchain"
	"github.com/zhide915/tamp/internal/ui"
)

// Manager runs the lifecycle operations, holding what all of them need so
// cmd/ stays a translation from flags to one call.
type Manager struct {
	Home   string
	Cwd    string
	Engine engine.Engine
	// Sync mirrors an environment's source to the host — tamp's second
	// external-process seam after the engine.
	Sync syncer.Mutagen
	Out  *ui.Printer
}

func NewManager(eng engine.Engine, sync syncer.Mutagen, out *ui.Printer) (*Manager, error) {
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
	return &Manager{Home: home, Cwd: cwd, Engine: eng, Sync: sync, Out: out}, nil
}

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

func (e *Environment) project() engine.ComposeProject {
	return engine.ComposeProject{
		Name: e.Resources.Project(),
		File: ComposePath(e.Dir),
		Dir:  e.Dir,
	}
}

func (e *Environment) bench(eng engine.Engine, out io.Writer) *frappe.Bench {
	return &frappe.Bench{
		Engine:    eng,
		Container: e.Resources.Container(FrappeService),
		Branch:    string(e.Config.Frappe.Version),
		Python:    e.Config.Toolchain.Python,
		Node:      e.Config.Toolchain.Node,
		// Peers answer to their service names on the environment's network.
		DBHost:     MariaDBService,
		RedisCache: RedisCacheService,
		RedisQueue: RedisQueueService,
		MailHost:   MailpitService,
		Out:        out,
	}
}

// SharedVolumes are common to every environment: the toolchain and the two
// package caches. The compose file declares them external so `tamp rm
// --volumes` on one environment cannot empty them for the rest — which means
// tamp must create them itself.
func SharedVolumes() []string {
	return []string{toolchain.Volume, frappe.PipCacheVolume, frappe.YarnCacheVolume}
}

func (m *Manager) ensureSharedVolumes(ctx context.Context) error {
	for _, name := range SharedVolumes() {
		if err := m.Engine.EnsureVolume(ctx, name); err != nil {
			return err
		}
	}
	return nil
}

// requireRunning refuses in tamp's own words rather than whatever the engine
// would say steps later. because completes "<env> is not running, ...".
func (m *Manager) requireRunning(ctx context.Context, e *Environment, because string) error {
	running, err := m.benchRunning(ctx, e)
	if err != nil {
		return err
	}
	if !running {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("%s is not running, %s", e.Name(), because),
			fmt.Sprintf("start it with 'tamp start %s'", e.Name()))
	}
	return nil
}

// benchRunning is separate from requireRunning because not every caller
// refuses: some fall back to what tamp last recorded.
func (m *Manager) benchRunning(ctx context.Context, e *Environment) (bool, error) {
	if _, err := m.Engine.Ping(ctx); err != nil {
		return false, err
	}
	containers, err := m.Engine.Containers(ctx, e.Resources.Project())
	if err != nil {
		return false, err
	}
	return isRunning(containers, FrappeService), nil
}

// State is what tamp reports an environment is doing.
type State string

const (
	StateRunning State = "running"
	// StateStopped — no containers, or none running.
	StateStopped State = "stopped"
	// StateDegraded — some running, some not: the state that needs looking at,
	// so it is not rounded to either of the others.
	StateDegraded State = "degraded"
	// StateUnknown — the engine was unreachable.
	StateUnknown State = "unknown"
)

func stateOf(containers []engine.Container) State {
	running := 0
	for _, c := range containers {
		if c.Running {
			running++
		}
	}
	switch running {
	case 0:
		// No containers at all also reads as stopped.
		return StateStopped
	case len(containers):
		return StateRunning
	default:
		return StateDegraded
	}
}

// Start brings an environment up, regenerating the generated files first —
// tamp.toml is the source of truth on every start, including after a tamp
// upgrade changed the templates.
func (m *Manager) Start(ctx context.Context, name string) error {
	e, err := m.resolve(name)
	if err != nil {
		return err
	}
	if _, err := m.Engine.Ping(ctx); err != nil {
		return err
	}

	// The sync mode decides what is written (a bind mount is a compose line),
	// so it is settled first; and regeneration precedes the running check so
	// a hand-edited compose.yaml disappears whether or not the environment is
	// up.
	sync := m.syncMode(ctx, e.Config.Sync.Mode)

	m.Out.Step(1, startSteps, "regenerating "+ComposeFile+" from "+ConfigFile)
	if err := m.regenerate(e, sync); err != nil {
		return err
	}

	containers, err := m.Engine.Containers(ctx, e.Resources.Project())
	if err != nil {
		return err
	}
	// Starting a running environment exits 0 with a notice — scripts run
	// start defensively. The routing still happens: the router is
	// machine-global and may have been stopped since.
	running := stateOf(containers) == StateRunning

	if running {
		m.Out.Step(2, startSteps, "containers are already running")
	} else {
		m.Out.Step(2, startSteps, "starting containers")
		if err := m.ensureSharedVolumes(ctx); err != nil {
			return err
		}
		if err := m.Engine.ComposeUp(ctx, e.project(), m.Out.Stream()); err != nil {
			return err
		}
	}

	m.Out.Step(3, startSteps, "starting the sync session")
	if err := m.startSync(ctx, e, sync, m.Out.Stream()); err != nil {
		return err
	}
	// Reconciles apps added through the exec bridge since the last start.
	if err := m.handGitToHost(ctx, e, sync); err != nil {
		return err
	}

	m.Out.Step(4, startSteps, "routing "+router.MailHost(string(e.Name())))
	status, err := m.applyRoutes(ctx, m.Out.Stream())
	if err != nil {
		return err
	}

	if running {
		m.Out.OK(fmt.Sprintf("%s is already running", e.Name()))
	} else {
		m.Out.OK(fmt.Sprintf("%s started", e.Name()))
	}
	m.announceRoutes(e, status)
	return nil
}

const startSteps = 4

func (m *Manager) regenerate(e *Environment, sync syncer.Effective) error {
	if err := EnsureDBRootPassword(e.Dir); err != nil {
		return err
	}
	if err := ensureAppsDir(e.Dir, sync); err != nil {
		return err
	}
	return e.Generate(sync)
}

// Stop stops the containers. Volumes always survive — there is deliberately
// no flag on stop that destroys data.
func (m *Manager) Stop(ctx context.Context, name string) error {
	return m.stop(ctx, name, true)
}

// stop serves Stop and Restart. final gates the closing hint: mid-restart,
// "tamp start brings it back" would read as the end of an unfinished
// operation.
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

	// Paused before the containers go, or Mutagen spends the teardown
	// reporting its far end vanished.
	m.pauseSync(ctx, e)

	if err := m.Engine.ComposeStop(ctx, e.project(), m.Out.Stream()); err != nil {
		return err
	}
	m.Out.OK(fmt.Sprintf("%s stopped", e.Name()))
	if final {
		m.Out.Hint("volumes are untouched — 'tamp start' brings it back with its data")
	}
	return nil
}

// Restart is spelled out rather than delegating so an already-stopped
// environment still starts.
func (m *Manager) Restart(ctx context.Context, name string) error {
	if err := m.stop(ctx, name, false); err != nil {
		return err
	}
	return m.Start(ctx, name)
}
