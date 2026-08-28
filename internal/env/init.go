package env

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/router"
	"github.com/zhide915/tamp/internal/syncer"
)

// InitRequest is what `tamp init` was asked for.
type InitRequest struct {
	// Name overrides the folder name, still unvalidated. Empty means the
	// environment is named after the directory it is being made in.
	Name string
	// Frappe, Apps and Sync are create's flags, and mean the same here. A
	// directory being re-adopted already answers all three from its own
	// tamp.toml, and they are ignored there.
	Frappe string
	Apps   string
	Sync   string
	// Explicit names the flags the user actually typed, defaults excluded. A
	// re-adoption ignores them, and has to say so per flag — a user pinning
	// --frappe version-16 must not believe they upgraded.
	Explicit []string
}

// Init turns the current directory into an environment.
//
// It is create's sibling, and it does one thing create cannot: it re-adopts.
// `tamp rm` deliberately keeps an environment's volumes and never touches its
// directory, and this is the command that turns what is left back into a
// working environment — same name, same path, so the same volumes attach and
// the data comes back with them.
func (m *Manager) Init(ctx context.Context, req InitRequest) error {
	dir, err := filepath.Abs(m.Cwd)
	if err != nil {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot resolve %q to an absolute path: %v", m.Cwd, err),
			"run tamp init from a directory that still exists")
	}

	leftover, err := m.leftover(dir)
	if err != nil {
		return err
	}
	if leftover != nil {
		return m.readopt(ctx, dir, leftover, req)
	}

	name := req.Name
	if name == "" {
		name = filepath.Base(dir)
		if _, err := ParseName(name); err != nil {
			return exitcode.New(exitcode.CodeFailed,
				fmt.Sprintf("this folder is called %q, which tamp cannot use as an environment name", name),
				"name the environment yourself: tamp init --name <name>")
		}
	}
	plan, err := m.newPlan(name, req.Frappe, req.Apps, req.Sync)
	if err != nil {
		return err
	}
	if err := m.requireFreeName(plan.Name, req.Name == ""); err != nil {
		return err
	}
	return m.raise(ctx, dir, plan)
}

// requireFreeName refuses a name the machine has already given out, before
// anything is made.
//
// The registry check happens again under the lock when the name is claimed,
// which is what actually makes it safe. This one exists for the message: the
// answer to a folder whose name is taken is --name, and the answer inside the
// lock cannot know whether tamp chose the name or the user did.
func (m *Manager) requireFreeName(name Name, derived bool) error {
	reg, err := LoadRegistry(m.Home)
	if err != nil {
		return err
	}
	entry, taken := reg[string(name)]
	if !taken {
		return nil
	}

	fix := "pick another name, or remove the old environment with 'tamp rm " + string(name) + "'"
	if derived {
		fix = "name this one yourself: tamp init --name <name>"
	}
	return exitcode.New(exitcode.CodeFailed,
		fmt.Sprintf("an environment named %q is already registered, at %s", name, entry.Path),
		fix)
}

// leftover reports the environment a directory already holds, or nil when the
// directory is one tamp may fill from scratch.
//
// The two are told apart by the source layer. A tamp.toml with an apps tree
// beside it is what `tamp rm` leaves behind — the code is still there, and
// so, unless the user asked otherwise, are the volumes. Anything else in the
// directory is somebody's work, and tamp will not build on top of it.
func (m *Manager) leftover(dir string) (*Config, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot look at %s: %v", dir, err),
			"run tamp init in a directory you can read")
	}

	var others []string
	for _, entry := range entries {
		if entry.Name() != ConfigFile {
			others = append(others, entry.Name())
		}
	}
	if len(others) == 0 {
		// Empty, or holding nothing but a tamp.toml tamp is about to write
		// itself. Either way there is nothing here to lose.
		return nil, nil
	}

	if _, err := os.Stat(ConfigPath(dir)); err == nil {
		cfg, warnings, err := LoadConfig(ConfigPath(dir))
		if err != nil {
			return nil, err
		}
		for _, w := range warnings {
			m.Out.Warn(w)
		}
		// The source is normally what proves this was an environment, but an
		// environment that syncs nothing to the host never had any here — and
		// its volumes survived a removal just the same.
		if hasSource(dir) || syncer.Resolve(cfg.Sync.Mode, runtime.GOOS) == syncer.UseOff {
			return cfg, nil
		}
	}

	return nil, exitcode.New(exitcode.CodeFailed,
		fmt.Sprintf("%s is not empty, and what is in it is not an environment tamp can adopt: it holds %s",
			dir, strings.Join(others, ", ")),
		fmt.Sprintf("run tamp init in an empty directory, or 'tamp create %s' to make one", filepath.Base(dir)))
}

// hasSource reports whether the directory holds an apps tree with something in
// it. Empty does not count: an environment whose create failed before the
// bench existed has the directory and none of the source, and adopting that
// would be adopting nothing.
func hasSource(dir string) bool {
	entries, err := os.ReadDir(syncer.AppsDir(dir))
	return err == nil && len(entries) > 0
}

// readoptSteps is buildSteps plus the two an adoption does first.
const readoptSteps = 2 + buildSteps

// readopt brings a removed environment back around the source it left behind.
//
// The tamp.toml decides everything — its Frappe version, its toolchain, its
// apps — because the volumes that may be about to reattach were built to match
// it. That is why create's flags are ignored here and merely reported: an
// environment cannot change Frappe version by being adopted, and pretending
// otherwise would hand the user a bench its own data does not fit.
func (m *Manager) readopt(ctx context.Context, dir string, cfg *Config, req InitRequest) error {
	m.Out.Note(ConfigFile + " already says what this environment is — tamp is regenerating everything else from it")
	if req.Name != "" {
		m.Out.Warn(fmt.Sprintf("--name is ignored here: this environment's volumes are named for %q, and renaming it would leave them behind",
			cfg.Name))
	}
	for _, flag := range req.Explicit {
		m.Out.Warn(flag + " is ignored here: " + ConfigFile + " already records what this environment is made of")
	}

	res, err := NewResources(cfg.Name, dir)
	if err != nil {
		return err
	}
	e := &Environment{Dir: dir, Config: cfg, Resources: res}

	// A directory that is still a registered environment is not a leftover:
	// adopting it would re-run the build against a live bench, and a failure
	// there rolls back — stopping and deregistering — an environment that was
	// healthy before init was typed.
	if err := m.requireUnregistered(e); err != nil {
		return err
	}

	log := &createLog{out: m.Out, total: readoptSteps + len(cfg.Frappe.Apps)}
	defer log.save(dir)

	log.step("checking Docker")
	if _, err := m.Engine.Ping(ctx); err != nil {
		return err
	}

	log.step("adopting " + dir)
	if err := m.reregister(e); err != nil {
		return err
	}

	sync := m.syncMode(ctx, cfg.Sync.Mode)
	if err := m.writeEnvironment(e, sync); err != nil {
		// The entry reregister just made must not outlive the adoption it was
		// made for: a registry naming a dead environment blocks the next try
		// and holds a port nothing answers on.
		m.unregister(e.Name())
		return err
	}

	if _, err := m.build(ctx, e, sync, log); err != nil {
		// Volumes are kept, and that is the whole promise being kept: they are
		// the data this command exists to bring back, and a failed adoption
		// must leave it exactly as it found it.
		m.rollback(ctx, e, engine.KeepVolumes, log)
		m.Out.Note("your source and your data are untouched — run 'tamp init' again once this is fixed")
		return err
	}

	// The registry entry this adoption made is new, and the site list tamp
	// caches in it went with the old one. The bench is up now and knows what
	// it has, so it is asked, and the routes are assembled again from what it
	// said — otherwise an adopted environment comes back unreachable.
	if _, _, err := m.sites(ctx, e); err != nil {
		return err
	}
	status, err := m.refreshRoutes(ctx)
	if err != nil {
		return err
	}

	m.Out.OK(fmt.Sprintf("%s adopted — the environment is back", cfg.Name))
	m.Out.Note("its volumes reattached by name and path, so every site's data came with them")
	m.announceRoutes(e, status)
	m.Out.Hint(fmt.Sprintf("see what it has: tamp site list %s", cfg.Name))
	return nil
}

// requireUnregistered refuses to adopt a directory that is still a registered
// environment. init exists for what `tamp rm` left behind, and rm removes the
// registry entry — an entry still present means the environment was never
// removed, and the way to rebuild one of those starts with removing it.
func (m *Manager) requireUnregistered(e *Environment) error {
	reg, err := LoadRegistry(m.Home)
	if err != nil {
		return err
	}
	existing, taken := reg[string(e.Name())]
	if !taken || !samePath(existing.Path, e.Dir) {
		return nil
	}
	return exitcode.New(exitcode.CodeFailed,
		fmt.Sprintf("%q is already an environment on this machine — there is nothing to adopt", e.Name()),
		fmt.Sprintf("start it with 'tamp start %s'; to rebuild it from source, run 'tamp rm %s' first (volumes are kept) and then tamp init again",
			e.Name(), e.Name()))
}

// reregister puts a re-adopted environment back in the machine's index.
//
// The port it used to publish is taken again when nothing else has claimed it,
// so a database client's saved connection still works. When something has, a
// new one is allocated and written back into tamp.toml — the alternative is
// two environments publishing one port, of which only the first would start.
func (m *Manager) reregister(e *Environment) error {
	name := string(e.Name())

	var port int
	err := UpdateRegistry(m.Home, func(reg Registry) error {
		if existing, taken := reg[name]; taken && !samePath(existing.Path, e.Dir) {
			return exitcode.New(exitcode.CodeFailed,
				fmt.Sprintf("an environment named %q is already registered, at %s", name, existing.Path),
				"remove that one with 'tamp rm "+name+"', or rename this directory's environment in "+ConfigFile)
		}
		// The environment's own mail hostname always looks claimed by the
		// environment itself, which is not a clash — only another environment
		// having taken it as a site is.
		if owner, what, clash := hostClaimedBy(reg, name, router.MailHost(name)); clash && owner != name {
			return exitcode.New(exitcode.CodeFailed,
				fmt.Sprintf("%s is already %s of %q", router.MailHost(name), what, owner),
				"rename this directory's environment in "+ConfigFile)
		}

		// The port this environment recorded is its own to take back, and the
		// registry is the only thing that can say otherwise. Whether something
		// is listening on it right now is not the question: an environment
		// being adopted in place is quite likely to be listening on it itself.
		port = e.Config.Ports.DB
		if claimedBy(reg, name, port) {
			var err error
			if port, err = AllocateDBPort(reg); err != nil {
				return err
			}
		}
		reg[name] = Entry{Path: e.Dir, Hash: e.Resources.Hash, DBPort: port, Sites: sitesOf(reg, name)}
		return nil
	})
	if err != nil {
		return err
	}

	if port != e.Config.Ports.DB {
		m.Out.Warn(fmt.Sprintf("another environment holds host port %d now, so this one's database moves to %d",
			e.Config.Ports.DB, port))
		// Written back by writeEnvironment, which rewrites tamp.toml as part
		// of regenerating everything this adoption regenerates.
		e.Config.Ports.DB = port
	}
	return nil
}

// claimedBy reports whether an environment other than self holds a host port.
func claimedBy(reg Registry, self string, port int) bool {
	for name, entry := range reg {
		if name != self && entry.DBPort == port {
			return true
		}
	}
	return false
}

// sitesOf is the site list tamp last recorded for an environment, which an
// adoption keeps: the bench is about to be asked anyway, and until it answers
// this is what its routes are assembled from.
func sitesOf(reg Registry, name string) []string {
	if entry, ok := reg[name]; ok {
		return entry.Sites
	}
	return []string{}
}

// samePath compares two environment directories the way the resource hash
// does, so that a path reached by a different spelling is still the same
// environment.
func samePath(a, b string) bool {
	return pathHash(a) == pathHash(b)
}
