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
	"github.com/zhide915/tamp/internal/syncer"
)

// InitRequest is what `tamp init` was asked for.
type InitRequest struct {
	// Name overrides the folder name; empty names the environment after the
	// directory.
	Name string
	// Frappe, Apps and Sync are create's flags. A re-adoption answers them
	// from its own tamp.toml and ignores them.
	Frappe string
	Apps   string
	Sync   string
	// Explicit names the flags actually typed, so a re-adoption can warn per
	// flag — a user pinning --frappe must not believe they upgraded.
	Explicit []string
}

// Init turns the current directory into an environment. Its one power create
// lacks is re-adoption: `tamp rm` keeps the volumes and the directory, and
// init turns them back into a working environment — same name, same path, so
// the volumes reattach with the data in them.
func (m *Manager) Init(ctx context.Context, req InitRequest) error {
	cwd, err := m.workingDir()
	if err != nil {
		return err
	}
	dir, err := filepath.Abs(cwd)
	if err != nil {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot resolve %q to an absolute path: %v", cwd, err),
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
	template, err := m.cachePolicy(useCache)
	if err != nil {
		return err
	}
	plan, err := m.newPlan(name, req.Frappe, req.Apps, req.Sync, template)
	if err != nil {
		return err
	}
	if err := m.requireFreeName(plan.Name, req.Name == ""); err != nil {
		return err
	}
	return m.raise(ctx, dir, plan)
}

// requireFreeName exists for the message: the fix for a taken folder-derived
// name is --name, which the later check under the lock cannot know to say.
// That later check is what actually makes claiming safe.
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

// leftover reports the environment a directory already holds, or nil for one
// tamp may fill from scratch. A tamp.toml with an apps tree beside it is what
// `tamp rm` leaves; anything else in the directory is somebody's work, and
// tamp will not build on top of it.
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
		// Empty, or only a tamp.toml about to be rewritten — nothing to lose.
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
		// A sync-off environment never had host source, but its volumes
		// survived a removal all the same.
		if hasSource(dir) || syncer.Resolve(cfg.Sync.Mode, runtime.GOOS) == syncer.UseOff {
			return cfg, nil
		}
	}

	return nil, exitcode.New(exitcode.CodeFailed,
		fmt.Sprintf("%s is not empty, and what is in it is not an environment tamp can adopt: it holds %s",
			dir, strings.Join(others, ", ")),
		fmt.Sprintf("run tamp init in an empty directory, or 'tamp create %s' to make one", filepath.Base(dir)))
}

// hasSource requires a non-empty apps tree: a create that failed before the
// bench existed left a directory with no source, and adopting that would be
// adopting nothing.
func hasSource(dir string) bool {
	entries, err := os.ReadDir(syncer.AppsDir(dir))
	return err == nil && len(entries) > 0
}

const readoptSteps = 2 + buildSteps

// readopt rebuilds a removed environment around the source it left behind.
// The surviving tamp.toml decides everything — the volumes about to reattach
// were built to match it, so create's flags cannot apply and are only
// reported.
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

	// A still-registered directory is live, not leftover: a failed build here
	// would roll back — stop and deregister — a healthy environment.
	if err := m.requireUnregistered(e); err != nil {
		return err
	}

	log := &createLog{out: m.Out, steps: m.Out.Steps(readoptSteps + len(cfg.Frappe.Apps))}
	defer log.save(dir)

	log.step("checking Docker")
	if _, err := m.Engine.Ping(ctx); err != nil {
		return err
	}

	log.step("adopting " + dir)
	port, err := Reclaim(m.Home, e.Name(), e.Dir, e.Resources.Hash, e.Config.Ports.DB)
	if err != nil {
		return err
	}
	if port != e.Config.Ports.DB {
		m.Out.Warn(fmt.Sprintf("another environment holds host port %d now, so this one's database moves to %d",
			e.Config.Ports.DB, port))
		// Reaches disk via writeEnvironment, which rewrites tamp.toml.
		e.Config.Ports.DB = port
	}

	sync := m.syncMode(ctx, cfg.Sync.Mode)
	if err := m.writeEnvironment(e, sync); err != nil {
		// The fresh registry entry must not outlive its adoption: it would
		// block the next try and hold a port nothing answers on.
		m.unregister(e.Name())
		return err
	}

	template, err := m.cachePolicy(useCache)
	if err != nil {
		return err
	}
	if _, err := m.build(ctx, e, sync, template, log); err != nil {
		// Volumes kept — they are the data this command exists to bring back.
		m.rollback(ctx, e, engine.KeepVolumes, log)
		m.Out.Note("your source and your data are untouched — run 'tamp init' again once this is fixed")
		return err
	}

	// The new registry entry has no cached site list; the bench is up, so ask
	// it and reassemble the routes — or the environment comes back
	// unreachable.
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

// requireUnregistered refuses to adopt a still-registered environment: an
// entry present means it was never removed, and rebuilding one starts with
// removing it.
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

// samePath compares directories the way the resource hash does, so a
// different spelling of the same path still matches.
func samePath(a, b string) bool {
	return pathHash(a) == pathHash(b)
}
