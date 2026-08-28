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
	"github.com/zhide915/tamp/internal/router"
)

func (c *cli) path(parts ...string) string {
	return filepath.Join(append([]string{c.dir}, parts...)...)
}

func (c *cli) read(t *testing.T, parts ...string) string {
	t.Helper()
	body, err := os.ReadFile(c.path(parts...))
	if err != nil {
		t.Fatalf("cannot read %s: %v", c.path(parts...), err)
	}
	return string(body)
}

func (c *cli) exists(parts ...string) bool {
	_, err := os.Stat(c.path(parts...))
	return err == nil
}

// create fails the test on error, so callers are not also testing create.
func (c *cli) create(t *testing.T, name string, args ...string) {
	t.Helper()
	r := c.run(t, append([]string{"create", name, "--frappe", "version-15"}, args...)...)
	r.assertCode(t, exitcode.CodeOK)
}

// ops returns the engine's compose operations by method, excluding the
// global router's project (routerOps covers that).
func (c *cli) ops(method string) []enginetest.Op {
	return filterOps(c.engine.Ops, method, func(project string) bool {
		return project != router.Project
	})
}

// routerOps returns the compose operations on the global router, by method.
func (c *cli) routerOps(method string) []enginetest.Op {
	return filterOps(c.engine.Ops, method, func(project string) bool {
		return project == router.Project
	})
}

func filterOps(ops []enginetest.Op, method string, keep func(project string) bool) []enginetest.Op {
	var out []enginetest.Op
	for _, op := range ops {
		if op.Method == method && keep(op.Project.Name) {
			out = append(out, op)
		}
	}
	return out
}

// --- create ---------------------------------------------------------------

func TestCreateWritesTheEnvironmentsFiles(t *testing.T) {
	c := sandbox(t)

	c.create(t, "demo")

	for _, f := range []string{
		env.ConfigFile,
		env.ComposeFile,
		env.GitignoreFile,
		filepath.Join(env.StateDirName, env.CreateLogFile),
		filepath.Join(env.StateDirName, env.SecretsDirName, env.DBRootPasswordFile),
	} {
		if !c.exists("demo", f) {
			t.Errorf("create did not write %s", f)
		}
	}

	cfg := c.read(t, "demo", env.ConfigFile)
	for _, want := range []string{"schema = 1", "name = \"demo\"", "version = \"version-15\"", "profile = \"dev\""} {
		if !strings.Contains(cfg, want) {
			t.Errorf("tamp.toml does not contain %q:\n%s", want, cfg)
		}
	}
}

func TestCreateRejectsUnusableNames(t *testing.T) {
	cases := map[string]struct{ name, wantIn string }{
		"not a DNS label": {"Demo_Env", "not a valid environment name"},
		"a command word":  {"restore", "command word"},
	}
	for what, tc := range cases {
		t.Run(what, func(t *testing.T) {
			c := sandbox(t)

			r := c.run(t, "create", tc.name, "--frappe", "version-15")

			r.assertCode(t, exitcode.CodeFailed)
			r.assertStderrContains(t, "error:", tc.wantIn)
			if c.exists(tc.name) {
				t.Errorf("a rejected name still left %s behind", tc.name)
			}
		})
	}

	t.Run("a name already registered", func(t *testing.T) {
		c := sandbox(t)
		c.create(t, "demo")
		elsewhere := t.TempDir()

		r := c.run(t, "create", "demo", "--frappe", "version-15", "--dir", elsewhere)

		r.assertCode(t, exitcode.CodeFailed)
		r.assertStderrContains(t, "already registered", "tamp rm demo")
		// Rejected before building anything: not even create.log may remain.
		assertEmptyDir(t, elsewhere)
	})
}

func assertEmptyDir(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		t.Errorf("the failed create left %s behind in %s", e.Name(), dir)
	}
}

func TestCreateRefusesToBuildOnTopOfAnExistingDirectory(t *testing.T) {
	c := sandbox(t)
	if err := os.MkdirAll(c.path("demo"), 0o755); err != nil {
		t.Fatal(err)
	}

	r := c.run(t, "create", "demo", "--frappe", "version-15")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, "already exists")
}

func TestCreateNeedsDockerAndSaysSoWithExitFour(t *testing.T) {
	c := sandbox(t)
	c.engine = enginetest.Unavailable()

	r := c.run(t, "create", "demo", "--frappe", "version-15")

	r.assertCode(t, exitcode.CodeEngineUnavailable)
	assertEmptyDir(t, c.dir)
}

// --- environment resolution -----------------------------------------------

func TestNeitherRouteResolvingIsExitThree(t *testing.T) {
	c := sandbox(t)

	t.Run("no name, not inside an environment", func(t *testing.T) {
		r := c.run(t, "stop")
		r.assertCode(t, exitcode.CodeNotFound)
		r.assertStderrContains(t, "tamp list")
	})

	t.Run("a name nothing is registered under", func(t *testing.T) {
		r := c.run(t, "stop", "ghost")
		r.assertCode(t, exitcode.CodeNotFound)
		r.assertStderrContains(t, "\"ghost\"", "tamp list")
	})
}

// --- start / stop / restart -----------------------------------------------

func TestStopThenStartBringsTheEnvironmentBack(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")

	c.run(t, "stop", "demo").assertCode(t, exitcode.CodeOK)
	c.run(t, "list").assertStdoutContains(t, "stopped")

	c.run(t, "start", "demo").assertCode(t, exitcode.CodeOK)
	c.run(t, "list").assertStdoutContains(t, "running")
}

func TestStopNeverRemovesVolumes(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.engine.Ops = nil

	r := c.run(t, "stop", "demo")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "volumes are untouched")
	for _, op := range c.engine.Ops {
		if op.Removal == engine.RemoveVolumes {
			t.Errorf("stop asked the engine for %v", op)
		}
	}
}

// tamp.toml is the source of truth; hand-edits to generated files do not survive.
func TestStartRegeneratesTheComposeFile(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.run(t, "stop", "demo").assertCode(t, exitcode.CodeOK)

	handEdit := "# I edited this by hand\n"
	if err := os.WriteFile(c.path("demo", env.ComposeFile), []byte(handEdit), 0o644); err != nil {
		t.Fatal(err)
	}

	r := c.run(t, "start", "demo")

	r.assertCode(t, exitcode.CodeOK)
	if got := c.read(t, "demo", env.ComposeFile); strings.Contains(got, "I edited this by hand") {
		t.Error("start did not regenerate compose.yaml — the hand-edit survived")
	}
}

// Scripts run start defensively; it must succeed on a running environment.
func TestStartingARunningEnvironmentIsANoOp(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.engine.Ops = nil

	r := c.run(t, "start", "demo")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "already running")
	if ups := c.ops("ComposeUp"); len(ups) != 0 {
		t.Errorf("start touched the engine anyway: %v", ups)
	}
}

// The no-op covers the containers only; generated files are still rewritten.
func TestStartRegeneratesEvenWhenTheEnvironmentIsAlreadyRunning(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	if err := os.WriteFile(c.path("demo", env.ComposeFile), []byte("# I edited this by hand\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := c.run(t, "start", "demo")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "already running")
	if got := c.read(t, "demo", env.ComposeFile); strings.Contains(got, "I edited this by hand") {
		t.Error("the hand-edit survived a start on a running environment")
	}
}

func TestRestartStopsAndStartsAgain(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.engine.Ops = nil

	r := c.run(t, "restart", "demo")

	r.assertCode(t, exitcode.CodeOK)
	if got, want := len(c.ops("ComposeStop")), 1; got != want {
		t.Errorf("restart ran %d stops, want %d", got, want)
	}
	if got, want := len(c.ops("ComposeUp")), 1; got != want {
		t.Errorf("restart ran %d ups, want %d", got, want)
	}
}

// --- rm --------------------------------------------------------------------

// Exit 5: act on nothing, state exactly what --yes would do.
func TestRemoveWithoutYesExplainsItselfAndChangesNothing(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.engine.Ops = nil

	r := c.run(t, "rm", "demo")

	r.assertCode(t, exitcode.CodeConfirmationRequired)
	r.assertStdoutContains(t, "would destroy", "containers", "network", "registry")
	r.assertStdoutContains(t, "tamp rm demo --volumes --yes")
	if len(c.engine.Ops) != 0 {
		t.Errorf("rm without --yes touched the engine: %v", c.engine.Ops)
	}
	c.run(t, "list").assertStdoutContains(t, "demo")
}

func TestRemoveKeepsTheVolumesAndTheDirectory(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.engine.Ops = nil

	r := c.run(t, "rm", "demo", "--yes")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "demo removed", "survives", "never deletes your source code")

	downs := c.ops("ComposeDown")
	if len(downs) != 1 {
		t.Fatalf("tamp ran %d compose downs, want 1", len(downs))
	}
	if downs[0].Removal != engine.KeepVolumes {
		t.Error("rm destroyed the volumes without being asked to")
	}
	if !c.exists("demo", env.ConfigFile) {
		t.Error("rm deleted the environment directory")
	}
	c.run(t, "list").assertStdoutContains(t, "no environments yet")
}

func TestRemoveWithVolumesDestroysTheDataLayer(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.engine.Ops = nil

	r := c.run(t, "rm", "demo", "--volumes", "--yes")

	r.assertCode(t, exitcode.CodeOK)
	downs := c.ops("ComposeDown")
	if len(downs) != 1 || downs[0].Removal != engine.RemoveVolumes {
		t.Errorf("compose downs = %v, want one that removes volumes", downs)
	}
	if !c.exists("demo", env.ConfigFile) {
		t.Error("rm --volumes deleted the environment directory")
	}
}

// With sync off the code volume holds the only copy of the source, and tamp
// never deletes source.
func TestRemoveWithVolumesSparesTheSourceVolumeWhenSyncIsOff(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo", "--sync", "off")
	c.engine.Ops = nil

	preview := c.run(t, "rm", "demo", "--volumes")
	preview.assertCode(t, exitcode.CodeConfirmationRequired)
	preview.assertStdoutContains(t, "it would keep", "-code", "your source")

	r := c.run(t, "rm", "demo", "--volumes", "--yes")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "holds your source")
	downs := c.ops("ComposeDown")
	if len(downs) != 1 || downs[0].Removal != engine.KeepVolumes {
		t.Errorf("compose downs = %v, want one keeping volumes for tamp to remove selectively", downs)
	}
	if len(c.engine.Removed) != 3 {
		t.Errorf("removed volumes = %v, want the db, deps and sites volumes", c.engine.Removed)
	}
	for _, volume := range c.engine.Removed {
		if strings.Contains(volume, "-code") {
			t.Errorf("rm removed the code volume %s, which holds the source", volume)
		}
	}
}

// --- coexistence -----------------------------------------------------------

func TestTwoEnvironmentsShareNoResources(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.create(t, "other", "--frappe", "version-16")

	projects := map[string]bool{}
	for _, op := range c.engine.Ops {
		if op.Project.Name != router.Project {
			projects[op.Project.Name] = true
		}
	}
	if len(projects) != 2 {
		t.Fatalf("compose projects = %v, want two distinct ones", projects)
	}

	demo, other := c.read(t, "demo", env.ComposeFile), c.read(t, "other", env.ComposeFile)
	for _, shared := range sharedResources(demo, other) {
		t.Errorf("both environments declare the same resource: %s", shared)
	}

	r := c.run(t, "list")
	r.assertStdoutContains(t, "demo", "other", "version-15", "version-16", "10.11", "11.8")
}

// Toolchain and cache volumes are shared machine-wide on purpose, and must be
// declared external so one environment's teardown cannot destroy them.
func TestEveryEnvironmentSharesTheMachinesToolchainAndCaches(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.create(t, "other", "--frappe", "version-16")

	for _, dir := range []string{"demo", "other"} {
		compose := c.read(t, dir, env.ComposeFile)
		for _, volume := range env.SharedVolumes() {
			if !strings.Contains(compose, "name: "+volume) {
				t.Errorf("%s does not mount the shared %s volume", dir, volume)
			}
		}
		if strings.Count(compose, "external: true") != len(env.SharedVolumes()) {
			t.Errorf("%s does not declare every shared volume external:\n%s", dir, compose)
		}
	}

	// Compose refuses missing external volumes; tamp must create them first.
	created := map[string]bool{}
	for _, volume := range c.engine.Volumes {
		created[volume] = true
	}
	for _, volume := range env.SharedVolumes() {
		if !created[volume] {
			t.Errorf("tamp never created the shared %s volume", volume)
		}
	}
}

// sharedResources returns resource-name and published-port lines both compose
// files declare, ignoring the deliberately shared machine-wide volumes.
func sharedResources(a, b string) []string {
	machineWide := map[string]bool{}
	for _, volume := range env.SharedVolumes() {
		machineWide["name: "+volume] = true
	}

	declared := func(s string) map[string]bool {
		out := map[string]bool{}
		for _, line := range strings.Split(s, "\n") {
			line = strings.TrimSpace(line)
			if machineWide[line] {
				continue
			}
			if strings.HasPrefix(line, "name: tamp-") || strings.HasPrefix(line, "- \"127.0.0.1:") {
				out[line] = true
			}
		}
		return out
	}

	first := declared(a)
	var shared []string
	for line := range declared(b) {
		if first[line] {
			shared = append(shared, line)
		}
	}
	return shared
}

// --- locking ---------------------------------------------------------------

// Concurrent registry mutations would lose a write; the loser must fail loudly.
func TestASecondRegistryMutationIsRefusedWhileTheFirstHoldsTheLock(t *testing.T) {
	c := sandbox(t)
	home, err := env.Home()
	if err != nil {
		t.Fatal(err)
	}
	held, err := env.AcquireLock(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Release() }()

	r := c.run(t, "create", "demo", "--frappe", "version-15")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, "another tamp command is running")
}

// --- list -------------------------------------------------------------------

// The registry is an index; entries for deleted directories must not persist.
func TestListPrunesEntriesWhoseDirectoryIsGone(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.create(t, "kept")
	if err := os.RemoveAll(c.path("demo")); err != nil {
		t.Fatal(err)
	}

	r := c.run(t, "list")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStderrContains(t, "pruned", "demo")
	r.assertStdoutContains(t, "kept")
	if strings.Contains(r.stdout, "demo") {
		t.Errorf("list still shows the pruned environment:\n%s", r.stdout)
	}

	again := c.run(t, "list")
	if strings.Contains(again.stderr, "pruned") {
		t.Errorf("the entry was not actually removed from the registry:\n%s", again.stderr)
	}
}

func TestListReportsWhatEachEnvironmentIs(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")

	r := c.run(t, "ls")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "NAME", "STATE", "FRAPPE", "PYTHON", "NODE", "MARIADB", "SITES", "MAIL", "SYNC", "PATH")
	r.assertStdoutContains(t, "demo", "running", "version-15", "3.11", "18", "10.11", "none yet", c.path("demo"))
}

func TestListShowsEachSiteURLAndTheSyncMode(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo", "--sync", "off")
	c.siteNew(t, "demo", "shop.localhost")
	// Stopped on purpose: the sites must come from the registry cache, not
	// the bench.
	c.run(t, "stop", "demo").assertCode(t, exitcode.CodeOK)

	r := c.run(t, "list")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "http://shop.localhost", "off")
}

// list reads tamp's own registry; Docker down costs only the state column.
func TestListStillWorksWithDockerDown(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.engine = enginetest.Unavailable()

	r := c.run(t, "list")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "demo", "unknown")
	r.assertStderrContains(t, "Docker is unreachable")
}
