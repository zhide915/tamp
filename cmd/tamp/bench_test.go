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
	// The bench container idles until a bench exists; a restart picks it up.
	if len(c.ops("ComposeRestart")) != 1 {
		t.Errorf("create restarted the bench service %d times, want 1", len(c.ops("ComposeRestart")))
	}
	if !c.engine.Ran("curl") {
		t.Error("create returned without waiting for the web server to answer")
	}
}

func TestCreateStreamsItsStepsAndNamesTheToolchain(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "create", "demo", "--frappe", "version-16")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t,
		"[1/10]", "[10/10]",
		"python 3.14 · node 24 · mariadb 11.8",
		"initializing the bench",
	)
}

func TestCreateRejectsAFrappeVersionTampDoesNotCarry(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "create", "demo", "--frappe", "version-14")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, "version-14 is not supported", "version-15", "version-16", "develop")
	if c.exists("demo") {
		t.Error("a rejected version still left a directory behind")
	}
}

// The credential lives on disk after create; reprinting on every start would
// scatter it through scrollback.
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

// A failed create removes everything outside the directory; the directory
// stays as the trace of what happened.
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

	for _, file := range []string{env.ConfigFile, filepath.Join(env.StateDirName, env.CreateLogFile)} {
		if !c.exists("demo", file) {
			t.Errorf("rollback removed %s", file)
		}
	}
	if log := c.read(t, "demo", env.StateDirName, env.CreateLogFile); !strings.Contains(log, "initializing the bench") {
		t.Errorf("the create log does not show how far tamp got:\n%s", log)
	}

	// The name must be free again for a retry.
	c.engine.ExecFails = nil
	c.run(t, "create", "demo", "--dir", t.TempDir(), "--frappe", "version-15").assertCode(t, exitcode.CodeOK)
}

// The bench survives in the volumes; start only revives the containers.
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
