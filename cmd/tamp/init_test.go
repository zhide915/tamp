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

// init is create's sibling and rm's other half. The first of those is a
// convenience; the second is the whole safety story — rm keeps the volumes and
// never deletes the directory precisely so that this command can put an
// environment back with its data intact.

// inside runs tamp from a directory beneath the sandbox, making it first.
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

// A folder tamp cannot turn into a hostname is not a reason to stop: --name
// is, and the error has to say so rather than merely refusing.
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

// Whatever is in a non-empty directory is somebody's work, and tamp will not
// build an environment on top of it.
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

// The whole cycle rm was designed around: remove the environment, and put it
// back around the source and the volumes that survived.
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
	// The site's data is in the volumes, and the volumes came back attached
	// because the environment kept its name and its path.
	c.run(t, "site", "list", "demo").assertStdoutContains(t, "shop.localhost")
	if !strings.Contains(c.caddyfile(t), "http://shop.localhost {") {
		t.Error("the adopted environment's site is not routed again")
	}
}

// The source is the one thing tamp never destroys, so adopting must not
// re-clone over it — bench refuses that interactively, which in a container is
// a command that aborts.
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

// tamp.toml is the authority for an environment being adopted: the volumes
// about to reattach were built for what it records.
func TestInitIgnoresTheFlagsWhenAdopting(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo", "--frappe", "version-15", "--sync", "bind")
	c.leaveSource(t, "demo")
	c.run(t, "rm", "demo", "--yes").assertCode(t, exitcode.CodeOK)

	r := c.inside(t, c.path("demo"), "init", "--frappe", "version-16", "--name", "other")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "demo adopted")
	r.assertStderrContains(t, "--name is ignored")
	// Every ignored flag is named: a user pinning --frappe version-16 must
	// not walk away believing they upgraded.
	r.assertStderrContains(t, "--frappe is ignored")
	if config := c.read(t, "demo", env.ConfigFile); !strings.Contains(config, `version = "version-15"`) {
		t.Errorf("adopting changed the environment's Frappe version:\n%s", config)
	}
}

// init exists for what rm left behind. A directory that is still a registered
// environment has nothing to adopt — and adopting it anyway would re-run the
// build against a live bench, whose failure path tears the environment down.
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

// A directory holding nothing but a tamp.toml is fresh, not foreign: there is
// nothing in it to lose, and tamp is about to write that file itself.
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

// Under Mutagen, fresh volumes mean bench init runs first and the session then
// mirrors the host's apps back in — apps bench did not clone and knows nothing
// about. They have to be registered, or the environment comes back with its
// source present but unloadable.
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

// The other half of the promise: --volumes really does mean the data is gone,
// and adopting afterwards brings back the source and nothing else.
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

// An adoption that fails must leave the data exactly as it found it: keeping
// it is the only reason this command exists.
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

// leaveSource puts an apps tree on the host, which is what a bind mount or a
// sync session would have left there and what makes a directory adoptable.
func (c *cli) leaveSource(t *testing.T, name string) {
	t.Helper()
	app := filepath.Join(c.path(name, syncer.AppsDirName), "frappe")
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "hooks.py"), []byte("app_name = \"frappe\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The bench in the container holds it too, which is what tamp probes for.
	c.engine.AddApp("frappe")
}

// removedVolumes reports whether the last compose down took the volumes.
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

// registeredPath is where the machine's registry says an environment lives.
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
