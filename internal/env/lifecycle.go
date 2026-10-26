package env

import (
	"context"
	"fmt"
	"slices"

	"github.com/zhide915/tamp/internal/docker"
	"github.com/zhide915/tamp/internal/exitcode"
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

// Remove deletes the environment's containers, network and volumes, and
// keeps its directory. Without yes it removes nothing.
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
		m.p.Print(fmt.Sprintf("%-*s  %-14s  %s", width, name, state(containers), dir))
		for _, url := range m.siteURLs(name, dir) {
			m.p.Print("  " + url)
		}
	}
	return nil
}

// Status prints one environment's containers and site URLs.
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
	m.p.Print(fmt.Sprintf("environment %s: %s", t.name, state(containers)))
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
		if c.Running {
			running = "running"
		}
		m.p.Print(fmt.Sprintf("  %-*s  %s", width, c.Name, running))
	}
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

// state sums up an environment's containers in a word or two.
func state(containers []docker.Container) string {
	running := 0
	for _, c := range containers {
		if c.Running {
			running++
		}
	}
	switch {
	case len(containers) == 0:
		return "not built"
	case running == len(containers):
		return "running"
	case running == 0:
		return "stopped"
	}
	return "partly running"
}
