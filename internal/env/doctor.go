package env

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
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
		r.warn("Mutagen "+syncer.Version+" is not installed, or it does not run", startFix("downloads it"))
	}

	if dockerErr != nil {
		// Only Docker can tell whether the router is what holds port 80.
		again := "`tamp doctor`"
		if envFlag != "" {
			again = "`tamp doctor -e " + envFlag + "`"
		}
		r.warn("tamp did not check port 80, the router, or the environment, because Docker does not answer",
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
		r.pass("no environment checked, because this directory holds no recipe: to check one, run `tamp doctor -e <name>` with a name from `tamp list`")
	}

	if r.failed > 0 {
		return exitcode.New(r.first, fmt.Sprintf("%d of the checks failed", r.failed),
			"follow each failed check's fix above")
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
		r.fail(exitcode.New(exitcode.CodeFailed, "git is not on PATH, and tamp clones each app with it",
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
		r.fail(router.PortTaken(ctx, m.docker, fmt.Sprintf("something other than the router holds port %d", router.Port)))
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
		r.pass("the router is not running, and no running environment needs it")
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
	// Only a recipe on disk is read: a moved environment's old directory has none.
	path := filepath.Join(t.dir, RecipeFile)
	if _, err := os.Stat(path); err == nil {
		if _, err := readRecipe(t.dir); err != nil {
			r.fail(err)
		} else {
			r.pass("the recipe of " + t.name + " reads")
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		r.fail(exitcode.New(exitcode.CodeFailed, fmt.Sprintf("cannot read %s: %v", path, err),
			"check that you can read "+t.dir))
	}
	containers, err := m.envContainers(ctx, t.name)
	if err != nil {
		r.fail(err)
		return
	}
	st := state(t.name, containers)
	switch st {
	case stateNotBuilt:
		r.warn("the environment "+t.name+" is not built", start("builds it"))
		return
	case statePartly:
		r.warn("the environment "+t.name+" is partly running", start("starts the rest"))
	case statePaused:
		r.warn(pausedFix(containers))
	case stateRunning:
		if crash, err := m.crashLoop(ctx, t); err != nil {
			r.fail(err)
		} else if crash != nil {
			r.fail(crash)
		} else {
			r.pass("the environment " + t.name + " is running")
		}
	default:
		r.pass("the environment " + t.name + " is " + st.String())
	}
	frappeRunning, frappePaused := false, false
	for _, c := range containers {
		if c.Name != frappeContainer(t.name) {
			continue
		}
		frappeRunning, frappePaused = c.Running, c.Paused
		if c.Paused {
			// A paused bench's fix, `tamp start`, waits for the unpause above.
			continue
		}
		_, err := m.docker.ReadFile(ctx, c.Name, frappe.Paused)
		switch {
		case err == nil:
			r.warn("the bench of "+t.name+" holds its processes down, because a build stopped partway", start("finishes the build"))
		case !errors.Is(err, docker.ErrNotExist):
			r.fail(err)
		}
	}
	if m.sync != nil {
		m.checkSync(ctx, r, t, frappeRunning, frappePaused)
	}
}

// checkSync judges the session by the frappe container, which it syncs with.
func (m *Manager) checkSync(ctx context.Context, r *report, t target, frappeRunning, frappePaused bool) {
	start := func(what string) string { return "run " + t.command("start") + ", which " + what }
	spec := syncSpec(t)
	of := "the sync session of " + t.name
	// Nothing syncs with a frozen container, and `tamp start` refuses one.
	if frappePaused {
		r.warn(of+" cannot sync, because "+spec.Container+" is paused", unpauseFix([]string{spec.Container}))
		return
	}
	s, err := m.sync.Peek(ctx, spec.Name)
	if err == nil && s == nil {
		var copying *syncer.Session
		if copying, err = m.sync.Peek(ctx, syncer.ReplicaName(spec.Name)); err == nil && copying != nil {
			r.warn("the first copy of apps/ into the container of "+t.name+" did not finish", start("makes the sync session"))
			return
		}
	}
	var run *syncer.RunError
	switch {
	case errors.Is(err, syncer.ErrNoDaemon) && frappeRunning:
		r.warn("the Mutagen daemon is not running, so the apps/ of "+t.name+" does not sync", start("starts it"))
	case errors.Is(err, syncer.ErrNoDaemon):
		r.pass("nothing syncs, because neither the frappe container of " + t.name + " nor the Mutagen daemon is running")
	case errors.As(err, &run):
		r.warn("tamp cannot read "+of+", because Mutagen does not run", start("downloads Mutagen again"))
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
		r.pass(of + " is paused, because its frappe container is stopped")
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
