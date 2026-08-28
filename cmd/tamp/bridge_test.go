package main

import (
	"os"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/engine/enginetest"
	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/exitcode"
)

// The source preflight: every fetch-bound source is probed in seconds, right
// after the containers start, before anything expensive.

const privateApp = "https://github.com/myorg/private"

func execIndex(execs []enginetest.Exec, fragment string) int {
	for i, e := range execs {
		if strings.Contains(e.Line(), fragment) {
			return i
		}
	}
	return -1
}

func TestCreateFailsInThePreflightWhenAnAppSourceDoesNotExist(t *testing.T) {
	c := sandbox(t)
	const typo = "https://github.com/myorg/typo"
	c.engine.MissingRepos = map[string]bool{typo: true}

	r := c.run(t, "create", "demo", "--frappe", "version-15", "--apps", typo+":version-15")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, typo, "check the URL")
	if c.engine.Ran("bench init") {
		t.Error("the bench was initialized for a source that does not exist")
	}
	if log := c.read(t, "demo", env.StateDirName, env.CreateLogFile); !strings.Contains(log, "checking that every app source answers") {
		t.Errorf("create.log does not show the preflight step:\n%s", log)
	}
}

func TestThePreflightRunsBeforeTheBenchIsInitialized(t *testing.T) {
	c := sandbox(t)

	c.create(t, "demo", "--apps", "erpnext:version-15")

	probe := execIndex(c.engine.Execs, "git ls-remote")
	init := execIndex(c.engine.Execs, "bench init")
	if probe < 0 || init < 0 || probe > init {
		t.Errorf("ls-remote at exec %d, bench init at %d — the preflight must come first", probe, init)
	}
}

// The honest error, replacing "the output above says why": the repo looks
// private, and the container cannot reach the host's credentials.
func TestCreateWithAPrivateSourceFailsBeforeTheBenchIsBuilt(t *testing.T) {
	c := sandbox(t)
	c.engine.PrivateRepos = map[string]string{privateApp: ""}

	r := c.run(t, "create", "demo", "--frappe", "version-15", "--apps", privateApp+":version-15")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, privateApp, "looks private")
	if c.engine.Ran("bench init") {
		t.Error("the bench was initialized for a fetch that could never work")
	}
}

func TestInitPreflightsAppSourcesLikeCreateDoes(t *testing.T) {
	c := sandbox(t)
	const typo = "https://github.com/myorg/typo"
	c.engine.MissingRepos = map[string]bool{typo: true}

	r := c.inside(t, c.path("demo"), "init", "--frappe", "version-15", "--apps", typo+":version-15")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, typo)
	if c.engine.Ran("bench init") {
		t.Error("init initialized the bench for a source that does not exist")
	}
}

// A tamp.toml from before this check can carry an ssh source the flag parser
// never sees again; the preflight must hand back the https rewrite, not a
// reachability guess.
func TestAnSSHSourceInAnOldTampTomlGetsTheHTTPSRewriteAtPreflight(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo", "--apps", "https://github.com/myorg/app:main")
	c.run(t, "rm", "demo", "--volumes", "--yes").assertCode(t, exitcode.CodeOK)

	// What an older tamp would have recorded.
	config := strings.Replace(c.read(t, "demo", "tamp.toml"),
		"https://github.com/myorg/app", "git@github.com:myorg/app.git", 1)
	if err := os.WriteFile(c.path("demo", "tamp.toml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	c.leaveSource(t, "demo")
	before := len(c.engine.Execs)

	r := c.inside(t, c.path("demo"), "init")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, "tamp.toml", "https://github.com/myorg/app.git")
	if execIndex(c.engine.Execs[before:], "bench init") >= 0 {
		t.Error("the adoption initialized the bench for a source tamp cannot fetch")
	}
}
