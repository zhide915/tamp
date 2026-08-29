package env

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"slices"

	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/frappe"
	"github.com/zhide915/tamp/internal/syncer"
	"github.com/zhide915/tamp/internal/toolchain"
)

// syncMode settles how source reaches the container, decided fresh on every
// create and start because Mutagen's availability can change. The bind
// fallback is loud: it works, but it is slow and inotify does not fire
// through it, so nothing hot-reloads.
func (m *Manager) syncMode(ctx context.Context, want syncer.Mode) syncer.Effective {
	mode := syncer.Resolve(want, runtime.GOOS)
	if mode != syncer.UseMutagen {
		return mode
	}
	if _, err := m.Sync.Ensure(ctx); err != nil {
		m.Out.Warn("tamp cannot use Mutagen here: " + err.Error())
		m.Out.Warn("falling back to a bind mount — it works, but it is slow, and nothing will hot-reload")
		return syncer.UseBind
	}
	return mode
}

// syncEndpoints names the two sides of an environment's session. The host
// side is alpha: it is where editing happens, and the side two-way-resolved
// settles conflicts toward. Both names are tamp's own, so reporting on a
// session needs no engine.
func (e *Environment) syncEndpoints() syncer.Session {
	return syncer.Session{
		Name:  e.Resources.Project(),
		Alpha: syncer.AppsDir(e.Dir),
		Beta: syncer.BetaURL(toolchain.User,
			e.Resources.Container(FrappeService), frappe.AppsDir),
	}
}

// syncSession is the session to create or rebuild, pinned to the engine tamp
// resolved rather than whatever Mutagen would detect.
func (m *Manager) syncSession(ctx context.Context, e *Environment) (syncer.Session, error) {
	info, err := m.Engine.Ping(ctx)
	if err != nil {
		return syncer.Session{}, err
	}
	session := e.syncEndpoints()
	session.DockerHost = info.Address.Host
	return session, nil
}

// startSync resumes the environment's session or creates one. Bind and off
// are finished states, not omissions — neither needs anything running.
func (m *Manager) startSync(ctx context.Context, e *Environment, mode syncer.Effective, out io.Writer) error {
	switch mode {
	case syncer.UseBind:
		m.Out.Note("source: " + syncer.AppsDir(e.Dir) + " — bound straight into the container")
		return nil
	case syncer.UseOff:
		m.Out.Note("source stays in the container: this environment syncs nothing to the host")
		return nil
	}

	held, err := m.Sync.Sessions(ctx)
	if err != nil {
		return err
	}
	if slices.Contains(held, e.Resources.Project()) {
		if err := m.Sync.Resume(ctx, e.Resources.Project()); err != nil {
			return err
		}
	} else {
		session, err := m.syncSession(ctx, e)
		if err != nil {
			return err
		}
		if err := m.Sync.Create(ctx, session, out); err != nil {
			return err
		}
	}
	m.Out.Note("source: " + syncer.AppsDir(e.Dir) + " — edits reach the container in about a second")
	return nil
}

// pauseSync stops the session without forgetting it, so the next start
// resumes instead of resynchronising the tree. Failure is a warning: the
// containers are already down, which is what was asked for.
func (m *Manager) pauseSync(ctx context.Context, e *Environment) {
	if !m.hasSyncSession(ctx, e) {
		return
	}
	if err := m.Sync.Pause(ctx, e.Resources.Project()); err != nil {
		m.Out.Warn(fmt.Sprintf("could not pause the sync session: %v", err))
	}
}

func (m *Manager) terminateSync(ctx context.Context, e *Environment) {
	if !m.hasSyncSession(ctx, e) {
		return
	}
	if err := m.Sync.Terminate(ctx, e.Resources.Project()); err != nil {
		m.Out.Warn(fmt.Sprintf("could not terminate the sync session: %v", err))
	}
}

// hasSyncSession asks Mutagen only when the machine already has it: stopping
// a bind-mounted environment must not trigger a Mutagen download.
func (m *Manager) hasSyncSession(ctx context.Context, e *Environment) bool {
	if syncer.Resolve(e.Config.Sync.Mode, runtime.GOOS) != syncer.UseMutagen {
		return false
	}
	if _, err := m.Sync.Find(ctx); err != nil {
		return false
	}
	held, err := m.Sync.Sessions(ctx)
	if err != nil {
		m.Out.Warn(fmt.Sprintf("could not ask Mutagen what it is syncing: %v", err))
		return false
	}
	return slices.Contains(held, e.Resources.Project())
}

// ensureAppsDir pre-creates the host apps/ directory: Docker creates a
// missing bind source as root, and a Mutagen session needs a mirror target.
// Only "off" has no host side.
func ensureAppsDir(dir string, mode syncer.Effective) error {
	if mode == syncer.UseOff {
		return nil
	}
	apps := syncer.AppsDir(dir)
	if err := os.MkdirAll(apps, 0o755); err != nil {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot create %s: %v", apps, err),
			"check the permissions on the environment directory")
	}
	return nil
}
