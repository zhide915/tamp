package env

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/syncer"
)

// lastFlushFile records when tamp last forced a full pass. Mutagen keeps no
// such date, and it is the one thing a user who just ran 'tamp sync flush'
// wants to see.
const lastFlushFile = "last-flush"

func lastFlushPath(dir string) string { return filepath.Join(StateDir(dir), lastFlushFile) }

// SyncStatus reports what tamp's sync is doing for one environment. Its
// first half is tamp's own knowledge, so it answers with Docker down too.
func (m *Manager) SyncStatus(ctx context.Context, name string) error {
	e, syncs, err := m.syncing(name)
	if err != nil || !syncs {
		return err
	}

	session := e.syncEndpoints()
	m.Out.Print("mode      " + syncer.UseMutagen.String())
	m.Out.Print("session   " + session.Name)
	m.Out.Print("host      " + session.Alpha)
	m.Out.Print("container " + session.Beta)
	m.Out.Print("ignores   " + strings.Join(syncer.Ignores, " "))
	m.Out.Print("flushed   " + m.lastFlush(e))
	// The daemon outlives every session, and nothing else in tamp mentions
	// that it is there at all.
	m.Out.Print("daemon    tamp's own, in " + filepath.Join(m.Home, syncer.DataDirName) + " — 'tamp sync stop' stops it")
	m.Out.Print("")

	// Find, not Ensure: reporting on a machine that has never synced must not
	// download Mutagen to say so.
	if _, err := m.Sync.Find(ctx); err != nil {
		m.Out.Note("this machine has no Mutagen yet — tamp downloads it the first time it syncs")
		return nil
	}
	held, err := m.Sync.Sessions(ctx)
	if err != nil {
		return err
	}
	if !slices.Contains(held, session.Name) {
		m.Out.Note("Mutagen is not holding this session")
		m.Out.Hint(startsTheSession(e))
		return nil
	}

	report, err := m.Sync.Report(ctx, session.Name)
	if err != nil {
		return err
	}
	m.Out.Print(strings.TrimRight(report, "\n"))
	return nil
}

// SyncFlush forces a full pass and waits for it, so a return means the two
// sides agree.
func (m *Manager) SyncFlush(ctx context.Context, name string) error {
	e, syncs, err := m.syncing(name)
	if err != nil || !syncs {
		return err
	}
	// Stopping an environment pauses its session rather than forgetting it,
	// so Mutagen still holds one here — and refuses to flush it.
	if err := m.requireRunning(ctx, e, "so its paused session has nothing to flush to"); err != nil {
		return err
	}
	session, err := m.requireSession(ctx, e)
	if err != nil {
		return err
	}

	if err := m.Sync.Flush(ctx, session); err != nil {
		return err
	}
	m.recordFlush(e)
	m.Out.OK("flushed " + session)
	m.Out.Note("both sides of " + syncer.AppsDir(e.Dir) + " agree as of now")
	return nil
}

// SyncReset terminates the session and creates it again. It is the
// documented recovery after a large host-side change, such as a branch
// checkout, that leaves Mutagen reconciling far more than it can settle.
func (m *Manager) SyncReset(ctx context.Context, name string) error {
	e, syncs, err := m.syncing(name)
	if err != nil || !syncs {
		return err
	}
	// The far end is a container: a session cannot be rebuilt against an
	// environment that is not running.
	if err := m.requireRunning(ctx, e, "so its container has no end for a session to reach"); err != nil {
		return err
	}

	session, err := m.syncSession(ctx, e)
	if err != nil {
		return err
	}
	held, err := m.Sync.Sessions(ctx)
	if err != nil {
		return err
	}

	steps := m.Out.Steps(2)
	steps.Step("terminating " + session.Name)
	if slices.Contains(held, session.Name) {
		if err := m.Sync.Terminate(ctx, session.Name); err != nil {
			return err
		}
	}

	steps.Step("creating it again and waiting for its first full pass")
	if err := m.Sync.Create(ctx, session, m.Out.Stream()); err != nil {
		return err
	}
	m.recordFlush(e)

	m.Out.OK("reset the sync session for " + e.Name().String())
	m.Out.Note("source: " + syncer.AppsDir(e.Dir) + " — edits reach the container in about a second")
	return nil
}

// SyncStopDaemon stops the Mutagen daemon tamp runs. Sessions go with their
// environments; the daemon is what outlives them all, and this is the only
// thing that stops it.
func (m *Manager) SyncStopDaemon(ctx context.Context) error {
	if _, err := m.Sync.Find(ctx); err != nil {
		m.Out.OK("this machine has no Mutagen, so tamp is running no daemon")
		return nil
	}
	if err := m.Sync.StopDaemon(ctx); err != nil {
		return err
	}
	m.Out.OK("stopped tamp's Mutagen daemon")
	m.Out.Note("the next 'tamp start' of a synced environment starts it again")
	return nil
}

// syncing resolves the environment every sync subcommand starts from, and
// reports whether it has a session at all. In bind and off mode it says so
// and syncs is false: the mode is an answer, not a failure, so the
// subcommand is finished and exits 0.
func (m *Manager) syncing(name string) (e *Environment, syncs bool, err error) {
	e, err = m.resolve(name)
	if err != nil {
		return nil, false, err
	}

	mode := syncer.Resolve(e.Config.Sync.Mode, runtime.GOOS)
	if mode == syncer.UseMutagen {
		return e, true, nil
	}
	m.Out.Print(fmt.Sprintf("mode: %s — sync not applicable", mode))
	if mode == syncer.UseBind {
		m.Out.Note(syncer.AppsDir(e.Dir) + " is bound straight into the container, so there is nothing to synchronize")
	} else {
		m.Out.Note("this environment keeps its source in the container and syncs nothing to the host")
	}
	return e, false, nil
}

// startsTheSession is the one thing that puts a session back, said the same
// way wherever tamp finds none.
func startsTheSession(e *Environment) string {
	return fmt.Sprintf("start the environment to create it: tamp start %s", e.Name())
}

// requireSession names the session to act on, refusing when Mutagen is not
// holding one — acting on a session that is not there is not a flush.
func (m *Manager) requireSession(ctx context.Context, e *Environment) (string, error) {
	session := e.Resources.Project()
	held, err := m.Sync.Sessions(ctx)
	if err != nil {
		return "", err
	}
	if !slices.Contains(held, session) {
		return "", exitcode.New(exitcode.CodeNotFound,
			fmt.Sprintf("Mutagen is not holding a sync session for %s", e.Name()),
			startsTheSession(e))
	}
	return session, nil
}

// recordFlush notes the moment. A failure here is a warning: the flush
// itself already happened, and the date is only a report.
func (m *Manager) recordFlush(e *Environment) {
	path := lastFlushPath(e.Dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		m.Out.Warn(fmt.Sprintf("could not record the flush in %s: %v", path, err))
		return
	}
	if err := os.WriteFile(path, []byte(time.Now().Format(time.RFC3339)+"\n"), 0o644); err != nil {
		m.Out.Warn(fmt.Sprintf("could not record the flush in %s: %v", path, err))
	}
}

// lastFlush is when tamp last forced a pass, in words. Mutagen flushes on its
// own continuously; this date is only about the forced ones.
func (m *Manager) lastFlush(e *Environment) string {
	body, err := os.ReadFile(lastFlushPath(e.Dir))
	if err != nil {
		return "never forced"
	}
	return strings.TrimSpace(string(body)) + " (last forced)"
}
