package env

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"

	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/frappe"
	"github.com/zhide915/tamp/internal/router"
	"github.com/zhide915/tamp/internal/syncer"
	"github.com/zhide915/tamp/internal/ui"
)

// CreateLogFile survives a failed create in .tamp/, showing how far tamp got
// and what the engine said.
const CreateLogFile = "create.log"

// buildSteps is build's numbered step count, before the per-app steps.
const buildSteps = 8

// createSteps adds the three steps a create runs before build.
const createSteps = 3 + buildSteps

// CreateRequest carries `tamp create`'s flags, unvalidated.
type CreateRequest struct {
	Name string
	// Parent is where <name>/ goes; empty means the cwd — there is no
	// mandatory root.
	Parent string
	Frappe string
	// Apps is a comma-separated list of app specs.
	Apps string
	Sync string
	// NoCache forces a fresh bench init, leaving the stored template alone.
	NoCache bool
}

// Create provisions a new environment and brings it up. On failure everything
// outside the directory is undone; the directory keeps tamp.toml and
// create.log — tamp never destroys a directory the user may have touched.
func (m *Manager) Create(ctx context.Context, req CreateRequest) error {
	// A misspelled machine setting must fail here too: the user would
	// otherwise believe it took effect.
	template, err := m.cachePolicy(!req.NoCache)
	if err != nil {
		return err
	}
	plan, err := m.newPlan(req.Name, req.Frappe, req.Apps, req.Sync, template)
	if err != nil {
		return err
	}
	dir, err := m.createDir(req, plan.Name)
	if err != nil {
		return err
	}

	if err := m.raise(ctx, dir, plan); err != nil {
		m.Out.Note("delete " + dir + " to try again")
		return err
	}
	return nil
}

// plan is a new environment with every flag validated — what create and init
// share.
type plan struct {
	Name      Name
	Version   FrappeVersion
	Toolchain Toolchain
	Apps      []App
	Sync      syncer.Mode
	Template  templatePolicy
}

// newPlan validates everything up front: a misspelled flag must fail before
// tamp has claimed a name, made a directory or pulled an image.
func (m *Manager) newPlan(name, version, apps, sync string, template templatePolicy) (plan, error) {
	parsedName, err := ParseName(name)
	if err != nil {
		return plan{}, err
	}
	parsedVersion, toolchain, err := ParseFrappeVersion(version)
	if err != nil {
		return plan{}, err
	}
	parsedApps, err := ParseApps(apps)
	if err != nil {
		return plan{}, err
	}
	parsedSync, err := syncer.ParseMode(sync)
	if err != nil {
		return plan{}, err
	}
	return plan{
		Name:      parsedName,
		Version:   parsedVersion,
		Toolchain: toolchain,
		Apps:      parsedApps,
		Sync:      parsedSync,
		Template:  template,
	}, nil
}

// raise writes a new environment into dir and brings it up — the whole of
// create, and of init in an empty directory. A failed step undoes everything
// outside dir, volumes included, which is safe only because this environment
// never held data.
func (m *Manager) raise(ctx context.Context, dir string, p plan) error {
	// Warnings, not refusals: where the environment lives is the user's call.
	for _, warning := range syncer.PathWarnings(dir) {
		m.Out.Warn(warning)
	}

	// One numbered step per app: a fetch is minutes of cloning and installing,
	// and would look stuck under a single line.
	log := &createLog{out: m.Out, steps: m.Out.Steps(createSteps + len(p.Apps))}
	defer log.save(dir)

	log.step("checking Docker")
	if _, err := m.Engine.Ping(ctx); err != nil {
		return err
	}

	log.step(fmt.Sprintf("resolving the Frappe %s toolchain", p.Version))
	log.note(fmt.Sprintf("python %s · node %s · mariadb %s",
		p.Toolchain.Python, p.Toolchain.Node, p.Toolchain.MariaDB))

	// Settled before anything is written, because it decides what is written:
	// a bind mount is a line in the compose file.
	sync := m.syncMode(ctx, p.Sync)

	log.step("writing the environment")
	e, err := m.provision(dir, p)
	if err != nil {
		return err
	}
	if err := m.requireFreshVolumes(ctx, e); err != nil {
		m.unregister(p.Name)
		return err
	}
	if err := m.writeEnvironment(e, sync); err != nil {
		m.unregister(p.Name)
		return err
	}

	status, err := m.build(ctx, e, sync, p.Template, log)
	if err != nil {
		m.rollback(ctx, e, engine.RemoveVolumes, log)
		return err
	}

	m.Out.OK(fmt.Sprintf("%s ready — no sites yet", p.Name))
	m.announceRoutes(e, status)
	m.announceDBPassword(e)
	m.Out.Hint("next: tamp site new <host>")
	return nil
}

// requireFreshVolumes refuses to raise a new environment on leftover data
// volumes: attaching them silently would hand it a database initialized under
// a root password it does not hold, and a failed build's rollback would then
// destroy data an earlier `tamp rm` deliberately kept.
func (m *Manager) requireFreshVolumes(ctx context.Context, e *Environment) error {
	held, err := m.Engine.HasVolumes(ctx, e.Resources.Project())
	if err != nil {
		return err
	}
	if !held {
		return nil
	}
	return exitcode.New(exitcode.CodeFailed,
		fmt.Sprintf("volumes from an earlier environment named %q still exist for this directory", e.Name()),
		fmt.Sprintf("adopt them by restoring its %s and apps tree and running 'tamp init' — or delete them first: docker volume rm $(docker volume ls -q --filter label=com.docker.compose.project=%s)",
			ConfigFile, e.Resources.Project()))
}

// build turns a written-out environment into a running bench. It runs
// entirely inside containers, and its one caller's rollback undoes every
// step: an environment with a toolchain but no bench is failed, not
// half-created.
func (m *Manager) build(ctx context.Context, e *Environment, sync syncer.Effective, template templatePolicy, log *createLog) (router.Status, error) {
	// Read before the sync session runs: a Mutagen session below mirrors the
	// container's tree out and would make this true for fresh creates too.
	adopted := hasSource(e.Dir)

	log.step("starting containers")
	if err := m.ensureSharedVolumes(ctx); err != nil {
		return router.Status{}, err
	}
	if err := m.Engine.ComposeUp(ctx, e.project(), log.stream()); err != nil {
		return router.Status{}, err
	}

	bench := e.bench(m.Engine, log.stream())

	// Before anything slow: an unreachable app source must cost seconds, not
	// the minutes a bench build takes.
	log.step("checking that every app source answers")
	bridge, err := m.preflightApps(ctx, bench, e.Config.Frappe.Apps, log)
	if err != nil {
		return router.Status{}, err
	}

	// Slow once per machine, near-instant after: the toolchain and package
	// caches live in volumes shared by every environment.
	log.step(fmt.Sprintf("provisioning python %s and node %s", bench.Python, bench.Node))
	if err := bench.Provision(ctx); err != nil {
		return router.Status{}, err
	}

	log.step("initializing the bench")
	initialized, err := m.materialize(ctx, e, bench, template, log)
	if err != nil {
		return router.Status{}, err
	}

	// Before the apps: the session mirrors the host's tree into the bench
	// first, so a re-adoption finds its apps present and skips re-cloning.
	log.step("starting the sync session")
	if err := m.startSync(ctx, e, sync, log.stream()); err != nil {
		return router.Status{}, err
	}

	// Fresh volumes, Mutagen, adopted source: bench initialized empty, the
	// session mirrored the host's apps in, and bench knows nothing of them —
	// without this they stay off apps.txt with requirements uninstalled.
	if initialized && adopted && sync == syncer.UseMutagen {
		log.note("registering the apps the sync session brought back")
		if err := bench.Rebuild(ctx); err != nil {
			return router.Status{}, err
		}
	}

	if err := m.fetchApps(ctx, e, bench, bridge, log); err != nil {
		return router.Status{}, err
	}
	if err := m.handGitToHost(ctx, e, sync); err != nil {
		return router.Status{}, err
	}
	if sync == syncer.UseMutagen && runtime.GOOS == "windows" {
		log.note("git in " + syncer.AppsDir(e.Dir) + " ignores file modes and line endings — this host stores neither the way Linux wrote them")
	}

	log.step("configuring the bench")
	if err := bench.Configure(ctx); err != nil {
		return router.Status{}, err
	}

	// The container decides at boot whether it has a bench to run; now it
	// does, so it is asked again.
	log.step("starting the bench processes")
	if err := m.Engine.ComposeRestart(ctx, e.project(), FrappeService, log.stream()); err != nil {
		return router.Status{}, err
	}
	if err := bench.WaitForWeb(ctx); err != nil {
		return router.Status{}, err
	}

	// Last: the router can only join a network that now exists.
	log.step("routing " + router.MailHost(string(e.Name())))
	return m.applyRoutes(ctx, log.stream())
}

// fetchApps clones each app onto the bench and onto no site — installation is
// per site, and `tamp site new --apps` is where the user decides.
func (m *Manager) fetchApps(ctx context.Context, e *Environment, bench *frappe.Bench, bridge *bridge, log *createLog) error {
	onBench, err := bench.Apps(ctx)
	if err != nil {
		return err
	}

	renamed := false
	for i := range e.Config.Frappe.Apps {
		app := &e.Config.Frappe.Apps[i]
		// A re-adopted bench already holds its apps, and bench get-app fails
		// on one rather than doing nothing.
		if slices.Contains(onBench, app.Name) {
			log.step(app.Name + " is already on the bench")
			continue
		}

		log.step("fetching " + app.Name)
		if app.Branch == "" {
			// Most apps default to develop, which breaks a pinned bench; tamp
			// warns rather than guessing, since many apps have no matching
			// release branch.
			pin := app.Name
			if app.Source != defaultAppOwner+app.Name {
				// A URL-sourced app must be pinned by URL — the bare name
				// would resolve to the frappe organisation.
				pin = app.Source
			}
			m.Out.Warn(fmt.Sprintf("fetching default branch of %s — pin with %s:%s if you meant a release branch",
				app.Name, pin, e.Config.Frappe.Version))
		}
		if err := m.fetchApp(ctx, bench, bridge, app); err != nil {
			return err
		}

		// The app a repository declares can differ from the repository's name
		// (frappe/health clones as healthcare), and the bench is the
		// authority. Rename the record only when exactly one new app
		// appeared — with more, tamp cannot tell which is which.
		now, err := bench.Apps(ctx)
		if err != nil {
			return err
		}
		if fresh := newApps(now, onBench); !slices.Contains(now, app.Name) && len(fresh) == 1 {
			log.note(app.Name + " calls itself " + fresh[0])
			app.Name = fresh[0]
			renamed = true
		}
		onBench = now
	}

	if renamed {
		return e.Config.Save(ConfigPath(e.Dir))
	}
	return nil
}

// fetchApp runs one bench get-app, injecting the bridge's credential into
// that exec alone and completing the credential protocol on the outcome.
func (m *Manager) fetchApp(ctx context.Context, bench *frappe.Bench, bridge *bridge, app *App) error {
	env := bridge.envFor(app.Source)
	if env == nil {
		return bench.GetApp(ctx, frappe.GetAppRequest{Source: app.Source, Branch: app.Branch})
	}

	// The tee: an authenticated fetch that fails must be classified, so the
	// host's helper can be told to drop a credential that stopped working.
	var said bytes.Buffer
	authed := *bench
	authed.Out = io.MultiWriter(bench.Out, &said)
	err := authed.GetApp(ctx, frappe.GetAppRequest{Source: app.Source, Branch: app.Branch, Env: env})
	if err == nil {
		bridge.approve(ctx, app.Source)
		return nil
	}
	if authShaped(said.String()) {
		bridge.reject(ctx, app.Source)
		_, host, _, hostErr := sourceParts(app.Source)
		if hostErr == nil {
			return refusedCredential(app.Source, host)
		}
	}
	return err
}

// newApps names the apps that appeared between two bench listings.
func newApps(now, before []string) []string {
	var fresh []string
	for _, name := range now {
		if !slices.Contains(before, name) {
			fresh = append(fresh, name)
		}
	}
	return fresh
}

// handGitToHost repairs the apps' repositories for the host's git, only where
// the host cannot store what Linux wrote: Windows under Mutagen. Elsewhere
// modes are real, and turning the check off would hide the user's own
// changes.
func (m *Manager) handGitToHost(ctx context.Context, e *Environment, sync syncer.Effective) error {
	if sync != syncer.UseMutagen || runtime.GOOS != "windows" {
		return nil
	}
	return e.bench(m.Engine, m.Out.Stream()).HandGitToHost(ctx)
}

// announceDBPassword prints the credential once, here only: it is on disk,
// and reprinting on every start would scatter it through scrollback.
func (m *Manager) announceDBPassword(e *Environment) {
	password, err := ReadDBRootPassword(e.Dir)
	if err != nil {
		m.Out.Warn(err.Error())
		return
	}
	m.Out.Note("database root password: " + password)
	m.Out.Note("kept in " + DBRootPasswordPath(e.Dir) + " — tamp prints it this once")
}

// createDir settles where the environment goes, refusing to build on anything
// already there.
func (m *Manager) createDir(req CreateRequest, name Name) (string, error) {
	parent := req.Parent
	if parent == "" {
		parent = m.Cwd
	}
	dir, err := filepath.Abs(filepath.Join(parent, string(name)))
	if err != nil {
		return "", exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot resolve %q to an absolute path: %v", parent, err),
			"pass --dir a directory that exists")
	}

	if _, err := os.Stat(dir); err == nil {
		return "", exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("%s already exists", dir),
			"pick another name, or create the environment somewhere else with --dir")
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot look at %s: %v", dir, err),
			"check the permissions on the parent directory")
	}
	return dir, nil
}

// provision claims the name and builds the environment in memory; rollback
// can undo all of it.
func (m *Manager) provision(dir string, p plan) (*Environment, error) {
	name := p.Name
	res, err := NewResources(name, dir)
	if err != nil {
		return nil, err
	}

	port, err := Claim(m.Home, name, dir, res.Hash)
	if err != nil {
		return nil, err
	}

	cfg := NewConfig(name, p.Version, p.Apps, p.Toolchain, port)
	cfg.Sync.Mode = p.Sync
	return &Environment{Dir: dir, Config: cfg, Resources: res}, nil
}

func (m *Manager) writeEnvironment(e *Environment, sync syncer.Effective) error {
	if err := os.MkdirAll(StateDir(e.Dir), 0o755); err != nil {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot create %s: %v", StateDir(e.Dir), err),
			"check the permissions on the parent directory")
	}
	if err := e.Config.Save(ConfigPath(e.Dir)); err != nil {
		return err
	}
	if err := WriteGitignore(e.Dir); err != nil {
		return err
	}
	return m.regenerate(e, sync)
}

// rollback undoes a failed provisioning outside the environment directory.
// removal is the caller's call: a new environment's volumes go with it, while
// a re-adopted one stands on volumes from a previous life. Its own failures
// are warnings — the original error is the one the user needs.
func (m *Manager) rollback(ctx context.Context, e *Environment, removal engine.Removal, log *createLog) {
	m.Out.Warn(fmt.Sprintf("create failed — rolling back %s", e.Name()))

	// The session's far end is one of the containers.
	m.terminateSync(ctx, e)

	// Docker refuses to remove a network the router is still joined to.
	if err := m.router().Detach(ctx, e.Resources.Network()); err != nil {
		m.Out.Warn(fmt.Sprintf("could not detach the router: %v", err))
	}
	if err := m.Engine.ComposeDown(ctx, e.project(), removal, log.stream()); err != nil {
		m.Out.Warn(fmt.Sprintf("could not remove the containers: %v", err))
		m.Out.Warn(fmt.Sprintf("remove them by hand with: docker compose -p %s down", e.Resources.Project()))
	}
	m.unregister(e.Name())
	// Nothing may still route to a deregistered environment.
	if _, err := m.refreshRoutes(ctx); err != nil {
		m.Out.Warn(fmt.Sprintf("could not update the router's routes: %v", err))
	}

	m.Out.Note(fmt.Sprintf("%s was left in place, with %s and %s",
		e.Dir, ConfigFile, filepath.Join(StateDirName, CreateLogFile)))
}

func (m *Manager) unregister(name Name) {
	if err := Release(m.Home, name); err != nil {
		m.Out.Warn(fmt.Sprintf("could not remove %q from the registry: %v", name, err))
	}
}

// createLog narrates to the terminal and into a buffer that becomes
// create.log — a buffer because the directory may not exist yet when the
// first step prints.
type createLog struct {
	buf   bytes.Buffer
	out   *ui.Printer
	steps *ui.Stepper
}

func (l *createLog) step(msg string) {
	n, total := l.steps.Step(msg)
	fmt.Fprintf(&l.buf, "[%d/%d] %s\n", n, total, msg)
}

func (l *createLog) note(msg string) {
	l.out.Note(msg)
	fmt.Fprintln(&l.buf, msg)
}

// stream tees the engine's output to the terminal and the log.
func (l *createLog) stream() io.Writer {
	return io.MultiWriter(l.out.Stream(), &l.buf)
}

// save never creates the directory: a create rejected before making anything
// must leave nothing behind. Its failures are silent — a bigger error is
// already being reported.
func (l *createLog) save(dir string) {
	if _, err := os.Stat(dir); err != nil {
		return
	}
	if err := os.MkdirAll(StateDir(dir), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(StateDir(dir), CreateLogFile), l.buf.Bytes(), 0o644)
}
