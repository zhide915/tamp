package doctor_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/doctor"
	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/engine/enginetest"
	"github.com/zhide915/tamp/internal/exitcode"
)

// doctor's contract is honesty: every check says pass, warn or fail, a failing
// check always carries the fix, and tamp exits non-zero exactly when
// something failed. These tests hold that against a recording fake, which also
// pins what tamp asked the engine to do.

func run(e engine.Engine) doctor.Report {
	return doctor.Run(context.Background(), e)
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

func TestHealthyEngineReportsEveryCheckAsPassing(t *testing.T) {
	r := run(enginetest.Running())

	for _, c := range r.Checks {
		if c.Status != doctor.Pass {
			t.Errorf("check %q = %v, want Pass", c.Name, c.Status)
		}
	}
	if got := r.ExitCode(); got != exitcode.CodeOK {
		t.Errorf("ExitCode() = %d, want %d", got, exitcode.CodeOK)
	}
}

// "Docker reachable (version, socket path)" — a check that says only "pass"
// tells the user nothing about which engine tamp would use.
func TestDockerCheckNamesTheVersionAndTheAddress(t *testing.T) {
	c := find(t, run(enginetest.Running()), "Docker")

	for _, want := range []string{"29.7.2", "unix:///var/run/docker.sock", string(engine.SourceProbe)} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("detail %q does not mention %q", c.Detail, want)
		}
	}
}

func TestComposeCheckNamesTheVersion(t *testing.T) {
	c := find(t, run(enginetest.Running()), "Docker Compose")

	if !strings.Contains(c.Detail, "2.39.1") {
		t.Errorf("detail %q does not name the compose version", c.Detail)
	}
}

func TestUnreachableEngineFailsWithTheExitCodeAndTheFix(t *testing.T) {
	r := run(enginetest.Unavailable())

	c := find(t, r, "Docker")
	if c.Status != doctor.Fail {
		t.Errorf("Docker check = %v, want Fail", c.Status)
	}
	if c.Fix == "" {
		t.Error("a failing check carries no fix; doctor exists to tell the user what to do")
	}
	if !strings.Contains(c.Detail, "no Docker engine found") {
		t.Errorf("detail %q does not say what went wrong", c.Detail)
	}
	if got := r.ExitCode(); got != exitcode.CodeEngineUnavailable {
		t.Errorf("ExitCode() = %d, want %d", got, exitcode.CodeEngineUnavailable)
	}
}

// Compose lives behind the docker CLI, not the daemon, so a stopped Docker
// must not hide the fact that compose is installed and fine. Reporting both
// checks from one run is the difference between "Docker is down" and "you also
// need to install compose".
func TestComposeIsStillCheckedWhenDockerIsDown(t *testing.T) {
	e := enginetest.Running()
	e.PingErr = exitcode.New(exitcode.CodeEngineUnavailable, "Docker is not answering", "start Docker")

	r := run(e)

	if got := find(t, r, "Docker").Status; got != doctor.Fail {
		t.Errorf("Docker check = %v, want Fail", got)
	}
	if got := find(t, r, "Docker Compose").Status; got != doctor.Pass {
		t.Errorf("Compose check = %v, want Pass — compose does not need the daemon", got)
	}
	if !slices.Contains(e.Calls, "ComposeVersion") {
		t.Errorf("tamp never asked for the compose version; calls were %v", e.Calls)
	}
}

// The recording fake earns its keep here: this is the assertion that tamp
// asked the engine for exactly what it reports, once each.
func TestDoctorAsksTheEngineForEachCheckExactlyOnce(t *testing.T) {
	e := enginetest.Running()

	run(e)

	want := []string{"Ping", "ComposeVersion"}
	if !slices.Equal(e.Calls, want) {
		t.Errorf("engine calls = %v, want %v", e.Calls, want)
	}
}

// tamp's exit codes are a public contract, so doctor reports the code of the
// thing that is broken rather than a blanket "something failed".
func TestExitCodeComesFromTheFailingCheck(t *testing.T) {
	e := enginetest.Running()
	e.PingErr = exitcode.New(exitcode.CodeFailed, "something else went wrong", "retry")

	if got := run(e).ExitCode(); got != exitcode.CodeFailed {
		t.Errorf("ExitCode() = %d, want %d", got, exitcode.CodeFailed)
	}
}

// A warning is tamp noticing something without refusing to work; it must not
// turn into a non-zero exit, or every soft notice would break scripts.
func TestWarningsDoNotFailTheReport(t *testing.T) {
	r := doctor.Report{Checks: []doctor.Check{
		{Name: "a", Status: doctor.Pass},
		{Name: "b", Status: doctor.Warn, Fix: "consider fixing"},
	}}

	if !r.OK() {
		t.Error("OK() = false with only a warning, want true")
	}
	if got := r.ExitCode(); got != exitcode.CodeOK {
		t.Errorf("ExitCode() = %d, want %d", got, exitcode.CodeOK)
	}
}
