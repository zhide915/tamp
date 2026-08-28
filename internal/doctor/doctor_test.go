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

func run(t *testing.T, e engine.Engine) doctor.Report {
	t.Helper()
	// Fresh home and an installed Mutagen: these tests are about the engine
	// and the router, not the developer's machine.
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

// Unreadable tamp state must not be filed under "router not started yet" —
// that warning's fix cannot work here.
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

func TestExitCodeComesFromTheFailingCheck(t *testing.T) {
	e := enginetest.Running()
	e.PingErr = exitcode.New(exitcode.CodeFailed, "something else went wrong", "retry")

	if got := run(t, e).ExitCode(); got != exitcode.CodeFailed {
		t.Errorf("ExitCode() = %d, want %d", got, exitcode.CodeFailed)
	}
}
