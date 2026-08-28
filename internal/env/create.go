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

// createSteps is how many numbered steps a create prints before the apps are
// counted in — one step each. It grows as tamp learns to do more at create
// time — a bench, a toolchain, a sync session, a router.
const createSteps = 10

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
	name, err := ParseName(req.Name)
	if err != nil {
		return err
	}
	version, toolchain, err := ParseFrappeVersion(req.Frappe)
	if err != nil {
		return err
	}
	apps, err := ParseApps(req.Apps)
	if err != nil {
		return err
	}
	syncMode, err := syncer.ParseMode(req.Sync)
	if err != nil {
		return err
	}
	dir, err := m.createDir(req, name)
	if err != nil {
		return err
	}
	// Warnings, not refusals: where the environment goes is the user's call,
	// and both of these describe a setup that works until it does not.
	for _, warning := range syncer.PathWarnings(dir) {
		m.Out.Warn(warning)
	}

	// One numbered step per app: fetching one is minutes of cloning and
	// pip-installing, and a create that spent them under a single line would
	// look stuck.
	log := &createLog{out: m.Out, total: createSteps + len(apps)}
	defer log.save(dir)

	log.step("checking Docker")
	if _, err := m.Engine.Ping(ctx); err != nil {
		return err
	}

	log.step(fmt.Sprintf("resolving the Frappe %s toolchain", version))
	log.note(fmt.Sprintf("python %s · node %s · mariadb %s",
		toolchain.Python, toolchain.Node, toolchain.MariaDB))

	// Settled before the environment is written, because it decides what is
	// written: a bind mount is a line in the compose file, and a machine that
	// cannot get Mutagen falls back to one.
	sync := m.syncMode(ctx, syncMode)

	log.step("writing the environment")
	e, err := m.provision(dir, name, version, apps, toolchain, syncMode)
	if err != nil {
		return err
	}
	if err := m.writeEnvironment(e, sync); err != nil {
		m.unregister(name)
		return err
	}

	status, err := m.build(ctx, e, sync, log)
	if err != nil {
		m.rollback(ctx, e, log)
		return err
	}

	m.Out.OK(fmt.Sprintf("%s ready — no sites yet", name))
	m.announceRoutes(e, status)
	m.announceDBPassword(e)
	m.Out.Hint("next: tamp site new <host>")
	return nil
}

// build turns a written-out environment into a running bench.
//
// Everything from here on happens inside containers, and every step of it is
// undone by the rollback its one caller performs: an environment that got a
// toolchain but no bench is not half-created, it is failed.
func (m *Manager) build(ctx context.Context, e *Environment, sync syncer.Effective, log *createLog) (router.Status, error) {
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
	if err := bench.Init(ctx); err != nil {
		return router.Status{}, err
	}

	if err := m.fetchApps(ctx, e, bench, log); err != nil {
		return router.Status{}, err
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

	// After the bench exists, because what a session mirrors out is the apps
	// tree the steps above put there.
	log.step("starting the sync session")
	if err := m.startSync(ctx, e, sync, log.stream()); err != nil {
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
	for _, app := range e.Config.Frappe.Apps {
		log.step("fetching " + app.Name)
		if app.Branch == "" {
			// The one predictable way this goes wrong: most Frappe apps
			// default to develop, which does not run on a pinned bench. tamp
			// says so rather than picking a branch, because plenty of apps
			// have no version-15 branch to pick.
			m.Out.Warn(fmt.Sprintf("fetching default branch of %s — pin with %s:%s if you meant a release branch",
				app.Name, app.Name, e.Config.Frappe.Version))
		}
		if err := bench.GetApp(ctx, frappe.GetAppRequest{Source: app.Source, Branch: app.Branch}); err != nil {
			return err
		}
	}
	return nil
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
func (m *Manager) provision(dir string, name Name, version FrappeVersion, apps []App, tc Toolchain, sync syncer.Mode) (*Environment, error) {
	res, err := NewResources(name, dir)
	if err != nil {
		return nil, err
	}

	// Claiming the name and allocating the port happen in one pass under the
	// machine lock, and the port is recorded in the entry itself: both
	// facts are then on disk together when the lock is released, so a second
	// create cannot see the name free or reuse the port.
	var port int
	err = UpdateRegistry(m.Home, func(reg Registry) error {
		if existing, taken := reg[string(name)]; taken {
			return exitcode.New(exitcode.CodeFailed,
				fmt.Sprintf("an environment named %q is already registered, at %s", name, existing.Path),
				"pick another name, or remove the old one with 'tamp rm "+string(name)+"'")
		}
		// The name decides a hostname too — the mail UI's — and the router
		// would refuse a configuration holding that address twice.
		mailHost := router.MailHost(string(name))
		if owner, what, clash := hostClaimedBy(reg, "", mailHost); clash {
			return exitcode.New(exitcode.CodeFailed,
				fmt.Sprintf("an environment named %q would take the hostname %s, which is already %s of %q",
					name, mailHost, what, owner),
				"pick another name, or remove that site with 'tamp site rm "+owner+" "+mailHost+" --yes'")
		}
		port, err = AllocateDBPort(reg)
		if err != nil {
			return err
		}
		reg[string(name)] = Entry{Path: dir, Hash: res.Hash, DBPort: port, Sites: []string{}}
		return nil
	})
	if err != nil {
		return nil, err
	}

	cfg := NewConfig(name, version, apps, tc, port)
	cfg.Sync.Mode = sync
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

// rollback undoes what a failed create put outside the environment directory.
//
// Its own failures are reported and then dropped: the user is about to be
// told why the create failed, and burying that under "and the cleanup failed
// too" would replace the actionable error with a less useful one.
func (m *Manager) rollback(ctx context.Context, e *Environment, log *createLog) {
	m.Out.Warn(fmt.Sprintf("create failed — rolling back %s", e.Name()))

	// Before the containers, because the far end of the session is one of them.
	m.terminateSync(ctx, e)

	// The router joins the environment's network at the last step of a create,
	// and Docker refuses to remove a network that still has something on it.
	if err := m.router().Detach(ctx, e.Resources.Network()); err != nil {
		m.Out.Warn(fmt.Sprintf("could not detach the router: %v", err))
	}
	if err := m.Engine.ComposeDown(ctx, e.project(), engine.RemoveVolumes, log.stream()); err != nil {
		m.Out.Warn(fmt.Sprintf("could not remove the containers: %v", err))
		m.Out.Warn(fmt.Sprintf("remove them by hand with: docker compose -p %s down --volumes", e.Resources.Project()))
	}
	m.unregister(e.Name())
	// The registry no longer names this environment, so nothing may still
	// route to it.
	if _, err := m.refreshRoutes(ctx); err != nil {
		m.Out.Warn(fmt.Sprintf("could not update the router's routes: %v", err))
	}

	m.Out.Note(fmt.Sprintf("%s was left in place, with %s and %s",
		e.Dir, ConfigFile, filepath.Join(StateDirName, CreateLogFile)))
	m.Out.Note("delete the directory to try again")
}

func (m *Manager) unregister(name Name) {
	err := UpdateRegistry(m.Home, func(reg Registry) error {
		delete(reg, string(name))
		return nil
	})
	if err != nil {
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
