package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/engine/enginetest"
	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/exitcode"
)

// path names a file inside the sandbox's working directory.
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

// create makes an environment and fails the test if it did not work, so that
// tests about start, stop and rm are not also tests about create.
func (c *cli) create(t *testing.T, name string, args ...string) {
	t.Helper()
	r := c.run(t, append([]string{"create", name, "--frappe", "version-15"}, args...)...)
	r.assertCode(t, exitcode.CodeOK)
}

// ops returns the compose operations tamp asked the engine for, by method.
func (c *cli) ops(method string) []enginetest.Op {
	var out []enginetest.Op
	for _, op := range c.engine.Ops {
		if op.Method == method {
			out = append(out, op)
		}
	}
	return out
}

// --- create ---------------------------------------------------------------

func TestCreateProvisionsTheEnvironmentAndStartsIt(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "create", "demo", "--frappe", "version-15")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "demo ready", "no sites yet")
	// The version matrix is resolved for the user and shown, because "which
	// Python is in there" is the question tamp exists to stop them asking.
	r.assertStdoutContains(t, "python 3.11", "node 18", "mariadb 10.11")

	if ups := c.ops("ComposeUp"); len(ups) != 1 {
		t.Fatalf("tamp ran %d compose ups, want 1", len(ups))
	}
}

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

// The ways a name can be unusable, each exit 1 with something to do.
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
		// A create rejected before it built anything must leave nothing —
		// not even the create.log, whose directory would be the whole trace
		// of an environment that was never made.
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

// version-14 is out of scope, and the error has to name what tamp does
// support rather than leave the user guessing.
func TestCreateRejectsAnUnsupportedFrappeVersion(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "create", "demo", "--frappe", "version-14")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, "not supported", "version-15", "version-16", "develop")
}

func TestCreateNeedsDockerAndSaysSoWithExitFour(t *testing.T) {
	c := sandbox(t)
	c.engine = enginetest.Unavailable()

	r := c.run(t, "create", "demo", "--frappe", "version-15")

	r.assertCode(t, exitcode.CodeEngineUnavailable)
	assertEmptyDir(t, c.dir)
}

// --- environment resolution -----------------------------------------------

func TestTheEnvironmentArgumentIsOptionalInsideAnEnvironment(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	deep := c.path("demo", "apps", "erpnext")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(deep)

	r := c.run(t, "stop")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "demo stopped")
}

func TestAnExplicitNameResolvesFromAnywhere(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	t.Chdir(t.TempDir())

	r := c.run(t, "stop", "demo")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "demo stopped")
}

// Both routes failing is exit 3 — the environment does not exist.
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

// Stopping an environment must never be a way to lose data, so no compose
// operation on the stop path may ask for volume removal.
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

// tamp.toml is the source of truth, and every start proves it.
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

// Agents and scripts run start defensively; it must not fail on an environment
// that is already up.
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

// "no-op" is about the containers, not about tamp.toml's authority: the rule is
// the generated files are rewritten on every start, and an environment that
// happens to be up is not an exception.
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

// The exit-5 contract: never act, and say exactly what --yes would do.
func TestRemoveWithoutYesExplainsItselfAndChangesNothing(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.engine.Ops = nil

	r := c.run(t, "rm", "demo")

	r.assertCode(t, exitcode.CodeConfirmationRequired)
	r.assertStdoutContains(t, "would destroy", "containers", "network", "registry")
	// The full-deletion recipe belongs here, where it is still runnable.
	r.assertStdoutContains(t, "tamp rm demo --volumes --yes")
	if len(c.engine.Ops) != 0 {
		t.Errorf("rm without --yes touched the engine: %v", c.engine.Ops)
	}
	c.run(t, "list").assertStdoutContains(t, "demo")
}

// Volumes survive by default, and the directory always survives.
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

// --- coexistence -----------------------------------------------------------

// Two environments on one machine share nothing: not a project name, not
// a network, not a volume, and not the one host port either publishes.
func TestTwoEnvironmentsShareNoResources(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.create(t, "other", "--frappe", "version-16")

	projects := map[string]bool{}
	for _, op := range c.engine.Ops {
		projects[op.Project.Name] = true
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

// sharedResources reports the resource-name and published-port lines two
// generated compose files have in common — the declarations that must differ.
func sharedResources(a, b string) []string {
	declared := func(s string) map[string]bool {
		out := map[string]bool{}
		for _, line := range strings.Split(s, "\n") {
			line = strings.TrimSpace(line)
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

// Two tamp commands mutating machine-global state at once would otherwise
// lose one of the writes; the loser has to say so rather than half-succeed.
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

// --- rollback --------------------------------------------------------------

// A create that fails partway must leave nothing running, nothing registered,
// and enough on disk to find out why.
func TestAFailedCreateRollsBackAndLeavesTheLog(t *testing.T) {
	c := sandbox(t)
	c.engine.UpErr = errors.New("mariadb never became healthy")

	r := c.run(t, "create", "demo", "--frappe", "version-15")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, "rolling back", "mariadb never became healthy")

	downs := c.ops("ComposeDown")
	if len(downs) != 1 || downs[0].Removal != engine.RemoveVolumes {
		t.Errorf("rollback compose downs = %v, want one that removes the volumes it made", downs)
	}

	// The registry must not remember an environment that was never built.
	c.run(t, "list").assertStdoutContains(t, "no environments yet")

	if !c.exists("demo", env.ConfigFile) {
		t.Errorf("rollback deleted %s — it is the user's directory", env.ConfigFile)
	}
	log := c.read(t, "demo", env.StateDirName, env.CreateLogFile)
	for _, want := range []string{"checking Docker", "starting containers", "ComposeUp"} {
		if !strings.Contains(log, want) {
			t.Errorf("create.log does not record %q:\n%s", want, log)
		}
	}
}

// --- list -------------------------------------------------------------------

// The registry is an index, not the truth: a directory the user deleted or
// moved would otherwise stay in it forever, breaking every command that names
// the environment.
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

	// Pruned means gone: a second list must not report it again.
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
	r.assertStdoutContains(t, "NAME", "STATE", "FRAPPE", "PYTHON", "NODE", "MARIADB", "PATH")
	r.assertStdoutContains(t, "demo", "running", "version-15", "3.11", "18", "10.11", c.path("demo"))
}

// list is mostly a report on tamp's own registry: a stopped Docker costs the
// state column and nothing else.
func TestListStillWorksWithDockerDown(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.engine = enginetest.Unavailable()

	r := c.run(t, "list")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "demo", "unknown")
	r.assertStderrContains(t, "Docker is unreachable")
}
