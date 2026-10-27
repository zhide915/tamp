package env

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/zhide915/tamp/internal/docker"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/frappe"
)

// Start builds the environment that envFlag names, or the one in the working
// directory, from its recipe.
func (m *Manager) Start(envFlag string) error {
	t, err := m.find(context.Background(), envFlag)
	if err != nil {
		return err
	}
	r, err := readRecipe(t.dir)
	if err != nil {
		return err
	}
	return m.build(t, r)
}

// build makes the environment t match its recipe. It checks that the
// environment is t's alone and built for the recipe's Frappe branch before
// it builds anything.
func (m *Manager) build(t target, r Recipe) error {
	ctx := context.Background()
	if err := m.docker.Ping(ctx); err != nil {
		return t.resume(err)
	}
	if err := checkName(t.name); err != nil {
		var e *exitcode.Error
		errors.As(err, &e)
		// A directory that exists is state, not the command line.
		return t.resume(exitcode.New(exitcode.CodeFailed, e.Msg, e.Fix))
	}
	if err := m.claim(ctx, t); err != nil {
		return t.resume(err)
	}
	if err := m.checkVersion(ctx, t, r); err != nil {
		return err
	}
	return t.resume(m.steps(ctx, t, r))
}

// resume adds to err's fix the start that resumes the build: nothing rolls
// back, so every build failure ends in `tamp start`, except a fullFix.
func (t target) resume(err error) error {
	var full fullFix
	if errors.As(err, &full) {
		return full.err
	}
	return t.again(err, "start")
}

// fullFix is a build failure whose fix ends in a command that builds again
// itself, so resume adds no `tamp start`.
type fullFix struct{ err *exitcode.Error }

func (f fullFix) Error() string { return f.err.Error() }

func (f fullFix) Unwrap() error { return f.err }

// again adds to err's fix the command that runs again what failed.
func (t target) again(err error, args string) error {
	var e *exitcode.Error
	if !errors.As(err, &e) {
		return err
	}
	rerun := "run " + t.command(args)
	fix := rerun
	if e.Fix != "" {
		fix = e.Fix + ", then " + rerun
	}
	return exitcode.New(e.Code, e.Msg, fix)
}

// in is where a fix's commands run, for a build that `tamp new` started
// outside the environment's directory.
func (t target) in() string {
	if t.where == "" {
		return ""
	}
	return " in " + t.where
}

// claim checks that the containers named like the environment are its own.
// Containers of a moved environment are recreated with the new path, and the
// volumes kept.
func (m *Manager) claim(ctx context.Context, t target) error {
	moved, err := m.owner(ctx, t)
	if moved != "" {
		m.p.Warn(fmt.Sprintf("%s moved from %s to %s: tamp recreates its containers and keeps its volumes", t.name, moved, t.dir))
	}
	return err
}

// checkVersion stops a build whose bench was built for another Frappe
// branch: a major-version change would half-migrate it.
func (m *Manager) checkVersion(ctx context.Context, t target, r Recipe) error {
	v, err := m.docker.InspectVolume(ctx, benchVolume(t.name))
	if errors.Is(err, docker.ErrNoVolume) {
		return nil
	}
	if err != nil {
		return t.resume(err)
	}
	if built := v.Labels[LabelFrappe]; built != r.Frappe {
		// tamp's clone holds one branch at depth 1, so the other is fetched
		// first. On Windows a watching sync holds git's new pack files open,
		// and the fetch fails, so the stop pauses the sync first.
		checkout := fmt.Sprintf("run `tamp stop`, fetch and check out %[1]s with `git -C apps/frappe remote set-branches --add upstream %[1]s`, `git -C apps/frappe fetch --depth 1 upstream %[1]s` and `git -C apps/frappe checkout %[1]s`", r.Frappe)
		fix := checkout + ", then run `tamp remove --yes` and `tamp start`"
		// After the remove, -e finds nothing: only the directory names the
		// environment.
		if dir := t.elsewhere(); dir != "" {
			fix = "in " + dir + ", " + fix
		}
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("the bench of %s was built for Frappe %s, but the recipe names %s", t.name, built, r.Frappe), fix)
	}
	return nil
}

// elsewhere is the environment's directory when the command did not run in
// it, as given to `tamp new` or found by -e.
func (t target) elsewhere() string {
	switch {
	case t.where != "":
		return t.where
	case t.flag != "":
		return t.dir
	}
	return ""
}

func (m *Manager) steps(ctx context.Context, t target, r Recipe) error {
	dir, name := t.dir, t.name
	out := m.p.Progress()

	// First, so a Mutagen that cannot run stops the build before anything starts.
	if err := m.installSync(ctx); err != nil {
		return err
	}

	image := imageBuild(r.Frappe)
	exists, err := m.docker.ImageExists(ctx, image.Tag)
	if err != nil {
		return err
	}
	if !exists {
		m.p.Step("building the image " + image.Tag)
		image.Out = out
		if err := m.docker.BuildImage(ctx, image); err != nil {
			return err
		}
	}

	if err := m.cloneApp(ctx, dir, "frappe", FrappeURL, r.Frappe); err != nil {
		return err
	}
	if err := m.cloneApps(ctx, t, r); err != nil {
		return err
	}
	if err := m.warnClones(t, r); err != nil {
		return err
	}
	if err := checkRequired(t, r); err != nil {
		return err
	}

	if err := m.docker.EnsureVolume(ctx, docker.Volume{
		Name:   benchVolume(name),
		Labels: map[string]string{LabelEnv: name, LabelFrappe: r.Frappe},
	}); err != nil {
		return err
	}
	if err := m.docker.EnsureVolume(ctx, docker.Volume{Name: CacheVolume}); err != nil {
		return err
	}
	compose, err := renderCompose(name, dir, image.Tag, r, m.sync == nil)
	if err != nil {
		return err
	}
	if err := writeFile(dir, ComposeFile, compose); err != nil {
		return err
	}
	m.p.Step("starting the containers of " + name)
	if err := m.docker.ComposeUp(ctx, docker.Project{
		Name: name, File: filepath.Join(dir, ComposeFile), Out: out,
	}); err != nil {
		return err
	}
	if err := m.ensureSync(ctx, t); err != nil {
		return err
	}

	b := bench{m: m, ctx: ctx, container: frappeContainer(name)}
	if err := b.configure(dir, r); err != nil {
		return err
	}
	for _, s := range r.Sites {
		if err := b.ensureSite(t, s.Host); err != nil {
			return err
		}
		if err := b.ensureSiteApps(s.Host, r); err != nil {
			return err
		}
	}
	m.p.Step("routing the sites")
	if err := m.route(ctx); err != nil {
		return err
	}
	if err := m.waitForSites(ctx, t, r); err != nil {
		return err
	}
	if err := b.warnUnlisted(t, r); err != nil {
		return err
	}
	m.p.OK("environment " + name + " is built")
	return nil
}

// siteWait is how long the build waits for its sites to answer. A stopped
// environment's web server answers within about 15 s, and the 60 s target is
// for a start that succeeds; a crash loop ends the wait at once.
const siteWait = 2 * time.Minute

// waitForSites waits until each site answers on the frappe container. The
// router already serves the sites, so only Frappe's web server can lag, and
// asking it directly also covers a hostname that the router sends to another
// environment, whose recipe also lists it.
func (m *Manager) waitForSites(ctx context.Context, t target, r Recipe) error {
	container := frappeContainer(t.name)
	// Exits from before this build would read as a crash loop that it fixed.
	if err := m.docker.Exec(ctx, docker.Exec{Container: container, Cmd: []string{"rm", "-f", frappe.HonchoExits}}); err != nil {
		return err
	}
	if len(r.Sites) > 0 {
		m.p.Step("waiting for the sites to answer")
	}
	deadline := time.Now().Add(siteWait)
	for _, s := range r.Sites {
		err := m.docker.Exec(ctx, docker.Exec{
			Container: container,
			Cmd:       frappe.WaitForSite(s.Host, max(1, int(time.Until(deadline).Seconds()))),
		})
		if err == nil {
			continue
		}
		// Only the probe's own give-up code says the site did not answer; any other
		// failure is what it says.
		var exited *docker.ExitError
		if !errors.As(err, &exited) || exited.Status != frappe.GaveUp {
			return err
		}
		crash, err := m.crashLoop(ctx, t)
		switch {
		case err != nil:
			return err
		case crash != nil:
			return crash
		}
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("the site %s did not answer within %d minutes", s.Host, int(siteWait.Minutes())),
			"see why with "+t.command("logs frappe"))
	}
	crash, err := m.crashLoop(ctx, t)
	if crash != nil {
		m.p.Warn(crash.Error())
	}
	return err
}

// bench runs the bench steps in one environment's frappe container.
type bench struct {
	m         *Manager
	ctx       context.Context
	container string
}

// configure runs bench init unless the Procfile, written last, shows it done,
// rewrites the settings tamp owns, installs the apps, and rewrites the
// Procfile. Processes that ran before an app was installed restart.
func (b bench) configure(dir string, r Recipe) error {
	_, err := b.read(frappe.Procfile)
	running := err == nil
	if errors.Is(err, docker.ErrNotExist) {
		b.m.p.Step("running bench init")
		err = b.run(frappe.Init(r.Frappe))
	}
	if err != nil {
		return err
	}

	existing, err := b.read(frappe.CommonSiteConfig)
	if err != nil && !errors.Is(err, docker.ErrNotExist) {
		return err
	}
	settings, err := frappe.MergeSettings(existing,
		frappe.Settings(dbService, redisCacheService, redisQueueService))
	if err != nil {
		return exitcode.New(exitcode.CodeFailed, err.Error(),
			"fix the JSON in "+frappe.CommonSiteConfig+" with `docker exec -it "+b.container+" bash`")
	}
	if err := b.m.docker.WriteFile(b.ctx, b.container, frappe.CommonSiteConfig, settings); err != nil {
		return err
	}
	changed, paused, err := b.installApps(dir, r, running)
	if err != nil {
		return err
	}
	if err := b.m.docker.WriteFile(b.ctx, b.container, frappe.Procfile, []byte(frappe.ProcfileText)); err != nil {
		return err
	}
	// A build that failed while paused leaves the pause behind.
	if !paused {
		if paused, err = b.exists(frappe.Paused); err != nil {
			return err
		}
	}
	switch {
	case paused:
		return b.run([]string{"rm", "-f", frappe.Paused})
	case running && changed:
		return b.run(frappe.RestartProcesses())
	}
	return nil
}

// ensureSite creates the site or finishes tamp's steps on it. A site that bench
// new-site never finished stops the build: tamp neither skips nor recreates it.
func (b bench) ensureSite(t target, host string) error {
	_, err := b.read(frappe.SiteConfig(host))
	switch {
	case errors.Is(err, docker.ErrNotExist):
		b.m.p.Step("creating the site " + host)
		if err := b.run(frappe.NewSite(host)); err != nil {
			return err
		}
		if err := b.m.docker.WriteFile(b.ctx, b.container, frappe.SiteInstalled(host), nil); err != nil {
			return err
		}
	case err != nil:
		return err
	default:
		if _, err := b.read(frappe.SiteInstalled(host)); errors.Is(err, docker.ErrNotExist) {
			// The remove takes the site out of the recipe, and the add puts it
			// back and builds it.
			return fullFix{exitcode.New(exitcode.CodeFailed,
				fmt.Sprintf("the site %s was only partly created", host),
				"run "+t.command("site remove "+host+" --yes")+", then "+t.command("site add "+host))}
		} else if err != nil {
			return err
		}
	}

	config, err := b.read(frappe.SiteConfig(host))
	if err != nil {
		return err
	}
	on, err := frappe.DeveloperModeOn(config)
	if err != nil {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("%s is not valid JSON: %v", frappe.SiteConfig(host), err),
			"fix the JSON with `docker exec -it "+b.container+" bash`")
	}
	if on {
		return nil
	}
	return b.run(frappe.DeveloperMode(host))
}

func (b bench) read(path string) ([]byte, error) {
	return b.m.docker.ReadFile(b.ctx, b.container, path)
}

func (b bench) run(cmd []string) error {
	out := b.m.p.Progress()
	return b.m.docker.Exec(b.ctx, docker.Exec{Container: b.container, Cmd: cmd, Stdout: out, Stderr: out})
}

// warnUnlisted names each site the bench holds that the recipe does not
// list. start deletes nothing, so the user decides.
func (b bench) warnUnlisted(t target, r Recipe) error {
	hosts, err := b.sites()
	if err != nil {
		return err
	}
	for _, host := range hosts {
		if slices.Contains(r.hosts(), host) {
			continue
		}
		b.m.p.Warn(fmt.Sprintf("the bench holds the site %s, which the recipe does not list: drop it with %s",
			host, t.command("site remove "+host+" --yes")))
	}
	return nil
}
