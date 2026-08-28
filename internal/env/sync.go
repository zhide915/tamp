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

// syncMode settles how an environment's source reaches its container, on this
// machine, right now.
//
// It is decided fresh on every create and every start rather than recorded,
// because it depends on something that changes: whether tamp can get hold of
// Mutagen. A machine that goes offline behind a proxy falls back to a bind
// mount and says so — loudly, because the fallback works and is slow, and
// because inotify does not fire through it, so nothing hot-reloads.
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

// syncSession describes an environment's sync to Mutagen.
//
// The host side is alpha because the host is where the editing happens, and
// alpha is the side two-way-resolved settles a conflict in favour of.
func (m *Manager) syncSession(ctx context.Context, e *Environment) (syncer.Session, error) {
	info, err := m.Engine.Ping(ctx)
	if err != nil {
		return syncer.Session{}, err
	}
	return syncer.Session{
		Name:  e.Resources.Project(),
		Alpha: syncer.AppsDir(e.Dir),
		Beta: syncer.BetaURL(toolchain.User,
			e.Resources.Container(FrappeService), frappe.AppsDir),
		DockerHost: info.Address.Host,
	}, nil
}

// startSync brings an environment's sync session up: resumed if tamp has one
// for this environment already, created if it does not.
//
// The other modes are not failures and not omissions. A bind mount needs
// nothing running, and off means the source stays in the container — both are
// finished states, and the note says which one the user is in.
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

// pauseSync stops an environment's sync session without forgetting it, so that
// starting the environment again picks it up rather than resynchronising the
// whole tree.
//
// Its failure is a warning: the containers are already down, which is what the
// user asked for, and a stop that reports a sync error instead would be
// telling them about the smaller half of what happened.
func (m *Manager) pauseSync(ctx context.Context, e *Environment) {
	if !m.hasSyncSession(ctx, e) {
		return
	}
	if err := m.Sync.Pause(ctx, e.Resources.Project()); err != nil {
		m.Out.Warn(fmt.Sprintf("could not pause the sync session: %v", err))
	}
}

// terminateSync forgets an environment's sync session for good.
func (m *Manager) terminateSync(ctx context.Context, e *Environment) {
	if !m.hasSyncSession(ctx, e) {
		return
	}
	if err := m.Sync.Terminate(ctx, e.Resources.Project()); err != nil {
		m.Out.Warn(fmt.Sprintf("could not terminate the sync session: %v", err))
	}
}

// hasSyncSession reports whether there is a session to act on at all.
//
// It asks Mutagen only when the machine already has one. Stopping a
// bind-mounted environment must not be the thing that downloads a Mutagen this
// machine has never needed.
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

// ensureAppsDir makes the host side of the source layer.
//
// It exists before anything starts, in both syncing modes and for different
// reasons: a bind mount whose host directory is missing is created by Docker
// as root, and a Mutagen session needs somewhere to put the tree it mirrors
// out. Only "off" has no host side at all.
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
