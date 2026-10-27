package env

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"

	"github.com/zhide915/tamp/internal/docker"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/frappe"
	"github.com/zhide915/tamp/internal/router"
	"github.com/zhide915/tamp/internal/syncer"
	"github.com/zhide915/tamp/internal/ui"
)

// report prints one line per check and keeps the first failure's code.
type report struct {
	p      *ui.Printer
	failed int
	first  exitcode.Code
}

func (r *report) pass(msg string) { r.p.OK(msg) }

func (r *report) warn(msg, fix string) { r.p.Caution(msg + ": " + fix) }

func (r *report) fail(err error) {
	r.p.Fail(err.Error())
	if r.failed == 0 {
		r.first = exitcode.Of(err)
	}
	r.failed++
}

// Doctor checks this machine, and the environment that envFlag or the
// working directory names, and repairs nothing: it only reads.
func (m *Manager) Doctor(envFlag string) error {
	ctx := context.Background()
	r := &report{p: m.p}

	dockerErr := m.docker.Ping(ctx)
	if dockerErr != nil {
		r.fail(dockerErr)
	} else {
		r.pass("Docker answers")
	}
	if v, err := m.docker.ComposeVersion(ctx); err != nil {
		r.fail(err)
	} else {
		r.pass("Docker Compose " + v)
	}
	checkGit(ctx, r)

	t, found, findErr := m.doctorTarget(ctx, envFlag, dockerErr == nil)
	startFix := func(what string) string {
		if !found {
			return "run `tamp start` in an environment's directory, which " + what
		}
		return "run " + t.command("start") + ", which " + what
	}

	if m.sync == nil {
		r.pass("no Mutagen needed: apps/ is bind-mounted")
	} else if installed, err := m.sync.Installed(ctx); err != nil {
		r.fail(err)
	} else if installed {
		r.pass("Mutagen " + syncer.Version)
	} else {
		r.warn("Mutagen "+syncer.Version+" is not installed, or does not run", startFix("downloads it"))
	}

	if dockerErr != nil {
		// Only Docker can tell whether the router is what holds port 80.
		again := "`tamp doctor`"
		if envFlag != "" {
			again = "`tamp doctor -e " + envFlag + "`"
		}
		r.warn("port 80, the router and the environment are not checked, as Docker does not answer",
			"start Docker, then run "+again+" again")
	} else {
		m.checkRouter(ctx, r, t, found)
	}
	switch {
	case findErr != nil:
		r.fail(findErr)
	case dockerErr != nil:
	case found:
		m.checkEnv(ctx, r, t)
	default:
		r.pass("no environment checked, as this directory holds no recipe: run `tamp doctor -e <name>` with a name from `tamp list`")
	}

	if r.failed > 0 {
		return exitcode.New(r.first, fmt.Sprintf("%d of tamp doctor's checks failed", r.failed),
			"follow the fix on each failed check above")
	}
	return nil
}

// doctorTarget is the environment to check: none when the working directory
// holds no recipe and no -e names one. Its error is reported in its turn.
func (m *Manager) doctorTarget(ctx context.Context, envFlag string, dockerUp bool) (target, bool, error) {
	// Only Docker can find it, but the fixes can still name it.
	if envFlag != "" && !dockerUp {
		return target{name: envFlag, flag: envFlag}, true, nil
	}
	t, err := m.find(ctx, envFlag)
	var e *exitcode.Error
	switch {
	case envFlag == "" && errors.As(err, &e) && e.Code == exitcode.CodeUsage:
		return target{}, false, nil
	case err != nil:
		return target{}, false, err
	}
	return t, true, nil
}

func checkGit(ctx context.Context, r *report) {
	out, err := exec.CommandContext(ctx, "git", "--version").Output()
	switch {
	case errors.Is(err, exec.ErrNotFound):
		r.fail(exitcode.New(exitcode.CodeFailed, "git is not on PATH, and tamp clones apps with it",
			"install git from https://git-scm.com/downloads"))
	case err != nil:
		r.fail(exitcode.New(exitcode.CodeFailed, fmt.Sprintf("git does not run: %v", err),
			"install git again from https://git-scm.com/downloads"))
	default:
		r.pass(strings.TrimSpace(string(out)))
	}
}

// checkRouter connects to port 80 only while the router is not running, so
// whatever answers there is something else.
func (m *Manager) checkRouter(ctx context.Context, r *report, t target, found bool) {
	c, err := m.docker.Inspect(ctx, router.Container)
	switch {
	case err == nil && c.Running:
		r.pass(fmt.Sprintf("the router is running, on port %d", router.Port))
		return
	case err != nil && !errors.Is(err, docker.ErrNoContainer):
		r.fail(err)
		return
	}
	if router.PortHeld(ctx) {
		r.fail(router.PortTaken(ctx, m.docker, fmt.Sprintf("port %d is taken by something other than the router", router.Port)))
	} else {
		r.pass(fmt.Sprintf("port %d is free for the router", router.Port))
	}
	running, err := m.docker.Containers(ctx, LabelEnv)
	if err != nil {
		r.fail(err)
		return
	}
	var names []string
	for _, c := range running {
		if name := c.Labels[LabelEnv]; !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		r.pass("the router is not running, and no environment needs it")
		return
	}
	starter := target{name: names[0], flag: names[0]}
	if found && slices.Contains(names, t.name) {
		// From a directory whose name another environment holds, start fails.
		// checkEnv reports that error in its turn.
		if t.flag != "" {
			starter = t
		} else if _, err := m.owner(ctx, t); err == nil {
			starter = t
		}
	}
	r.warn("the router is not running, so no site of "+strings.Join(names, ", ")+" answers",
		"run "+starter.command("start")+", which starts it")
}

// checkEnv reads the environment's containers, its bench and its sync session.
func (m *Manager) checkEnv(ctx context.Context, r *report, t target) {
	start := func(what string) string { return "run " + t.command("start") + ", which " + what }
	// The containers named like a directory's environment may be another's.
	if t.flag == "" {
		moved, err := m.owner(ctx, t)
		if err != nil {
			r.fail(err)
			return
		}
		if moved != "" {
			r.warn(fmt.Sprintf("%s moved from %s to %s", t.name, moved, t.dir), start("recreates its containers for the new directory"))
		}
	}
	containers, err := m.envContainers(ctx, t.name)
	if err != nil {
		r.fail(err)
		return
	}
	st := state(containers)
	switch st {
	case stateNotBuilt:
		r.warn("the environment "+t.name+" is not built", start("builds it"))
		return
	case statePartly:
		r.warn("the environment "+t.name+" is partly running", start("starts the rest"))
	default:
		r.pass("the environment " + t.name + " is " + st.String())
	}
	frappeRunning := false
	for _, c := range containers {
		if c.Name != frappeContainer(t.name) {
			continue
		}
		frappeRunning = c.Running
		_, err := m.docker.ReadFile(ctx, c.Name, frappe.Paused)
		switch {
		case err == nil:
			r.warn("the bench of "+t.name+" keeps its processes down, as a build stopped partway", start("finishes it"))
		case !errors.Is(err, docker.ErrNotExist):
			r.fail(err)
		}
	}
	if m.sync != nil {
		m.checkSync(ctx, r, t, frappeRunning)
	}
}

// checkSync judges the session by the frappe container, which it syncs with.
func (m *Manager) checkSync(ctx context.Context, r *report, t target, frappeRunning bool) {
	start := func(what string) string { return "run " + t.command("start") + ", which " + what }
	spec := syncSpec(t)
	of := "the sync session of " + t.name
	s, err := m.sync.Peek(ctx, spec.Name)
	if err == nil && s == nil {
		var copying *syncer.Session
		if copying, err = m.sync.Peek(ctx, syncer.ReplicaName(spec.Name)); err == nil && copying != nil {
			r.warn("the first copy of apps/ into the container of "+t.name+" never finished", start("makes the sync session"))
			return
		}
	}
	var run *syncer.RunError
	switch {
	case errors.Is(err, syncer.ErrNoDaemon) && frappeRunning:
		r.warn("Mutagen's daemon is not running, so apps/ of "+t.name+" does not sync", start("starts it"))
	case errors.Is(err, syncer.ErrNoDaemon):
		r.pass("nothing syncs, as neither the frappe container of " + t.name + " nor Mutagen's daemon is running")
	case errors.As(err, &run):
		r.warn(of+" cannot be read, as Mutagen does not run", start("downloads it again"))
	case err != nil:
		r.fail(err)
	case s == nil:
		r.warn(t.name+" has no sync session", start("makes it"))
	case !samePath(s.Host, spec.Host) || s.Container != spec.Container || s.Path != spec.Path:
		r.warn(of+" syncs "+s.Host, start("makes it sync "+spec.Host))
	case s.Halted():
		r.warn(of+" is "+statusWords(s.Status), start("makes it anew"))
	case s.Paused && frappeRunning:
		r.warn(of+" is paused", start("resumes it"))
	case s.Paused:
		r.pass(of + " is paused, as its frappe container is stopped")
	case s.Status == "disconnected" || strings.HasPrefix(s.Status, "connecting"):
		r.warn(of+" is "+statusWords(s.Status), start("connects it again"))
	case len(s.Conflicts) > 0:
		var folders []string
		for _, c := range s.Conflicts {
			folders = append(folders, conflictFolder(c))
		}
		r.warn(of+" cannot settle "+strings.Join(folders, ", "), start("names how to settle each"))
	default:
		r.pass(of + " is " + statusWords(s.Status))
	}
}
