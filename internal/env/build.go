package env

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"

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
// back, so every build failure ends in `tamp start`.
func (t target) resume(err error) error {
	var e *exitcode.Error
	if !errors.As(err, &e) {
		return err
	}
	start := "run " + t.command("start")
	fix := start
	if e.Fix != "" {
		fix = e.Fix + ", then " + start
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
		fix := fmt.Sprintf("check out %s in apps/frappe, then `tamp remove --yes` and `tamp start`", r.Frappe)
		// After the remove, -e finds nothing: only the directory names the
		// environment.
		if dir := t.elsewhere(); dir != "" {
			fix = fmt.Sprintf("in %s, check out %s in apps/frappe, then run `tamp remove --yes` and `tamp start`", dir, r.Frappe)
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

	if err := cloneApp(ctx, dir, "frappe", FrappeURL, r.Frappe, out); err != nil {
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
	compose, err := renderCompose(name, dir, image.Tag, r)
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
	if err := b.warnUnlisted(t, r); err != nil {
		return err
	}
	m.p.OK("environment " + name + " is built")
	return nil
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
			return exitcode.New(exitcode.CodeFailed,
				fmt.Sprintf("the site %s was only partly created", host),
				"run "+t.command("site remove "+host+" --yes"))
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
