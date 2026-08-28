package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/frappe"
	"github.com/zhide915/tamp/internal/toolchain"
)

// A create that returns has produced a bench that is serving: the toolchain is
// in place, bench init has run against it, tamp's own process file and site
// config are on the bench, and the web server has answered.
func TestCreateEndsWithABenchThatIsServing(t *testing.T) {
	c := sandbox(t)

	c.create(t, "demo")

	if !c.engine.Ran("uv python install") {
		t.Error("create never provisioned a Python")
	}
	if !c.engine.Ran("bench init") {
		t.Error("create never initialized a bench")
	}
	for _, path := range []string{toolchain.EnvScript, frappe.ProcfilePath, frappe.CommonSiteConfigPath} {
		if _, ok := c.engine.Wrote(path); !ok {
			t.Errorf("create did not write %s; it wrote %v", path, c.engine.Written())
		}
	}
	// The bench container is idle until there is a bench to run, so tamp has
	// to ask it again once there is.
	if len(c.ops("ComposeRestart")) != 1 {
		t.Errorf("create restarted the bench service %d times, want 1", len(c.ops("ComposeRestart")))
	}
	if !c.engine.Ran("curl") {
		t.Error("create returned without waiting for the web server to answer")
	}
}

// The numbered steps are what makes a create that takes minutes legible, and
// the toolchain tamp resolved is the answer to "which Python is in there".
func TestCreateStreamsItsStepsAndNamesTheToolchain(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "create", "demo", "--frappe", "version-16")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t,
		"[1/9]", "[9/9]",
		"python 3.14 · node 24 · mariadb 11.8",
		"initializing the bench",
	)
}

// The version matrix is tamp's answer to a question the user should not have
// to ask, and a release tamp does not carry has to say what it does.
func TestCreateRejectsAFrappeVersionTampDoesNotCarry(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "create", "demo", "--frappe", "version-14")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, "version-14 is not supported", "version-15", "version-16", "develop")
	if c.exists("demo") {
		t.Error("a rejected version still left a directory behind")
	}
}

// The database credential is generated per environment and shown once, at the
// moment it comes into existence. It lives on disk from then on; reprinting it
// on every start would scatter it through scrollback nobody asked to keep.
func TestCreatePrintsTheDatabaseRootPasswordExactlyOnce(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "create", "demo", "--frappe", "version-15")
	r.assertCode(t, exitcode.CodeOK)

	password := strings.TrimSpace(c.read(t, "demo",
		filepath.Join(env.StateDirName, env.SecretsDirName, env.DBRootPasswordFile)))
	if password == "" {
		t.Fatal("create generated no database password")
	}
	if got := strings.Count(r.stdout, password); got != 1 {
		t.Errorf("create printed the database password %d times, want 1\nstdout: %s", got, r.stdout)
	}
	r.assertStdoutContains(t, "database root password")

	restart := c.run(t, "restart", "demo")
	restart.assertCode(t, exitcode.CodeOK)
	if strings.Contains(restart.stdout, password) {
		t.Errorf("restart printed the database password again:\n%s", restart.stdout)
	}
}

// A create that fails while building the bench is a failed create, not a
// half-made environment: everything outside the directory goes, and the
// directory stays with enough in it to see what happened.
func TestAFailureWhileBuildingTheBenchRollsBackTheEnvironment(t *testing.T) {
	c := sandbox(t)
	c.engine.ExecFails = map[string]error{
		"bench init": errors.New("frappe branch version-15 could not be cloned"),
	}

	r := c.run(t, "create", "demo", "--frappe", "version-15")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, "rolling back demo")

	downs := c.ops("ComposeDown")
	if len(downs) != 1 {
		t.Fatalf("rollback ran %d compose downs, want 1", len(downs))
	}
	if downs[0].Removal != engine.RemoveVolumes {
		t.Error("rollback left the failed environment's volumes behind")
	}

	// The directory is the one thing tamp never destroys — the user may have
	// put something in it — and what is left in it says how far tamp got.
	for _, file := range []string{env.ConfigFile, filepath.Join(env.StateDirName, env.CreateLogFile)} {
		if !c.exists("demo", file) {
			t.Errorf("rollback removed %s", file)
		}
	}
	if log := c.read(t, "demo", env.StateDirName, env.CreateLogFile); !strings.Contains(log, "initializing the bench") {
		t.Errorf("the create log does not show how far tamp got:\n%s", log)
	}

	// The name has to be free again, or the user who fixes what broke cannot
	// simply run the same command.
	c.engine.ExecFails = nil
	c.run(t, "create", "demo", "--dir", t.TempDir(), "--frappe", "version-15").assertCode(t, exitcode.CodeOK)
}

// Starting a stopped environment revives its processes without rebuilding
// anything: the bench is in the volumes, and the container works out at boot
// that it has one to run.
func TestStartingAnEnvironmentDoesNotInitializeItAgain(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	built := len(c.engine.Execs)

	c.run(t, "stop", "demo").assertCode(t, exitcode.CodeOK)
	c.run(t, "start", "demo").assertCode(t, exitcode.CodeOK)

	for _, ran := range c.engine.Execs[built:] {
		if strings.Contains(ran.Line(), "bench init") {
			t.Errorf("start initialized the bench again:\n%s", ran.Line())
		}
	}
}
