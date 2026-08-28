package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/engine/enginetest"
	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/syncer"
)

// rm keeps the volumes and the directory precisely so that init can put an
// environment back with its data intact.

// inside runs tamp from a subdirectory of the sandbox, creating it first.
func (c *cli) inside(t *testing.T, dir string, args ...string) result {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	defer t.Chdir(c.dir)
	return c.run(t, args...)
}

// --- an empty directory ----------------------------------------------------

func TestInitNamesTheEnvironmentAfterItsFolder(t *testing.T) {
	c := sandbox(t)

	r := c.inside(t, c.path("shopfloor"), "init", "--frappe", "version-15", "--sync", "bind")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "shopfloor ready")
	if config := c.read(t, "shopfloor", env.ConfigFile); !strings.Contains(config, `name = "shopfloor"`) {
		t.Errorf("tamp.toml does not name the environment after its folder:\n%s", config)
	}
	if c.registeredPath(t, "shopfloor") != c.path("shopfloor") {
		t.Errorf("tamp registered %q, want %q", c.registeredPath(t, "shopfloor"), c.path("shopfloor"))
	}
}

func TestInitTakesTheNameFromTheFlagInstead(t *testing.T) {
	c := sandbox(t)

	r := c.inside(t, c.path("Some Folder"), "init", "--name", "erp15", "--frappe", "version-15", "--sync", "bind")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "erp15 ready")
}

// The error must point at --name, not merely refuse.
func TestInitPointsAtNameWhenTheFolderCannotBeOne(t *testing.T) {
	c := sandbox(t)

	r := c.inside(t, c.path("My Project"), "init", "--frappe", "version-15")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, "My Project", "--name")
}

func TestInitPointsAtNameWhenTheFolderNameIsTaken(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo", "--sync", "bind")

	r := c.inside(t, filepath.Join(c.path("elsewhere"), "demo"), "init", "--frappe", "version-15")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, "already registered", "--name")
}

func TestInitRefusesADirectoryWithSomebodyElsesThingsInIt(t *testing.T) {
	c := sandbox(t)
	dir := c.path("mine")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := c.inside(t, dir, "init", "--frappe", "version-15")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, "not empty")
	if c.exists("mine", env.ConfigFile) {
		t.Error("tamp wrote into a directory it refused")
	}
}

// --- re-adoption -----------------------------------------------------------

func TestInitReadoptsWhatRemoveLeftBehind(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo", "--sync", "bind")
	c.siteNew(t, "demo", "shop.localhost")
	c.leaveSource(t, "demo")

	c.run(t, "rm", "demo", "--yes").assertCode(t, exitcode.CodeOK)
	if c.removedVolumes(t) {
		t.Fatal("rm removed the volumes it promised to keep, so there is nothing to adopt")
	}

	r := c.inside(t, c.path("demo"), "init")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "demo adopted")
	if c.registeredPath(t, "demo") != c.path("demo") {
		t.Error("the adopted environment is not back in the registry")
	}
	// Same name and path, so the volumes reattach with the site's data.
	c.run(t, "site", "list", "demo").assertStdoutContains(t, "shop.localhost")
	if !strings.Contains(c.caddyfile(t), "http://shop.localhost {") {
		t.Error("the adopted environment's site is not routed again")
	}
}

// bench init over existing source prompts, which in a container aborts.
func TestInitRebuildsAroundTheSourceRatherThanCloningOverIt(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo", "--sync", "bind")
	c.leaveSource(t, "demo")
	c.run(t, "rm", "demo", "--yes").assertCode(t, exitcode.CodeOK)

	before := len(c.engine.Execs)
	c.inside(t, c.path("demo"), "init").assertCode(t, exitcode.CodeOK)

	adopted := c.engine.Execs[before:]
	for _, e := range adopted {
		if strings.Contains(e.Line(), "bench init") {
			t.Fatal("tamp ran bench init over source that was already there")
		}
	}
	if !ranAny(adopted, "bench setup requirements") {
		t.Error("tamp adopted the source without rebuilding the environment around it")
	}
}

// When adopting, tamp.toml rules: the reattaching volumes match what it records.
func TestInitIgnoresTheFlagsWhenAdopting(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo", "--frappe", "version-15", "--sync", "bind")
	c.leaveSource(t, "demo")
	c.run(t, "rm", "demo", "--yes").assertCode(t, exitcode.CodeOK)

	r := c.inside(t, c.path("demo"), "init", "--frappe", "version-16", "--name", "other")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "demo adopted")
	r.assertStderrContains(t, "--name is ignored")
	// Each ignored flag is named, or the user believes the upgrade happened.
	r.assertStderrContains(t, "--frappe is ignored")
	if config := c.read(t, "demo", env.ConfigFile); !strings.Contains(config, `version = "version-15"`) {
		t.Errorf("adopting changed the environment's Frappe version:\n%s", config)
	}
}

// Adopting a live environment would re-run the build, whose failure path
// tears the environment down.
func TestInitRefusesADirectoryThatIsStillARegisteredEnvironment(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo", "--sync", "bind")
	c.leaveSource(t, "demo")

	r := c.inside(t, c.path("demo"), "init")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, "already an environment")
	if c.registeredPath(t, "demo") != c.path("demo") {
		t.Error("a refused init damaged the registry")
	}
}

// A lone tamp.toml holds nothing to lose; tamp writes that file anyway.
func TestInitTreatsAConfigOnlyDirectoryAsFresh(t *testing.T) {
	c := sandbox(t)
	dir := c.path("shopfloor")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, env.ConfigFile), []byte("schema = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := c.inside(t, dir, "init", "--frappe", "version-15", "--sync", "bind")

	r.assertCode(t, exitcode.CodeOK)
	if c.registeredPath(t, "shopfloor") != dir {
		t.Error("the config-only directory was not initialized")
	}
}

// With fresh volumes the sync mirrors host apps in after bench init — apps
// bench never cloned, which must still be registered or they stay unloadable.
func TestReadoptWithFreshVolumesRegistersTheAppsTheSyncBringsBack(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo", "--sync", "mutagen")
	c.leaveSource(t, "demo")
	c.run(t, "rm", "demo", "--volumes", "--yes").assertCode(t, exitcode.CodeOK)

	before := len(c.engine.Execs)
	r := c.inside(t, c.path("demo"), "init")

	r.assertCode(t, exitcode.CodeOK)
	adopted := c.engine.Execs[before:]
	if !ranAny(adopted, "bench init") {
		t.Fatal("fresh volumes should have taken the bench init path")
	}
	if !ranAny(adopted, "apps.txt") {
		t.Error("the apps the sync mirrored back were never registered on the bench")
	}
}

func TestInitAfterRemoveWithVolumesStartsWithNothingInTheDatabase(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo", "--sync", "bind")
	c.siteNew(t, "demo", "shop.localhost")
	c.leaveSource(t, "demo")

	c.run(t, "rm", "demo", "--volumes", "--yes").assertCode(t, exitcode.CodeOK)
	if !c.removedVolumes(t) {
		t.Fatal("rm --volumes kept the volumes")
	}

	r := c.inside(t, c.path("demo"), "init")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "demo adopted")
	if sites := c.engine.Sites(); len(sites) != 0 {
		t.Errorf("the adopted bench has %v, and its volumes were destroyed", sites)
	}
}

func TestAFailedAdoptionKeepsTheVolumes(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo", "--sync", "bind")
	c.leaveSource(t, "demo")
	c.run(t, "rm", "demo", "--yes").assertCode(t, exitcode.CodeOK)
	c.engine.ExecFails = map[string]error{"bench setup requirements": exitcode.New(exitcode.CodeFailed, "no network", "")}

	r := c.inside(t, c.path("demo"), "init")

	r.assertCode(t, exitcode.CodeFailed)
	if c.removedVolumes(t) {
		t.Fatal("a failed adoption destroyed the data it exists to bring back")
	}
	r.assertStdoutContains(t, "source and your data are untouched")
}

// --- helpers ---------------------------------------------------------------

// leaveSource plants the apps tree a bind mount or sync session would have
// left on the host — what makes a directory adoptable.
func (c *cli) leaveSource(t *testing.T, name string) {
	t.Helper()
	app := filepath.Join(c.path(name, syncer.AppsDirName), "frappe")
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "hooks.py"), []byte("app_name = \"frappe\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// tamp probes the bench for the app, so the fake holds it too.
	c.engine.AddApp("frappe")
}

func (c *cli) removedVolumes(t *testing.T) bool {
	t.Helper()
	downs := c.ops("ComposeDown")
	if len(downs) == 0 {
		t.Fatal("tamp never took the environment down")
	}
	for _, op := range downs {
		if op.Removal == engine.RemoveVolumes {
			return true
		}
	}
	return false
}

func (c *cli) registeredPath(t *testing.T, name string) string {
	t.Helper()
	reg, err := env.LoadRegistry(filepath.Join(c.home, env.HomeDirName))
	if err != nil {
		t.Fatalf("cannot read the registry: %v", err)
	}
	return reg[name].Path
}

func ranAny(execs []enginetest.Exec, fragment string) bool {
	for _, e := range execs {
		if strings.Contains(e.Line(), fragment) {
			return true
		}
	}
	return false
}
