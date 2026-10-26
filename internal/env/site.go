package env

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/zhide915/tamp/internal/docker"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/frappe"
)

// SiteAdd adds host to the recipe and builds the environment, which creates
// the site and routes it. A hostname any environment's recipe lists is
// refused before anything is written.
func (m *Manager) SiteAdd(envFlag, host string) error {
	if !localhostName.MatchString(host) {
		return exitcode.Usage(fmt.Sprintf("%q is not a *.localhost hostname", host), siteHostFix)
	}
	ctx := context.Background()
	t, err := m.find(ctx, envFlag)
	if err != nil {
		return err
	}
	r, err := readRecipe(t.dir)
	if err != nil {
		return err
	}
	if slices.Contains(r.hosts(), host) {
		return exitcode.New(exitcode.CodeFailed, fmt.Sprintf("%s already lists the site %s", t.name, host),
			"run "+t.command("start")+" to build it")
	}
	if err := m.docker.Ping(ctx); err != nil {
		return err
	}
	// The build makes the same check, but only after the recipe is written.
	if _, err := m.owner(ctx, t); err != nil {
		return err
	}
	if err := m.checkHostFree(ctx, t.name, host); err != nil {
		return err
	}
	r.Sites = append(r.Sites, Site{Host: host})
	if err := writeRecipe(t.dir, r); err != nil {
		return err
	}
	return m.build(t, r)
}

// checkHostFree refuses a hostname that another environment's recipe lists,
// running or not. A recipe it cannot read is a warning, as for the router.
func (m *Manager) checkHostFree(ctx context.Context, name, host string) error {
	all, err := m.docker.AllContainers(ctx, LabelEnv)
	if err != nil {
		return err
	}
	seen := map[string]bool{name: true}
	for _, c := range all {
		other := c.Labels[LabelEnv]
		if seen[other] {
			continue
		}
		seen[other] = true
		r, err := readRecipe(c.Labels[LabelPath])
		if err != nil {
			m.p.Warn(fmt.Sprintf("cannot check the sites of %s: %s", other, reason(err)))
			continue
		}
		if slices.Contains(r.hosts(), host) {
			return exitcode.New(exitcode.CodeFailed,
				fmt.Sprintf("the environment %s already lists the site %s", other, host),
				"name the site another way, or remove it from "+other+" with `tamp site remove "+host+" --yes -e "+other+"`")
		}
	}
	return nil
}

// SiteList prints each site of the recipe with its URL.
func (m *Manager) SiteList(envFlag string) error {
	t, err := m.locate(context.Background(), envFlag)
	if err != nil {
		return err
	}
	r, err := readRecipe(t.dir)
	if err != nil {
		return err
	}
	if len(r.Sites) == 0 {
		m.p.Step(t.name + " has no sites: add one with " + t.command("site add <host>"))
		return nil
	}
	width := 0
	for _, h := range r.hosts() {
		width = max(width, len(h))
	}
	for _, h := range r.hosts() {
		m.p.Print(fmt.Sprintf("%-*s  http://%s", width, h, h))
	}
	return nil
}

// SiteRemove drops the site from the bench, removes it from the recipe, and
// takes it off the router. It acts on a site the recipe lists or the bench
// holds, so it also clears a site that start warned about.
func (m *Manager) SiteRemove(envFlag, host string, yes bool) error {
	// The name becomes a path in the bench and an argument to bench.
	if host == "" || strings.ContainsAny(host, `/\`) || strings.Contains(host, "..") || strings.HasPrefix(host, "-") {
		return exitcode.Usage(fmt.Sprintf("%q is not a site name", host), siteHostFix)
	}
	ctx := context.Background()
	t, err := m.locate(ctx, envFlag)
	if err != nil {
		return err
	}
	r, err := readRecipe(t.dir)
	if err != nil {
		return err
	}
	listed := slices.Contains(r.hosts(), host)

	container := frappeContainer(t.name)
	built := false
	c, err := m.docker.Inspect(ctx, container)
	switch {
	case errors.Is(err, docker.ErrNoContainer):
	case err != nil:
		return err
	case !c.Running:
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("%s is stopped, so tamp cannot tell whether its bench holds the site %s", t.name, host),
			"run "+t.command("start")+", then "+t.command("site remove "+host+" --yes"))
	default:
		_, err := m.docker.ReadFile(ctx, container, frappe.SiteConfig(host))
		if err != nil && !errors.Is(err, docker.ErrNotExist) {
			return err
		}
		built = err == nil
	}
	// A site made by hand may have any name, so only the bench can vouch for one.
	if !built && !localhostName.MatchString(host) {
		return exitcode.Usage(fmt.Sprintf("%q is not a *.localhost hostname, nor a site the bench holds", host), siteHostFix)
	}
	if !listed && !built {
		return exitcode.New(exitcode.CodeNotFound, fmt.Sprintf("%s has no site %s", t.name, host),
			"run "+t.command("site list")+" to see its sites")
	}
	if !yes {
		return exitcode.New(exitcode.CodeNeedsYes,
			fmt.Sprintf("removing the site %s drops its database", host),
			"run "+t.command("site remove "+host+" --yes")+" to confirm")
	}

	if built {
		m.p.Step("dropping the site " + host)
		b := bench{m: m, ctx: ctx, container: container}
		if err := b.run(frappe.DropSite(host)); err != nil {
			return err
		}
	}
	if listed {
		r.Sites = slices.DeleteFunc(r.Sites, func(s Site) bool { return s.Host == host })
		if err := writeRecipe(t.dir, r); err != nil {
			return err
		}
	}
	if err := m.reroute(ctx, ""); err != nil {
		return err
	}
	m.p.OK("removed the site " + host + " from " + t.name)
	return nil
}

// hosts are the recipe's site hostnames, in order.
func (r Recipe) hosts() []string {
	var hosts []string
	for _, s := range r.Sites {
		hosts = append(hosts, s.Host)
	}
	return hosts
}
