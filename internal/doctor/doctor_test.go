package doctor_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/zhide915/tamp/internal/doctor"
	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/engine/enginetest"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/router"
	"github.com/zhide915/tamp/internal/syncer/synctest"
)

// doctor's contract is honesty: every check says pass, warn or fail, a failing
// check always carries the fix, and tamp exits non-zero exactly when
// something failed. These tests hold that against a recording fake, which also
// pins what tamp asked the engine to do.

func run(t *testing.T, e engine.Engine) doctor.Report {
	t.Helper()
	// A tamp home the router has never started in: no check may depend on
	// tamp having run before.
	// A machine that already has the pinned Mutagen: what this file is about
	// is the engine and the router, and a sync check that varied with the
	// developer's own machine would make every one of these tests flaky.
	return doctor.Run(context.Background(), e, router.New(t.TempDir(), e), synctest.Installed())
}

func find(t *testing.T, r doctor.Report, name string) doctor.Check {
	t.Helper()
	for _, c := range r.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no %q check in the report; got %v", name, names(r))
	return doctor.Check{}
}

func names(r doctor.Report) []string {
	out := make([]string, 0, len(r.Checks))
	for _, c := range r.Checks {
		out = append(out, c.Name)
	}
	return out
}

// tamp's own state being unreadable is tamp's fault, and doctor must not
// file it under the same warning as "you have not started a router yet" — the
// user would be told to run a command that cannot work.
func TestBrokenRouterStateIsAFailureRatherThanAWarning(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, router.DirName), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, router.DirName, "router.json"),
		[]byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := doctor.Run(context.Background(), enginetest.Running(), router.New(home, enginetest.Running()), synctest.Installed())

	c := find(t, r, "Router")
	if c.Status != doctor.Fail {
		t.Errorf("Router check = %v, want Fail", c.Status)
	}
	if c.Fix == "" {
		t.Error("the Router failure carries no fix")
	}
}

// tamp's exit codes are a public contract, so doctor reports the code of the
// thing that is broken rather than a blanket "something failed".
func TestExitCodeComesFromTheFailingCheck(t *testing.T) {
	e := enginetest.Running()
	e.PingErr = exitcode.New(exitcode.CodeFailed, "something else went wrong", "retry")

	if got := run(t, e).ExitCode(); got != exitcode.CodeFailed {
		t.Errorf("ExitCode() = %d, want %d", got, exitcode.CodeFailed)
	}
}
