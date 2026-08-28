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

// The sync layer is what makes an agent on the host able to edit a bench
// running in a container. Every test here names the mode explicitly rather
// than leaving it to auto, because auto means different things on different
// machines and these must say the same thing on all of them.

// session is the Mutagen session name tamp gives an environment: its own
// resource name, so a session is traceable to what owns it.
func (c *cli) session(t *testing.T, name string) string {
	t.Helper()
	res, err := env.NewResources(env.Name(name), c.path(name))
	if err != nil {
		t.Fatalf("cannot derive %s's resource names: %v", name, err)
	}
	return res.Project()
}

// hostOS is the platform these tests are running on, which decides what the
// sync check has to say.
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
	// Alpha is the host, which is what makes the host win a conflict — it is
	// the side a person or an agent is editing.
	if made.Alpha != c.path("demo", syncer.AppsDirName) {
		t.Errorf("session alpha = %q, want the host's apps directory", made.Alpha)
	}
	if !strings.HasPrefix(made.Beta, "docker://") || !strings.HasSuffix(made.Beta, "/apps") {
		t.Errorf("session beta = %q, want the bench's apps directory in its container", made.Beta)
	}
	// The directory has to be there before anything mirrors into it.
	if !c.exists("demo", syncer.AppsDirName) {
		t.Error("tamp started a session with nowhere on the host to sync to")
	}
}

// The Linux answer, and the documented fallback everywhere else: the container
// reads the host's filesystem directly, and there is no session at all.
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

// A machine that cannot download Mutagen still gets a working environment.
// It has to be told, though: the fallback is slow and nothing hot-reloads.
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

// A session survives a stop rather than being torn down and rebuilt, which is
// what stops every start resynchronising the whole tree.
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

// The far end of a session is a container rm is about to remove, so the
// session goes with it rather than being left reporting a bench that is gone.
func TestRemoveTerminatesTheSession(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo", "--sync", "mutagen")

	c.run(t, "rm", "demo", "--yes").assertCode(t, exitcode.CodeOK)

	if _, held := c.sync.Paused(c.session(t, "demo")); held {
		t.Error("tamp removed the environment and left its sync session behind")
	}
}

// Stopping a bind-mounted environment must not be the thing that downloads a
// Mutagen this machine has never needed.
func TestStopNeverReachesForMutagenInBindMode(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo", "--sync", "bind")

	c.run(t, "stop", "demo").assertCode(t, exitcode.CodeOK)

	if len(c.sync.Calls) != 0 {
		t.Errorf("tamp went to Mutagen stopping a bind-mounted environment: %v", c.sync.Calls)
	}
}

// Where the environment goes is the user's call, so this is a warning. It is
// still worth saying: two synchronizers on one directory undo each other.
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

// doctor answers "will syncing work here", and on a platform that needs no
// Mutagen the honest answer is that there is nothing to have.
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
