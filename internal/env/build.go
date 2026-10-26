package env

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zhide915/tamp/internal/docker"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/frappe"
)

// Start builds the environment in the working directory from its recipe.
func (m *Manager) Start() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	r, err := readRecipe(dir)
	if err != nil {
		return err
	}
	return m.build(dir, r, "")
}

// build makes the environment at dir match its recipe. Nothing rolls back, so
// every fix ends in `tamp start`, in where unless where is empty.
func (m *Manager) build(dir string, r Recipe, where string) error {
	err := m.steps(context.Background(), dir, r)
	var e *exitcode.Error
	if errors.As(err, &e) {
		start := "run `tamp start`"
		if where != "" {
			start += " in " + where
		}
		fix := start
		if e.Fix != "" {
			fix = e.Fix + ", then " + start
		}
		return exitcode.New(e.Code, e.Msg, fix)
	}
	return err
}

func (m *Manager) steps(ctx context.Context, dir string, r Recipe) error {
	name := filepath.Base(dir)
	out := m.p.Progress()

	if err := m.docker.Ping(ctx); err != nil {
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

	if err := cloneApp(ctx, dir, "frappe", FrappeURL, r.Frappe, out); err != nil {
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
	if err := b.configure(r.Frappe); err != nil {
		return err
	}
	for _, s := range r.Sites {
		if err := b.ensureSite(s.Host); err != nil {
			return err
		}
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
// then rewrites the settings tamp owns and the Procfile.
func (b bench) configure(branch string) error {
	_, err := b.read(frappe.Procfile)
	if errors.Is(err, docker.ErrNotExist) {
		b.m.p.Step("running bench init")
		err = b.run(frappe.Init(branch))
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
	return b.m.docker.WriteFile(b.ctx, b.container, frappe.Procfile, []byte(frappe.ProcfileText))
}

// ensureSite creates the site or finishes tamp's steps on it. A site that bench
// new-site never finished stops the build: tamp neither skips nor recreates it.
func (b bench) ensureSite(host string) error {
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
				fmt.Sprintf("run `tamp site remove %s --yes`", host))
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
