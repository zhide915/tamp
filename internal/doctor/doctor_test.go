package doctor_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
	return doctor.Run(context.Background(), e, router.New(t.TempDir(), e), synctest.Installed(), doctor.Input{})
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

	r := doctor.Run(context.Background(), enginetest.Running(), router.New(home, enginetest.Running()), synctest.Installed(), doctor.Input{})

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

// --- the registry and the hosts block --------------------------------------

// doctor diagnoses; it never stops at the first problem. An unreadable
// registry used to abort the whole report.
func TestAnUnreadableRegistryIsAFailingCheckRatherThanAnAbortedReport(t *testing.T) {
	broken := exitcode.New(exitcode.CodeFailed, "registry.json is not valid JSON", "repair or delete it")

	r := doctor.Run(context.Background(), enginetest.Running(),
		router.New(t.TempDir(), enginetest.Running()), synctest.Installed(),
		doctor.Input{RegistryErr: broken})

	c := find(t, r, "Registry")
	if c.Status != doctor.Fail {
		t.Errorf("Registry check = %v, want Fail", c.Status)
	}
	if c.Fix == "" {
		t.Error("the Registry failure carries no fix")
	}
	// The point of the change: find fails the test if a later check is absent.
	find(t, r, "Hostnames")
	find(t, r, "Paths")
}

func TestAHostnameWithNoHostsEntryIsReportedWithTheSyncAsItsFix(t *testing.T) {
	r := diagnose(t, doctor.Input{
		Hosts: doctor.HostsState{Path: "/etc/hosts", Wanted: []string{"abc.xyz.com"}},
	})

	c := find(t, r, "Hosts file")
	if c.Status != doctor.Warn {
		t.Errorf("Hosts file check = %v, want Warn", c.Status)
	}
	if !strings.Contains(c.Detail, "abc.xyz.com") {
		t.Errorf("the check does not name the pending hostname: %q", c.Detail)
	}
	if !strings.Contains(c.Fix, "tamp hosts sync") {
		t.Errorf("the fix is not the sync: %q", c.Fix)
	}
}

// A line for a site that no longer exists is just as much out of sync as a
// missing one, and the same command fixes it.
func TestAnEntryForNoSiteIsReportedToo(t *testing.T) {
	r := diagnose(t, doctor.Input{Hosts: doctor.HostsState{Path: "/etc/hosts", Present: []string{"gone.example.test"}}})

	c := find(t, r, "Hosts file")
	if c.Status != doctor.Warn || !strings.Contains(c.Detail, "gone.example.test") {
		t.Errorf("Hosts file check = %v %q, want a warning naming the stale entry", c.Status, c.Detail)
	}
}

func TestABlockThatMatchesEveryCustomDomainPasses(t *testing.T) {
	r := diagnose(t, doctor.Input{
		Hosts: doctor.HostsState{
			Path:    "/etc/hosts",
			Wanted:  []string{"abc.xyz.com"},
			Present: []string{"abc.xyz.com"},
		},
	})

	if c := find(t, r, "Hosts file"); c.Status != doctor.Pass {
		t.Errorf("Hosts file check = %v %q, want Pass", c.Status, c.Detail)
	}
}

func diagnose(t *testing.T, in doctor.Input) doctor.Report {
	t.Helper()
	return doctor.Run(context.Background(), enginetest.Running(),
		router.New(t.TempDir(), enginetest.Running()), synctest.Installed(), in)
}
