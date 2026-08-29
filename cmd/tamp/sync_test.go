package main

import (
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/syncer"
	"github.com/zhide915/tamp/internal/syncer/synctest"
)

// Every test names the sync mode explicitly: auto differs per machine, and
// these must not.

// session is the Mutagen session name tamp derives for an environment.
func (c *cli) session(t *testing.T, name string) string {
	t.Helper()
	res, err := env.NewResources(env.Name(name), c.path(name))
	if err != nil {
		t.Fatalf("cannot derive %s's resource names: %v", name, err)
	}
	return res.Project()
}

func hostOS() string { return runtime.GOOS }

func TestCreateStartsASyncSessionBetweenTheHostAndTheBench(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "create", "demo", "--frappe", "version-15", "--sync", "mutagen")

	r.assertCode(t, exitcode.CodeOK)
	if len(c.sync.Created) != 1 {
		t.Fatalf("tamp created %d sync sessions, want 1", len(c.sync.Created))
	}

	made := c.sync.Created[0]
	if made.Name != c.session(t, "demo") {
		t.Errorf("session name = %q, want %q", made.Name, c.session(t, "demo"))
	}
	// Alpha is the host side — the side being edited, and the conflict winner.
	if made.Alpha != c.path("demo", syncer.AppsDirName) {
		t.Errorf("session alpha = %q, want the host's apps directory", made.Alpha)
	}
	if !strings.HasPrefix(made.Beta, "docker://") || !strings.HasSuffix(made.Beta, "/apps") {
		t.Errorf("session beta = %q, want the bench's apps directory in its container", made.Beta)
	}
	// The host directory must exist before anything mirrors into it.
	if !c.exists("demo", syncer.AppsDirName) {
		t.Error("tamp started a session with nowhere on the host to sync to")
	}
}

// Bind is the Linux answer and the documented fallback elsewhere.
func TestBindModeMountsTheHostSourceAndRunsNoSession(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "create", "demo", "--frappe", "version-15", "--sync", "bind")

	r.assertCode(t, exitcode.CodeOK)
	if compose := c.read(t, "demo", env.ComposeFile); !strings.Contains(compose, "./apps:") {
		t.Errorf("compose does not bind the host's apps directory:\n%s", compose)
	}
	if !c.exists("demo", syncer.AppsDirName) {
		t.Error("tamp left Docker to create the bind mount's host directory, which it would own as root")
	}
	if len(c.sync.Calls) != 0 {
		t.Errorf("tamp went to Mutagen in bind mode: %v", c.sync.Calls)
	}
}

// The fallback works but is slow and does not hot-reload, so it is announced.
func TestABlockedDownloadFallsBackToABindMountAndSaysSo(t *testing.T) {
	c := sandbox(t)
	c.sync = synctest.Blocked()

	r := c.run(t, "create", "demo", "--frappe", "version-15", "--sync", "mutagen")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStderrContains(t, "bind mount", "hot-reload")
	if compose := c.read(t, "demo", env.ComposeFile); !strings.Contains(compose, "./apps:") {
		t.Errorf("tamp warned about the fallback and did not fall back:\n%s", compose)
	}
	if len(c.sync.Created) != 0 {
		t.Error("tamp created a session with a Mutagen it could not get")
	}
}

// Pause, not teardown: a rebuilt session resynchronises the whole tree.
func TestStopPausesTheSessionAndStartResumesIt(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo", "--sync", "mutagen")

	c.run(t, "stop", "demo").assertCode(t, exitcode.CodeOK)
	if paused, held := c.sync.Paused(c.session(t, "demo")); !held || !paused {
		t.Fatalf("after stop: held=%v paused=%v, want a paused session", held, paused)
	}

	c.run(t, "start", "demo").assertCode(t, exitcode.CodeOK)
	if paused, held := c.sync.Paused(c.session(t, "demo")); !held || paused {
		t.Fatalf("after start: held=%v paused=%v, want a running session", held, paused)
	}
	if len(c.sync.Created) != 1 {
		t.Errorf("tamp created %d sessions, want the one from create resumed", len(c.sync.Created))
	}
}

// The session's far end is a container rm removes; the session goes with it.
func TestRemoveTerminatesTheSession(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo", "--sync", "mutagen")

	c.run(t, "rm", "demo", "--yes").assertCode(t, exitcode.CodeOK)

	if _, held := c.sync.Paused(c.session(t, "demo")); held {
		t.Error("tamp removed the environment and left its sync session behind")
	}
}

// Stop must not download a Mutagen this machine has never needed.
func TestStopNeverReachesForMutagenInBindMode(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo", "--sync", "bind")

	c.run(t, "stop", "demo").assertCode(t, exitcode.CodeOK)

	if len(c.sync.Calls) != 0 {
		t.Errorf("tamp went to Mutagen stopping a bind-mounted environment: %v", c.sync.Calls)
	}
}

// Two synchronizers on one directory undo each other; the location is still
// the user's call, so warn rather than refuse.
func TestCreateWarnsAboutACloudSyncedDirectory(t *testing.T) {
	c := sandbox(t)
	parent := c.path("OneDrive")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}

	r := c.run(t, "create", "demo", "--frappe", "version-15", "--sync", "bind", "--dir", parent)

	r.assertCode(t, exitcode.CodeOK)
	r.assertStderrContains(t, "OneDrive")
}

func TestCreateWarnsAboutASpaceInThePath(t *testing.T) {
	c := sandbox(t)
	parent := c.path("My Projects")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}

	r := c.run(t, "create", "demo", "--frappe", "version-15", "--sync", "bind", "--dir", parent)

	r.assertCode(t, exitcode.CodeOK)
	r.assertStderrContains(t, "space in it")
}

// --- the sync subcommands ---------------------------------------------------

func TestSyncStatusReportsTheSessionsEndpointsAndMutagensOwnAccount(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo", "--sync", "mutagen")

	r := c.run(t, "sync", "status", "demo")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t,
		c.session(t, "demo"),
		c.path("demo", syncer.AppsDirName), // the host endpoint
		"docker://",                        // the container endpoint
		syncer.Ignores[0],
		"never forced",
		// Quoted from Mutagen rather than invented by tamp.
		"Conflicts:",
	)
}

// Every subcommand answers on Linux too: having no session is a mode.
func TestEverySyncSubcommandReportsTheBindModeAndExitsZero(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo", "--sync", "bind")

	for _, sub := range []string{"status", "flush", "reset"} {
		r := c.run(t, "sync", sub, "demo")

		r.assertCode(t, exitcode.CodeOK)
		r.assertStdoutContains(t, "mode: bind — sync not applicable")
	}
	if len(c.sync.Calls) != 0 {
		t.Errorf("tamp went to Mutagen for a bind-mounted environment: %v", c.sync.Calls)
	}
}

func TestSyncFlushForcesAPassAndRecordsWhen(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo", "--sync", "mutagen")

	r := c.run(t, "sync", "flush", "demo")

	r.assertCode(t, exitcode.CodeOK)
	if got := c.sync.Flushed; len(got) != 1 || got[0] != c.session(t, "demo") {
		t.Fatalf("tamp flushed %v, want the environment's own session", got)
	}
	if status := c.run(t, "sync", "status", "demo"); strings.Contains(status.stdout, "never forced") {
		t.Errorf("the status still reports no forced flush:\n%s", status.stdout)
	}
}

// The documented recovery after a large host-side change: the old session is
// past settling, so it goes and a fresh one mirrors the tree again.
func TestSyncResetTerminatesTheSessionAndCreatesItAgain(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo", "--sync", "mutagen")

	r := c.run(t, "sync", "reset", "demo")

	r.assertCode(t, exitcode.CodeOK)
	if len(c.sync.Created) != 2 {
		t.Fatalf("tamp created %d sessions, want the create's and the reset's", len(c.sync.Created))
	}
	if paused, held := c.sync.Paused(c.session(t, "demo")); !held || paused {
		t.Errorf("after reset: held=%v paused=%v, want a running session", held, paused)
	}
	// A session recreated against a stale endpoint would sync nothing.
	if made := c.sync.Created[1]; made.Alpha != c.path("demo", syncer.AppsDirName) {
		t.Errorf("the new session's alpha = %q, want the host's apps directory", made.Alpha)
	}
}

// Stopping an environment pauses its session rather than forgetting it, so
// Mutagen still holds one — and a flush to a container that is down is not a
// flush.
func TestSyncFlushRefusesAStoppedEnvironment(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo", "--sync", "mutagen")
	c.run(t, "stop", "demo").assertCode(t, exitcode.CodeOK)

	r := c.run(t, "sync", "flush", "demo")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, "tamp start demo")
	if len(c.sync.Flushed) != 0 {
		t.Errorf("tamp flushed %v against a stopped environment", c.sync.Flushed)
	}
}

func TestSyncFlushRefusesWhenMutagenHoldsNoSession(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo", "--sync", "mutagen")
	// As the user's own 'mutagen sync terminate' would leave it.
	if err := c.sync.Terminate(t.Context(), c.session(t, "demo")); err != nil {
		t.Fatal(err)
	}

	r := c.run(t, "sync", "flush", "demo")

	r.assertCode(t, exitcode.CodeNotFound)
	r.assertStderrContains(t, "tamp start demo")
}

// The daemon outlives every session, which is the whole reason to have this.
func TestSyncStopStopsTheMutagenDaemon(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "sync", "stop")

	r.assertCode(t, exitcode.CodeOK)
	if !c.sync.DaemonStopped {
		t.Error("tamp did not stop the daemon")
	}
}

// On a platform needing no Mutagen, the honest answer is the bind mount.
func TestDoctorReportsTheStateOfTheManagedMutagen(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "doctor")

	r.assertStdoutContains(t, "Sync")
	if syncer.Resolve(syncer.ModeAuto, hostOS()) == syncer.UseMutagen {
		r.assertStdoutContains(t, syncer.Version)
	} else {
		r.assertStdoutContains(t, "bind mount")
	}
}
