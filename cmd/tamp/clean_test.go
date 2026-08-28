package main

import (
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/frappe"
)

// --- the layer table ------------------------------------------------------

// The tool teaching its own storage model: no flag means "explain", not
// "guess what I meant".
func TestCleanWithNoLayerPrintsTheLayerTableAndDestroysNothing(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.siteNew(t, "demo", "demo.localhost", "--admin-password", "secret")
	before := len(c.engine.Execs)

	r := c.run(t, "clean", "demo")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t,
		"LAYER", "HOLDS", "WIPED BY", "RESTORED BY",
		"source", "deps", "assets", "data",
		"tamp clean --deps", "tamp clean --assets", "tamp clean --data --yes",
		"tamp rebuild", "tamp site new <host>")
	if len(c.engine.Execs) != before {
		t.Errorf("the layer table ran %d container commands, want none", len(c.engine.Execs)-before)
	}
	if got := c.engine.Sites(); len(got) != 1 {
		t.Errorf("the layer table lost sites: bench has %v", got)
	}
}

// The table is tamp's storage model, not one environment's, so it answers
// outside an environment too.
func TestCleanPrintsTheLayerTableWithoutAnEnvironment(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "clean")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "LAYER", "tamp deletes nothing you wrote")
}

// --- the safe layers ------------------------------------------------------

func TestCleanDepsWipesTheVirtualenvAndNeedsNoConfirmation(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")

	r := c.run(t, "clean", "demo", "--deps")

	r.assertCode(t, exitcode.CodeOK)
	if !c.engine.Ran(frappe.EnvDir) {
		t.Error("clean --deps never emptied the virtualenv")
	}
	if !c.engine.Ran("node_modules") || !c.engine.Ran("__pycache__") {
		t.Error("clean --deps left node_modules or __pycache__ behind")
	}
	r.assertStdoutContains(t, "cleaned demo: deps", "tamp deletes nothing you wrote", "next: tamp rebuild demo")
}

func TestCleanAssetsWipesTheBuiltBundlesAndNeedsNoConfirmation(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")

	r := c.run(t, "clean", "demo", "--assets")

	r.assertCode(t, exitcode.CodeOK)
	if !c.engine.Ran(frappe.AssetsDir) {
		t.Error("clean --assets never removed the built assets")
	}
	r.assertStdoutContains(t, "cleaned demo: assets", "next: tamp rebuild demo")
}

// The safe layers must not reach the data layer, whatever else they do.
func TestCleanDepsLeavesEverySiteInPlace(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.siteNew(t, "demo", "demo.localhost", "--admin-password", "secret")

	c.run(t, "clean", "demo", "--deps").assertCode(t, exitcode.CodeOK)

	if c.engine.Ran("bench drop-site") {
		t.Error("clean --deps dropped a site")
	}
	if got := c.engine.Sites(); len(got) != 1 || got[0] != "demo.localhost" {
		t.Errorf("bench sites = %v, want [demo.localhost]", got)
	}
}

// --- the data layer -------------------------------------------------------

func TestCleanDataWithoutYesExitsFiveNamingWhatWouldDie(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.siteNew(t, "demo", "demo.localhost", "--admin-password", "secret")

	r := c.run(t, "clean", "demo", "--data")

	r.assertCode(t, exitcode.CodeConfirmationRequired)
	r.assertStdoutContains(t,
		"tamp clean would destroy, in demo:",
		"demo.localhost",
		"it would keep:",
		"source",
		"tamp clean demo --data --yes")
	if c.engine.Ran("bench drop-site") {
		t.Error("the preview dropped a site")
	}
	if got := c.engine.Sites(); len(got) != 1 {
		t.Errorf("the preview destroyed data: bench sites = %v", got)
	}
}

func TestCleanDataWithYesEmptiesTheSiteListAndTheRoutes(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.siteNew(t, "demo", "demo.localhost", "--admin-password", "secret")

	r := c.run(t, "clean", "demo", "--data", "--yes")

	r.assertCode(t, exitcode.CodeOK)
	if got := c.engine.Sites(); len(got) != 0 {
		t.Errorf("bench sites = %v, want none", got)
	}
	if !c.engine.Ran(frappe.ArchivedDir) {
		t.Error("clean --data left the archived sites behind")
	}
	if got := c.registered(t, "demo"); len(got) != 0 {
		t.Errorf("the registry still records %v as demo's sites", got)
	}
	if caddy := c.caddyfile(t); strings.Contains(caddy, "http://demo.localhost") {
		t.Errorf("the router still routes the destroyed site:\n%s", caddy)
	}
	r.assertStdoutContains(t, "cleaned demo: data", "tamp deletes nothing you wrote", "next: tamp site new demo <host>")
}

// The exit-5 contract is about the data layer; --all carries it along.
func TestCleanAllWithoutYesExitsFive(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")

	r := c.run(t, "clean", "demo", "--all")

	r.assertCode(t, exitcode.CodeConfirmationRequired)
	r.assertStdoutContains(t, "tamp clean demo --all --yes")
}

// Dropping a site is a bench command, and bench lives in the virtualenv: the
// data layer has to go before the deps layer that runs it.
func TestCleanAllDropsSitesBeforeItWipesTheVirtualenv(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.siteNew(t, "demo", "demo.localhost", "--admin-password", "secret")
	mark := c.mark()

	c.run(t, "clean", "demo", "--all", "--yes").assertCode(t, exitcode.CodeOK)

	drop, wipe := c.ranAtSince(t, mark, "bench drop-site"), c.ranAtSince(t, mark, frappe.EnvDir)
	if drop > wipe {
		t.Errorf("clean --all wiped the virtualenv (command %d) before dropping the site (command %d)", wipe, drop)
	}
}

// The bench's processes run from the virtualenv, and honcho exits with them,
// taking the container down. Nothing would be left for rebuild to run in.
func TestCleanDepsLeavesTheContainerUpForRebuildToReach(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	mark := c.mark()

	r := c.run(t, "clean", "demo", "--deps")

	r.assertCode(t, exitcode.CodeOK)
	if _, ok := c.engine.Wrote(frappe.ProcfilePath); ok {
		t.Error("clean --deps left the Procfile, so the container restarts into a bench it cannot run")
	}
	if len(c.ops("ComposeRestart")) != 2 {
		t.Errorf("clean --deps restarted the bench service %d times, want 2 — one from create, one to drop the processes",
			len(c.ops("ComposeRestart")))
	}
	stop, wipe := c.ranAtSince(t, mark, frappe.ProcfilePath), c.ranAtSince(t, mark, frappe.EnvDir)
	if stop > wipe {
		t.Errorf("clean --deps wiped the virtualenv (command %d) before stopping the processes running from it (command %d)", wipe, stop)
	}
	r.assertStdoutContains(t, "up but serving nothing", "next: tamp rebuild demo")
}

// --- rebuild --------------------------------------------------------------

func TestRebuildRestoresTheDependenciesAndTheAssets(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")

	r := c.run(t, "rebuild", "demo")

	r.assertCode(t, exitcode.CodeOK)
	if !c.engine.Ran("bench setup requirements") {
		t.Error("rebuild never reinstalled the dependencies")
	}
	if !c.engine.Ran("bench build") {
		t.Error("rebuild never built the assets")
	}
	r.assertStdoutContains(t, "demo rebuilt", "serving again", "tamp deletes nothing you wrote")
}

// The whole promise of the pair: wipe the deps layer, rebuild, and the site
// is served again by processes that are actually running.
func TestCleanDepsThenRebuildLeavesTheBenchServingAgain(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.siteNew(t, "demo", "demo.localhost", "--admin-password", "secret")
	c.run(t, "clean", "demo", "--deps").assertCode(t, exitcode.CodeOK)

	r := c.run(t, "rebuild", "demo")

	r.assertCode(t, exitcode.CodeOK)
	if _, ok := c.engine.Wrote(frappe.ProcfilePath); !ok {
		t.Error("rebuild did not put the process list back, so nothing serves")
	}
	if !c.engine.Ran("curl") {
		t.Error("rebuild returned without waiting for the web server to answer")
	}
}

func TestRebuildRefusesAStoppedEnvironment(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.run(t, "stop", "demo").assertCode(t, exitcode.CodeOK)

	r := c.run(t, "rebuild", "demo")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, "demo is not running", "tamp start demo")
	if c.engine.Ran("bench build") {
		t.Error("rebuild built assets in a stopped environment")
	}
}

func TestCleanRefusesAStoppedEnvironment(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.run(t, "stop", "demo").assertCode(t, exitcode.CodeOK)

	r := c.run(t, "clean", "demo", "--deps")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, "demo is not running")
}

// ranAtSince is the position of the first container command after mark that
// contained fragment — what pins the order two operations must happen in.
func (c *cli) ranAtSince(t *testing.T, mark int, fragment string) int {
	t.Helper()
	at := execIndex(c.engine.Execs[mark:], fragment)
	if at < 0 {
		t.Fatalf("tamp never ran anything containing %q in a container", fragment)
	}
	return at
}
