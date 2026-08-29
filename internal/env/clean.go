package env

import (
	"context"
	"fmt"
	"strings"

	"github.com/zhide915/tamp/internal/exitcode"
)

// sourceUntouched closes every clean and rebuild. Deliberately not "tamp never
// deletes apps/": a deps clean takes the node_modules and __pycache__ that sit
// inside it. Nothing the user wrote ever goes.
const sourceUntouched = "your source code is untouched — tamp deletes nothing you wrote"

// keptSource is the line every destructive preview ends on: the one layer no
// confirmation is ever about, named with the directory it lives in.
func keptSource(e *Environment) string {
	return "  source  " + e.Dir + "  (tamp never deletes it)"
}

// CleanRequest is what `tamp clean` was asked for. No layer named is not an
// error: it is the request to be told what the layers are.
type CleanRequest struct {
	Env    string
	Deps   bool
	Assets bool
	Data   bool
	// All names every wipeable layer at once; source is not one of them.
	All bool
	// Yes replaces a prompt — agents run these commands, so confirmation is a
	// flag.
	Yes bool
}

func (r CleanRequest) deps() bool   { return r.Deps || r.All }
func (r CleanRequest) assets() bool { return r.Assets || r.All }
func (r CleanRequest) data() bool   { return r.Data || r.All }

func (r CleanRequest) namesNoLayer() bool { return !r.deps() && !r.assets() && !r.data() }

// layers names what is being wiped, in the order it is wiped.
func (r CleanRequest) layers() []string {
	var named []string
	if r.data() {
		named = append(named, "data")
	}
	if r.assets() {
		named = append(named, "assets")
	}
	if r.deps() {
		named = append(named, "deps")
	}
	return named
}

// Clean wipes the layers the request names. Named in destruction order rather
// than table order: dropping a site is a bench command, and a wiped deps
// layer has no bench to run it with.
func (m *Manager) Clean(ctx context.Context, req CleanRequest) error {
	// The layer table is the answer to `tamp clean` with no layer, and it is
	// true without an environment — the storage model is tamp's, not one
	// environment's.
	if req.namesNoLayer() {
		m.printLayers()
		m.Out.Hint("name a layer to wipe it: tamp clean --deps")
		return nil
	}

	e, err := m.resolve(req.Env)
	if err != nil {
		return err
	}
	if err := m.requireRunning(ctx, e, "so tamp cannot clean it"); err != nil {
		return err
	}

	// Read before anything is destroyed: the preview names the sites, and the
	// wipe iterates them.
	hosts, _, err := m.sites(ctx, e)
	if err != nil {
		return err
	}

	// Only the data layer is worth a confirmation; the other two are a
	// rebuild away.
	if req.data() && !req.Yes {
		m.previewClean(e, req, hosts)
		return exitcode.New(exitcode.CodeConfirmationRequired,
			fmt.Sprintf("cleaning the data layer of %s is destructive", e.Name()),
			"add --yes once the list above is what you meant")
	}

	bench := e.bench(m.Engine, m.Out.Stream())
	steps := m.Out.Steps(req.steps(len(hosts)))

	if req.data() {
		password, err := ReadDBRootPassword(e.Dir)
		if err != nil {
			return err
		}
		for _, host := range hosts {
			steps.Step("destroying " + host + " and its database")
			if err := bench.DropSite(ctx, host, password); err != nil {
				return err
			}
		}
		steps.Step("clearing what bench archived")
		if err := bench.ClearArchivedSites(ctx); err != nil {
			return err
		}
		// Re-asked because the bench is the authority: recording its now
		// empty list is what takes the routes away.
		if _, _, err := m.sites(ctx, e); err != nil {
			return err
		}
		if _, err := m.refreshRoutes(ctx); err != nil {
			return err
		}
	}
	if req.assets() {
		steps.Step("wiping the built assets")
		if err := bench.CleanAssets(ctx); err != nil {
			return err
		}
	}
	if req.deps() {
		// The processes run from the virtualenv and honcho exits with them,
		// taking the container down. Without a Procfile the container idles
		// instead, so 'tamp rebuild' still has somewhere to run.
		steps.Step("stopping the bench processes — they run from the virtualenv")
		if err := bench.RemoveProcfile(ctx); err != nil {
			return err
		}
		if err := m.Engine.ComposeRestart(ctx, e.project(), FrappeService, m.Out.Stream()); err != nil {
			return err
		}

		steps.Step("wiping the virtualenv, node_modules and __pycache__")
		if err := bench.CleanDeps(ctx); err != nil {
			return err
		}
	}

	m.Out.OK(fmt.Sprintf("cleaned %s: %s", e.Name(), strings.Join(req.layers(), ", ")))
	m.Out.Note(sourceUntouched)
	m.announceNextSteps(e, req)
	return nil
}

// steps counts one per site plus one per layer; the archive sweep closes the
// data layer, and the deps layer costs an extra one for stopping the
// processes that live in it.
func (r CleanRequest) steps(sites int) int {
	steps := 0
	if r.data() {
		steps += sites + 1
	}
	if r.assets() {
		steps++
	}
	if r.deps() {
		steps += 2
	}
	return steps
}

// announceNextSteps names the command that restores each wiped layer.
func (m *Manager) announceNextSteps(e *Environment, req CleanRequest) {
	if req.deps() {
		m.Out.Note(fmt.Sprintf("%s is up but serving nothing — its processes come back with the dependencies", e.Name()))
	}
	if req.deps() || req.assets() {
		m.Out.Hint(fmt.Sprintf("next: tamp rebuild %s", e.Name()))
	}
	if req.data() {
		m.Out.Hint(fmt.Sprintf("next: tamp site new %s <host>, or tamp snapshot restore %s", e.Name(), e.Name()))
	}
}

// previewClean prints exactly what --yes would destroy.
func (m *Manager) previewClean(e *Environment, req CleanRequest, hosts []string) {
	m.Out.Print(fmt.Sprintf("tamp clean would destroy, in %s:", e.Name()))
	if len(hosts) == 0 {
		m.Out.Print("  data    no sites — there is nothing in the data layer yet")
	}
	for _, host := range hosts {
		m.Out.Print("  data    " + host + "  (its database, its files, its config)")
	}
	if req.assets() {
		m.Out.Print("  assets  every built JS and CSS bundle")
	}
	if req.deps() {
		m.Out.Print("  deps    the virtualenv, node_modules and __pycache__")
	}

	m.Out.Print("")
	m.Out.Print("it would keep:")
	m.Out.Print(keptSource(e))
	if req.data() {
		m.Out.Print("")
		m.Out.Hint("a snapshot would bring the data back afterwards: tamp snapshot create " + e.Name().String())
	}
	m.Out.Print("")
	m.Out.Print("to clean the data layer:")
	m.Out.Print("  tamp clean " + e.Name().String() + cleanFlags(req) + " --yes")
}

func cleanFlags(req CleanRequest) string {
	if req.All {
		return " --all"
	}
	var flags string
	if req.Data {
		flags += " --data"
	}
	if req.Assets {
		flags += " --assets"
	}
	if req.Deps {
		flags += " --deps"
	}
	return flags
}

// printLayers is the tool teaching its own storage model.
func (m *Manager) printLayers() {
	m.Out.Print("an environment stores four things, and tamp wipes them separately:")
	m.Out.Print("")
	m.Out.Table(
		[]string{"LAYER", "HOLDS", "WIPED BY", "RESTORED BY"},
		[][]string{
			{"source", "apps/ — your code", "nothing tamp does", "it is yours, and tamp never deletes it"},
			{"deps", "the virtualenv, node_modules, __pycache__", "tamp clean --deps", "tamp rebuild"},
			{"assets", "the built JS and CSS", "tamp clean --assets", "tamp rebuild"},
			{"data", "every site's database, files and config", "tamp clean --data --yes", "tamp site new <host>, or tamp snapshot restore"},
		})
	m.Out.Print("")
	m.Out.Print("--all names every wipeable layer; source is never one of them.")
	m.Out.Note(sourceUntouched)
}

// Rebuild restores the two disposable layers: the dependencies, from the
// machine-wide caches, and the assets built from them.
func (m *Manager) Rebuild(ctx context.Context, name string) error {
	e, err := m.resolve(name)
	if err != nil {
		return err
	}
	if err := m.requireRunning(ctx, e, "so tamp cannot rebuild it"); err != nil {
		return err
	}

	bench := e.bench(m.Engine, m.Out.Stream())
	steps := m.Out.Steps(rebuildSteps)

	steps.Step("installing the apps' Python and Node dependencies")
	if err := bench.SetupRequirements(ctx); err != nil {
		return err
	}
	steps.Step("building the assets")
	if err := bench.Build(ctx); err != nil {
		return err
	}

	// A deps clean took the Procfile away to keep the container up, so
	// writing it back is what makes the bench serve again; elsewhere this is
	// a restart, which a rebuild wants anyway — the code just changed.
	steps.Step("starting the bench processes")
	if err := bench.WriteProcfile(ctx); err != nil {
		return err
	}
	if err := m.Engine.ComposeRestart(ctx, e.project(), FrappeService, m.Out.Stream()); err != nil {
		return err
	}
	if err := bench.WaitForWeb(ctx); err != nil {
		return err
	}

	m.Out.OK(fmt.Sprintf("%s rebuilt — deps and assets are back, and it is serving again", e.Name()))
	m.Out.Note(sourceUntouched)
	m.Out.Note("the data layer was not touched either — every site keeps its database")
	return nil
}

const rebuildSteps = 3
