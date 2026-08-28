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

// CreateLogFile records what happened during a create. It is left behind by a
// create that failed, next to the tamp.toml, so the user can see how far
// tamp got and what the engine said.
const CreateLogFile = "create.log"

// buildSteps is how many numbered steps turning a written-out environment into
// a running bench prints, before its apps are counted in — one step each. It
// grows as tamp learns to do more at create time — a bench, a toolchain, a
// sync session, a router.
const buildSteps = 7

// createSteps is buildSteps plus the three a create does first.
const createSteps = 3 + buildSteps

// CreateRequest is what `tamp create` was asked for.
type CreateRequest struct {
	// Name is the environment name, still unvalidated.
	Name string
	// Parent is the directory <name>/ is created inside. Empty means the
	// directory tamp was run in (there is no mandatory root).
	Parent string
	// Frappe is the --frappe value, still unvalidated.
	Frappe string
	// Apps is the --apps value, still unvalidated: a comma-separated list of
	// app specs, each a name or a git URL, optionally with a branch after it.
	Apps string
	// Sync is the --sync value, still unvalidated.
	Sync string
}

// Create provisions a new environment and brings its containers up.
//
// Everything it makes outside the environment directory — the registry entry,
// the containers, the volumes — is undone if any step fails; the directory
// itself is left with tamp.toml and create.log, because the one thing tamp
// must never destroy is a directory the user might have put something in.
func (m *Manager) Create(ctx context.Context, req CreateRequest) error {
	plan, err := m.newPlan(req.Name, req.Frappe, req.Apps, req.Sync)
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

// plan is a new environment as its flags describe it, with every one of them
// already validated. It is what create and init have in common: two ways of
// saying where the directory is, and one way of filling it.
type plan struct {
	Name      Name
	Version   FrappeVersion
	Toolchain Toolchain
	Apps      []App
	Sync      syncer.Mode
}

// newPlan validates the flags create and init share.
//
// All of them, before anything is made: a misspelled Frappe version has to
// fail before tamp has claimed a name, made a directory or pulled an image.
func (m *Manager) newPlan(name, version, apps, sync string) (plan, error) {
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
	}, nil
}

// raise writes a new environment into a directory and brings it up.
//
// It is the whole of create, and the whole of init in a directory that holds
// nothing yet. A failure at any step undoes everything tamp made outside the
// directory — including the volumes, which is safe here and only here: this
// environment has never held anything.
func (m *Manager) raise(ctx context.Context, dir string, p plan) error {
	// Warnings, not refusals: where the environment goes is the user's call,
	// and both of these describe a setup that works until it does not.
	for _, warning := range syncer.PathWarnings(dir) {
		m.Out.Warn(warning)
	}

	// One numbered step per app: fetching one is minutes of cloning and
	// pip-installing, and a create that spent them under a single line would
	// look stuck.
	log := &createLog{out: m.Out, total: createSteps + len(p.Apps)}
	defer log.save(dir)

	log.step("checking Docker")
	if _, err := m.Engine.Ping(ctx); err != nil {
		return err
	}

	log.step(fmt.Sprintf("resolving the Frappe %s toolchain", p.Version))
	log.note(fmt.Sprintf("python %s · node %s · mariadb %s",
		p.Toolchain.Python, p.Toolchain.Node, p.Toolchain.MariaDB))

	// Settled before the environment is written, because it decides what is
	// written: a bind mount is a line in the compose file, and a machine that
	// cannot get Mutagen falls back to one.
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

	status, err := m.build(ctx, e, sync, log)
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

// requireFreshVolumes refuses to raise a new environment on top of data
// volumes an earlier one left behind. Reattaching them silently would hand
// this environment a database it knows nothing about — initialized under a
// root password it does not hold — and the rollback a failed build performs
// would then destroy data the earlier `tamp rm` deliberately kept.
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

// build turns a written-out environment into a running bench.
//
// Everything from here on happens inside containers, and every step of it is
// undone by the rollback its one caller performs: an environment that got a
// toolchain but no bench is not half-created, it is failed.
func (m *Manager) build(ctx context.Context, e *Environment, sync syncer.Effective, log *createLog) (router.Status, error) {
	// Whether the host held source before anything ran: the mark of an
	// adoption, read now because a Mutagen session below will mirror the
	// container's tree out and make it true for fresh creates too.
	adopted := hasSource(e.Dir)

	log.step("starting containers")
	if err := m.ensureSharedVolumes(ctx); err != nil {
		return router.Status{}, err
	}
	if err := m.Engine.ComposeUp(ctx, e.project(), log.stream()); err != nil {
		return router.Status{}, err
	}

	bench := e.bench(m.Engine, log.stream())

	// Slow once per machine, near-instant every time after: the Python, the
	// Node and the package caches all live in volumes shared by every
	// environment, so this is where a second create stops being expensive.
	log.step(fmt.Sprintf("provisioning python %s and node %s", bench.Python, bench.Node))
	if err := bench.Provision(ctx); err != nil {
		return router.Status{}, err
	}

	log.step("initializing the bench")
	initialized, err := bench.Materialize(ctx)
	if err != nil {
		return router.Status{}, err
	}

	// Before the apps, not after. A session started here mirrors whatever the
	// host already holds into the bench first, so an environment being
	// re-adopted round its own source finds those apps present and skips
	// re-cloning them — and a fresh create syncs out an app as it arrives.
	log.step("starting the sync session")
	if err := m.startSync(ctx, e, sync, log.stream()); err != nil {
		return router.Status{}, err
	}

	// The one path where bench init and the host's source both exist: fresh
	// volumes under Mutagen. bench initialized empty, the session has now
	// mirrored the host's apps in, and bench knows nothing about them —
	// without this they stay off apps.txt, requirements uninstalled.
	if initialized && adopted && sync == syncer.UseMutagen {
		log.note("registering the apps the sync session brought back")
		if err := bench.Rebuild(ctx); err != nil {
			return router.Status{}, err
		}
	}

	if err := m.fetchApps(ctx, e, bench, log); err != nil {
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

	// The container decides at boot whether it has a bench to run. It did not
	// when it first started, and it does now, so it is asked again.
	log.step("starting the bench processes")
	if err := m.Engine.ComposeRestart(ctx, e.project(), FrappeService, log.stream()); err != nil {
		return router.Status{}, err
	}
	if err := bench.WaitForWeb(ctx); err != nil {
		return router.Status{}, err
	}

	// Last, because the router can only join a network that exists, and the
	// environment's network is made by the compose up above.
	log.step("routing " + router.MailHost(string(e.Name())))
	return m.applyRoutes(ctx, log.stream())
}

// fetchApps clones each of the environment's apps onto the bench.
//
// Onto the bench and onto no site: an app is fetched once and installed per
// site, so a create that installed them everywhere would be deciding something
// 'tamp site new --apps' exists to let the user decide.
func (m *Manager) fetchApps(ctx context.Context, e *Environment, bench *frappe.Bench, log *createLog) error {
	onBench, err := bench.Apps(ctx)
	if err != nil {
		return err
	}

	renamed := false
	for i := range e.Config.Frappe.Apps {
		app := &e.Config.Frappe.Apps[i]
		// Re-adopting an environment runs this against a bench that already
		// holds its apps, and bench get-app on one of those fails rather than
		// quietly doing nothing.
		if slices.Contains(onBench, app.Name) {
			log.step(app.Name + " is already on the bench")
			continue
		}

		log.step("fetching " + app.Name)
		if app.Branch == "" {
			// The one predictable way this goes wrong: most Frappe apps
			// default to develop, which does not run on a pinned bench. tamp
			// says so rather than picking a branch, because plenty of apps
			// have no version-15 branch to pick.
			pin := app.Name
			if app.Source != defaultAppOwner+app.Name {
				// The hint has to repeat a URL-sourced app's URL: the bare
				// name would resolve to the frappe organisation instead.
				pin = app.Source
			}
			m.Out.Warn(fmt.Sprintf("fetching default branch of %s — pin with %s:%s if you meant a release branch",
				app.Name, pin, e.Config.Frappe.Version))
		}
		if err := bench.GetApp(ctx, frappe.GetAppRequest{Source: app.Source, Branch: app.Branch}); err != nil {
			return err
		}

		// The recorded name came from the URL, but the app a repository
		// declares can differ from the repository's name — frappe/health
		// clones as healthcare. The bench is the authority: whatever just
		// appeared on it is what this app is called everywhere tamp needs
		// the name again, the already-on-the-bench check above included.
		// Renaming is safe only when the fetch produced exactly one new app;
		// more than one and tamp cannot tell which is which, so the record
		// stands and the bench remains the authority at install time.
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

// newApps names the apps that appeared between two listings of the bench.
func newApps(now, before []string) []string {
	var fresh []string
	for _, name := range now {
		if !slices.Contains(before, name) {
			fresh = append(fresh, name)
		}
	}
	return fresh
}

// handGitToHost makes the apps' repositories usable by the host's git.
//
// Only where the host's filesystem cannot describe what Linux wrote. On Linux
// the source is bind-mounted and every mode is real; on macOS the executable
// bit stores fine and turning the check off would hide changes the user made
// themselves. Windows can do neither, and the repositories are in that state
// because tamp cloned them in a container — so tamp is the one to settle it.
func (m *Manager) handGitToHost(ctx context.Context, e *Environment, sync syncer.Effective) error {
	if sync != syncer.UseMutagen || runtime.GOOS != "windows" {
		return nil
	}
	return e.bench(m.Engine, m.Out.Stream()).HandGitToHost(ctx)
}

// announceDBPassword prints the environment's generated MariaDB credential.
//
// Once, here, and nowhere else: it is on disk in the environment's own secrets
// directory, and reprinting it on every start would scatter it through
// terminal scrollback nobody asked to keep it in.
func (m *Manager) announceDBPassword(e *Environment) {
	password, err := ReadDBRootPassword(e.Dir)
	if err != nil {
		m.Out.Warn(err.Error())
		return
	}
	m.Out.Note("database root password: " + password)
	m.Out.Note("kept in " + DBRootPasswordPath(e.Dir) + " — tamp prints it this once")
}

// createDir settles where the environment goes and refuses to build on top of
// anything that is already there.
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

// provision claims the name, writes the environment's files, and returns it
// ready to start. Everything here is undoable by rollback.
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

// writeEnvironment lays down the directory and everything in it.
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
	if err := EnsureDBRootPassword(e.Dir); err != nil {
		return err
	}
	if err := ensureAppsDir(e.Dir, sync); err != nil {
		return err
	}
	return e.Generate(sync)
}

// rollback undoes what a failed provisioning put outside the environment
// directory.
//
// The removal is the caller's to decide, and it is the difference between a
// tidy failure and a disaster: a new environment's volumes have never held
// anything and go with it, while one being re-adopted is standing on volumes
// that survived a previous life.
//
// Its own failures are reported and then dropped: the user is about to be
// told why the operation failed, and burying that under "and the cleanup
// failed too" would replace the actionable error with a less useful one.
func (m *Manager) rollback(ctx context.Context, e *Environment, removal engine.Removal, log *createLog) {
	m.Out.Warn(fmt.Sprintf("create failed — rolling back %s", e.Name()))

	// Before the containers, because the far end of the session is one of them.
	m.terminateSync(ctx, e)

	// The router joins the environment's network at the last step of a create,
	// and Docker refuses to remove a network that still has something on it.
	if err := m.router().Detach(ctx, e.Resources.Network()); err != nil {
		m.Out.Warn(fmt.Sprintf("could not detach the router: %v", err))
	}
	if err := m.Engine.ComposeDown(ctx, e.project(), removal, log.stream()); err != nil {
		m.Out.Warn(fmt.Sprintf("could not remove the containers: %v", err))
		m.Out.Warn(fmt.Sprintf("remove them by hand with: docker compose -p %s down", e.Resources.Project()))
	}
	m.unregister(e.Name())
	// The registry no longer names this environment, so nothing may still
	// route to it.
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

// createLog narrates a create to the terminal and, at the same time, into the
// buffer that becomes create.log.
//
// It is a buffer rather than an open file because the environment directory
// does not exist yet when the first step is printed, and a create that fails
// before the directory exists has nowhere to write anyway.
type createLog struct {
	buf   bytes.Buffer
	out   *ui.Printer
	n     int
	total int
}

func (l *createLog) step(msg string) {
	l.n++
	l.out.Step(l.n, l.total, msg)
	fmt.Fprintf(&l.buf, "[%d/%d] %s\n", l.n, l.total, msg)
}

func (l *createLog) note(msg string) {
	l.out.Note(msg)
	fmt.Fprintln(&l.buf, msg)
}

// stream is where the engine's own output goes: to the terminal as it happens,
// and into the log so a failed create can be read afterwards.
func (l *createLog) stream() io.Writer {
	return io.MultiWriter(l.out.Stream(), &l.buf)
}

// save writes the log into an environment directory that already exists.
//
// It never creates that directory: a create rejected before it got that far —
// a name already registered, an unreachable engine — must leave nothing behind
// at all, and a log of a create that made nothing is not worth a directory.
//
// A failure here is silent on purpose: it happens while tamp is already
// reporting something the user cares about more.
func (l *createLog) save(dir string) {
	if _, err := os.Stat(dir); err != nil {
		return
	}
	if err := os.MkdirAll(StateDir(dir), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(StateDir(dir), CreateLogFile), l.buf.Bytes(), 0o644)
}
