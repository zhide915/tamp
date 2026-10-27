package env

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/frappe"
	"github.com/zhide915/tamp/internal/syncer"
)

// syncIgnore is what the sync skips: node packages, Python bytecode, the
// Python build output at an app's root, and the dot folders that tamp clones
// into before it renames them. bench build's public/dist syncs.
var syncIgnore = []string{"node_modules", "__pycache__", "*.pyc", "/*/build/", "/*/dist/", "*.egg-info/", "/.*/"}

// sessionName is the environment's sync session. Mutagen allows only letters,
// digits and `-`, so `-` doubles and `_` becomes `-u`, which keeps names apart.
func sessionName(name string) string {
	return "tamp-" + strings.NewReplacer("-", "--", "_", "-u").Replace(name)
}

func syncSpec(t target) syncer.Spec {
	return syncer.Spec{
		Name:      sessionName(t.name),
		Host:      filepath.Join(t.dir, AppsDir),
		Container: frappeContainer(t.name),
		Path:      frappe.AppsDir,
		Ignore:    syncIgnore,
	}
}

// installSync downloads Mutagen unless this machine has a copy that runs, or
// needs none.
func (m *Manager) installSync(ctx context.Context) error {
	if m.sync == nil {
		return nil
	}
	installed, err := m.sync.Installed(ctx)
	if err != nil || installed {
		return err
	}
	m.p.Step("downloading Mutagen " + syncer.Version)
	return m.sync.Install(ctx)
}

// ensureSync syncs apps/ into the running frappe container and waits until
// both sides match. A session that syncs another folder, as after a move, or
// that Mutagen halted, is made anew.
func (m *Manager) ensureSync(ctx context.Context, t target) error {
	if m.sync == nil {
		return nil
	}
	if err := m.syncNow(ctx, t); err != nil {
		var run *syncer.RunError
		if errors.As(err, &run) {
			return exitcode.New(exitcode.CodeFailed, run.Error(),
				"check that security software lets programs in "+filepath.Dir(run.Bin)+" run")
		}
		return err
	}
	return nil
}

func (m *Manager) syncNow(ctx context.Context, t target) error {
	spec := syncSpec(t)
	s, err := m.sync.Session(ctx, spec.Name)
	if err != nil {
		return err
	}
	switch {
	case s == nil:
		m.p.Step("creating the sync session of " + t.name)
		err = m.createSync(ctx, spec)
	case !samePath(s.Host, spec.Host) || s.Container != spec.Container || s.Path != spec.Path:
		// stop finished the sync, so the moved folder holds what the container wrote.
		err = m.recreateSync(ctx, t.name, spec, "it syncs "+s.Host)
	default:
		err = m.sync.Resume(ctx, spec.Name)
	}
	if err != nil {
		return err
	}
	m.p.Step("syncing apps/ with the frappe container")
	if err := m.sync.Flush(ctx, spec.Name); err != nil {
		// Mutagen halts a session for safety, and a paused one shows it only now.
		s, sErr := m.sync.Session(ctx, spec.Name)
		if sErr != nil {
			return sErr
		}
		if s == nil || !s.Halted() {
			return err
		}
		m.p.Warn(fmt.Sprintf("Mutagen halted the sync session of %s (%s), so files written only in the container since then are replaced by the host's",
			t.name, s.Status))
		if err := m.recreateSync(ctx, t.name, spec, "Mutagen halted it"); err != nil {
			return err
		}
		if err := m.sync.Flush(ctx, spec.Name); err != nil {
			return err
		}
	}
	return m.warnConflicts(ctx, t, spec.Name)
}

func (m *Manager) recreateSync(ctx context.Context, name string, spec syncer.Spec, why string) error {
	m.p.Step(fmt.Sprintf("recreating the sync session of %s, as %s", name, why))
	if err := m.sync.Terminate(ctx, spec.Name); err != nil {
		return err
	}
	return m.createSync(ctx, spec)
}

// createSync first makes the container's apps/ a copy of the host's: a new
// two-way session would bring back to the host what it deleted meanwhile.
func (m *Manager) createSync(ctx context.Context, spec syncer.Spec) error {
	m.p.Step("copying apps/ into the frappe container")
	if err := m.sync.Replicate(ctx, spec); err != nil {
		return err
	}
	return m.sync.Create(ctx, spec)
}

// warnConflicts names each change the sync cannot settle. They involve files
// the sync skips, which neither side deletes for the other.
func (m *Manager) warnConflicts(ctx context.Context, t target, name string) error {
	s, err := m.sync.Session(ctx, name)
	if err != nil || s == nil {
		return err
	}
	for _, c := range s.Conflicts {
		folder := conflictFolder(c)
		host := filepath.Join(t.dir, filepath.FromSlash(folder))
		if c.HostDeleted {
			m.p.Warn(fmt.Sprintf("the sync cannot delete %s in the container, which holds files the sync skips: delete them with %s%s",
				folder, shellLine(t.flag, false, shellWords([]string{"rm", "-rf", folder})), t.in()))
			continue
		}
		m.p.Warn(fmt.Sprintf("the sync cannot settle %s, where one side holds files the sync skips: delete %s on the host, or %s in the container with %s%s",
			folder, host, folder, shellLine(t.flag, false, shellWords([]string{"rm", "-rf", folder})), t.in()))
	}
	return nil
}

func conflictFolder(c syncer.Conflict) string {
	return strings.TrimSuffix(AppsDir+"/"+c.Path, "/")
}

// syncTrouble is why a sync command failed, with its fix: a Mutagen that
// cannot run is downloaded again by the next start.
func syncTrouble(t target, err error) string {
	var run *syncer.RunError
	if errors.As(err, &run) {
		return run.Error() + ": " + t.command("start") + " downloads it again"
	}
	return err.Error()
}

// pauseSync finishes the sync, so the host holds what the container wrote even
// if its folder then moves, and pauses it. A failure is a warning only.
func (m *Manager) pauseSync(ctx context.Context, t target) {
	if m.sync == nil {
		return
	}
	name := sessionName(t.name)
	s, err := m.sync.Session(ctx, name)
	if err == nil && s != nil && !s.Paused {
		m.p.Step("finishing the sync of " + t.name)
		if err = m.sync.Flush(ctx, name); err != nil {
			m.p.Warn(fmt.Sprintf("cannot finish the sync of %s, so files written in the container since its last sync stay there: %s",
				t.name, syncTrouble(t, err)))
		}
		m.p.Step("pausing the sync session of " + t.name)
		err = m.sync.Pause(ctx, name)
	}
	if err != nil {
		m.p.Warn(fmt.Sprintf("cannot pause the sync session of %s: %s", t.name, syncTrouble(t, err)))
	}
}

// terminateSync deletes the session and any copy a killed start left, with
// tamp's Mutagen, which it repairs first. A failure is a warning only.
func (m *Manager) terminateSync(ctx context.Context, t target) {
	if m.sync == nil {
		return
	}
	err := m.installSync(ctx)
	sessions := [][2]string{
		{sessionName(t.name), "the sync session of " + t.name},
		{syncer.ReplicaName(sessionName(t.name)), "the unfinished copy into the container of " + t.name},
	}
	for _, session := range sessions {
		name, what := session[0], session[1]
		if err != nil {
			break
		}
		var s *syncer.Session
		if s, err = m.sync.Session(ctx, name); err == nil && s != nil {
			m.p.Step("terminating " + what)
			err = m.sync.Terminate(ctx, name)
		}
	}
	if err == nil {
		return
	}
	var run *syncer.RunError
	trouble := err.Error()
	if errors.As(err, &run) {
		trouble = run.Error() + ": check that security software lets programs in " + filepath.Dir(run.Bin) + " run"
	}
	m.p.Warn(fmt.Sprintf("cannot terminate the sync session of %s: %s, then run `tamp remove --yes` in %s", t.name, trouble, t.dir))
}

// syncState sums up the sync of apps/ for status.
func (m *Manager) syncState(ctx context.Context, t target) string {
	if m.sync == nil {
		return "apps/ is bind-mounted"
	}
	name := sessionName(t.name)
	s, err := m.sync.Session(ctx, name)
	if err == nil && s == nil {
		var copying *syncer.Session
		if copying, err = m.sync.Session(ctx, syncer.ReplicaName(name)); err == nil && copying != nil {
			return "none, as its first copy into the container never finished: " + t.command("start") + " makes it"
		}
	}
	switch {
	case err != nil:
		return "unknown: " + syncTrouble(t, err)
	case s == nil:
		return "none: create it with " + t.command("start")
	}
	state := strings.ReplaceAll(s.Status, "-", " ")
	switch {
	case s.Paused:
		state = "paused"
	case s.Halted():
		state += ": " + t.command("start") + " recreates it"
	}
	var folders []string
	for _, c := range s.Conflicts {
		folders = append(folders, conflictFolder(c))
	}
	if len(folders) > 0 {
		state += ", and cannot settle " + strings.Join(folders, ", ")
	}
	return state
}
