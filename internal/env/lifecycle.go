package env

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/zhide915/tamp/internal/docker"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/frappe"
)

// dbVolume is compose's name for the environment's db volume.
func dbVolume(name string) string { return name + "_db" }

// Stop stops the environment's containers and takes its sites off the
// router.
func (m *Manager) Stop(envFlag string) error {
	ctx := context.Background()
	t, err := m.locate(ctx, envFlag)
	if err != nil {
		return err
	}
	containers, err := m.envContainers(ctx, t.name)
	if err != nil {
		return err
	}
	// Paused first, so Mutagen never sees the container go.
	m.pauseSync(ctx, t, slices.ContainsFunc(containers, func(c docker.Container) bool {
		return c.Name == frappeContainer(t.name) && c.Running
	}))
	for _, c := range containers {
		if !c.Running {
			continue
		}
		m.p.Step("stopping " + c.Name)
		if err := m.docker.StopContainer(ctx, c.Name); err != nil {
			return err
		}
	}
	if err := m.reroute(ctx, t.name); err != nil {
		return err
	}
	m.p.OK("environment " + t.name + " is stopped")
	return nil
}

// Remove deletes the environment's sync session, containers, network and
// volumes, and keeps its directory. Without yes it removes nothing.
func (m *Manager) Remove(envFlag string, yes bool) error {
	ctx := context.Background()
	t, err := m.locate(ctx, envFlag)
	if err != nil {
		return err
	}
	if !yes {
		return exitcode.New(exitcode.CodeNeedsYes,
			fmt.Sprintf("removing %s deletes its containers, network and volumes, with every site's database", t.name),
			"run "+t.command("remove --yes")+" to confirm")
	}
	m.terminateSync(ctx, t)
	containers, err := m.envContainers(ctx, t.name)
	if err != nil {
		return err
	}
	r, err := m.router()
	if err != nil {
		return err
	}
	// Docker refuses to remove a network the router is still on.
	if err := r.Leave(ctx, network(t.name)); err != nil {
		return err
	}
	for _, c := range containers {
		m.p.Step("removing " + c.Name)
		if err := m.docker.RemoveContainer(ctx, c.Name); err != nil {
			return err
		}
	}
	if err := m.docker.RemoveNetwork(ctx, network(t.name)); err != nil {
		return err
	}
	// Compose treats the bench volume as external, so tamp removes both itself.
	for _, v := range []string{dbVolume(t.name), benchVolume(t.name)} {
		if err := m.docker.RemoveVolume(ctx, v); err != nil {
			return err
		}
	}
	if err := m.reroute(ctx, t.name); err != nil {
		return err
	}
	m.p.OK(fmt.Sprintf("removed environment %s, and kept its directory %s", t.name, t.dir))
	return nil
}

// List prints every environment that has containers, with its sites
// beneath it.
func (m *Manager) List() error {
	ctx := context.Background()
	all, err := m.docker.AllContainers(ctx, LabelEnv)
	if err != nil {
		return err
	}
	var names []string
	byName := map[string][]docker.Container{}
	for _, c := range all {
		name := c.Labels[LabelEnv]
		if _, seen := byName[name]; !seen {
			names = append(names, name)
		}
		byName[name] = append(byName[name], c)
	}
	slices.Sort(names)
	if len(names) == 0 {
		m.p.Step("no environment has containers: create one with `tamp new <path> --frappe <branch>`")
		return nil
	}
	width := 0
	for _, name := range names {
		width = max(width, len(name))
	}
	for _, name := range names {
		containers := byName[name]
		dir := containers[0].Labels[LabelPath]
		st := m.benchState(ctx, target{name: name, dir: dir, flag: name}, containers)
		m.p.Print(fmt.Sprintf("%-*s  %-14s  %s", width, name, st, dir))
		for _, url := range m.siteURLs(name, dir) {
			m.p.Print("  " + url)
		}
	}
	return nil
}

// Status prints one environment's containers, sync state and site URLs.
func (m *Manager) Status(envFlag string) error {
	ctx := context.Background()
	t, err := m.locate(ctx, envFlag)
	if err != nil {
		return err
	}
	containers, err := m.envContainers(ctx, t.name)
	if err != nil {
		return err
	}
	st := m.benchState(ctx, t, containers)
	m.p.Print(fmt.Sprintf("environment %s: %s", t.name, st))
	m.p.Print("directory: " + t.dir)
	m.p.Print("containers:")
	if len(containers) == 0 {
		m.p.Print("  none: build them with " + t.command("start"))
	}
	width := 0
	for _, c := range containers {
		width = max(width, len(c.Name))
	}
	for _, c := range containers {
		running := "stopped"
		switch {
		case c.Paused:
			running = "paused"
		case c.Running:
			running = "running"
		}
		m.p.Print(fmt.Sprintf("  %-*s  %s", width, c.Name, running))
	}
	if len(containers) > 0 {
		for _, name := range missingContainers(t.name, containers) {
			m.p.Print(fmt.Sprintf("  %-*s  missing", width, name))
		}
	}
	m.p.Print("sync: " + m.syncState(ctx, t))
	m.p.Print("sites:")
	urls := m.siteURLs(t.name, t.dir)
	if len(urls) == 0 {
		m.p.Print("  none")
	}
	for _, url := range urls {
		m.p.Print("  " + url)
	}
	return nil
}

// siteURLs reads the sites of the recipe in dir. A recipe it cannot read is
// a warning, so one broken environment never hides the rest.
func (m *Manager) siteURLs(name, dir string) []string {
	r, err := readRecipe(dir)
	if err != nil {
		m.p.Warn(fmt.Sprintf("cannot list the sites of %s: %s", name, reason(err)))
		return nil
	}
	var urls []string
	for _, s := range r.Sites {
		urls = append(urls, "http://"+s.Host)
	}
	return urls
}

// runState sums up an environment's containers.
type runState int

const (
	stateNotBuilt runState = iota
	stateRunning
	stateStopped
	statePartly
	// stateCrashing is running, with honcho exiting again and again.
	stateCrashing
	// statePaused has a container that `docker pause` froze, which `tamp start`
	// cannot start.
	statePaused
)

// String is the state in a word or two, as list and status print it.
func (s runState) String() string {
	return [...]string{"not built", "running", "stopped", "partly running", "crash looping", "paused"}[s]
}

// benchState is state, with a running environment whose bench is crash
// looping read as such, and a paused or crash looping one warned about. A
// bench tamp cannot ask keeps the state its containers show, with a warning,
// so one never hides the rest.
func (m *Manager) benchState(ctx context.Context, t target, containers []docker.Container) runState {
	st := state(t.name, containers)
	if st == statePaused {
		msg, fix := pausedFix(containers)
		m.p.Warn(msg + ": " + fix)
	}
	if st != stateRunning {
		return st
	}
	crash, err := m.crashLoop(ctx, t)
	switch {
	case err != nil:
		m.p.Warn(fmt.Sprintf("cannot tell whether the bench processes of %s keep crashing: %v", t.name, err))
	case crash != nil:
		m.p.Warn(crash.Error())
		return stateCrashing
	}
	return st
}

// crashLoop is the error for t's bench crash looping, or nil when it is not.
// It runs a read-only query in the running frappe container.
func (m *Manager) crashLoop(ctx context.Context, t target) (*exitcode.Error, error) {
	var out bytes.Buffer
	if err := m.docker.Exec(ctx, docker.Exec{Container: frappeContainer(t.name), Cmd: frappe.RecentExits(), Stdout: &out}); err != nil {
		return nil, err
	}
	n := len(strings.Fields(out.String()))
	if n < frappe.CrashExits {
		return nil, nil
	}
	return exitcode.New(exitcode.CodeFailed,
		fmt.Sprintf("the bench processes of %s keep crashing: honcho exited %d times in the last %d minutes",
			t.name, n, frappe.CrashWindow/60),
		"see why with "+t.command("logs frappe")), nil
}

// state counts the environment's services, so a container deleted by hand
// reads as partly running.
func state(name string, containers []docker.Container) runState {
	up := 0
	for _, c := range containers {
		if c.Running {
			up++
		}
	}
	switch {
	case len(containers) == 0:
		return stateNotBuilt
	case len(paused(containers)) > 0:
		return statePaused
	case up == 0:
		return stateStopped
	case up == len(containers) && len(missingContainers(name, containers)) == 0:
		return stateRunning
	}
	return statePartly
}

// missingContainers is the containers of name's services that do not exist.
func missingContainers(name string, containers []docker.Container) []string {
	var out []string
	for _, s := range envServices {
		want := serviceContainer(name, s)
		if !slices.ContainsFunc(containers, func(c docker.Container) bool { return c.Name == want }) {
			out = append(out, want)
		}
	}
	return out
}

// paused names the containers that `docker pause` froze.
func paused(containers []docker.Container) []string {
	var names []string
	for _, c := range containers {
		if c.Paused {
			names = append(names, c.Name)
		}
	}
	return names
}

// pausedFix says which containers are paused, and how to unpause them:
// `tamp start` cannot, as Docker refuses to start a paused container.
func pausedFix(containers []docker.Container) (msg, fix string) {
	names := paused(containers)
	verb, them := "is", "it"
	if len(names) > 1 {
		verb, them = "are", "them"
	}
	return fmt.Sprintf("%s %s paused, so the environment's sites do not answer", strings.Join(names, ", "), verb),
		fmt.Sprintf("unpause %s with `docker unpause %s`", them, strings.Join(names, " "))
}
