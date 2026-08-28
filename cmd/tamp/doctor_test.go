package main

import (
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/engine/enginetest"
	"github.com/zhide915/tamp/internal/exitcode"
)

func TestDoctorReportsEveryCheckWhenTheEngineIsHealthy(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "doctor")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t,
		"✓ Docker",
		"29.7.2",
		"unix:///var/run/docker.sock",
		"✓ Docker Compose",
		"2.39.1",
	)
	if r.stderr != "" {
		t.Errorf("stderr = %q, want empty — a healthy report is not a diagnostic", r.stderr)
	}
}

// The report is what the user asked for, so it must survive being redirected;
// a doctor that wrote its failures to stderr would produce an empty file.
func TestDoctorWritesFailuresToStdout(t *testing.T) {
	c := sandbox(t)
	c.engine = enginetest.Unavailable()

	r := c.run(t, "doctor")

	r.assertStdoutContains(t, "✗ Docker", "no Docker engine found")
	if strings.Contains(r.stderr, "✗") {
		t.Errorf("stderr = %q, want the report on stdout", r.stderr)
	}
}

// Exit 4 is the documented "engine unavailable" code, and it is what an agent
// branches on to decide whether to tell the user to start Docker.
func TestDoctorExitsEngineUnavailableWhenDockerIsDown(t *testing.T) {
	c := sandbox(t)
	c.engine = enginetest.Unavailable()

	r := c.run(t, "doctor")

	r.assertCode(t, exitcode.CodeEngineUnavailable)
}

// doctor has already said what is wrong, in more detail than one line can
// carry; tamp's trailing "error:" line would only repeat it.
func TestDoctorDoesNotAppendAnErrorLineToItsOwnReport(t *testing.T) {
	c := sandbox(t)
	c.engine = enginetest.Unavailable()

	r := c.run(t, "doctor")

	if strings.Contains(r.stdout+r.stderr, "error:") {
		t.Errorf("output repeats the failure as an error line:\n%s%s", r.stdout, r.stderr)
	}
}

// Every failing check names its fix — that is the entire reason doctor exists
// rather than tamp just failing at the moment of use.
func TestDoctorPrintsTheFixForEveryFailingCheck(t *testing.T) {
	c := sandbox(t)
	c.engine = enginetest.Unavailable()

	r := c.run(t, "doctor")

	r.assertStdoutContains(t, "start Docker Desktop", "install Docker Desktop")
}

// A failing report is the result, not progress, so --quiet cannot swallow it.
func TestDoctorReportSurvivesQuiet(t *testing.T) {
	c := sandbox(t)
	c.engine = enginetest.Unavailable()

	r := c.run(t, "--quiet", "doctor")

	r.assertCode(t, exitcode.CodeEngineUnavailable)
	r.assertStdoutContains(t, "✗ Docker", "start Docker Desktop")
}

// A stopped Docker must not hide a missing compose: they are separate installs
// and the user needs both problems in one pass.
func TestDoctorStillChecksComposeWhenDockerIsDown(t *testing.T) {
	c := sandbox(t)
	c.engine.PingErr = exitcode.New(exitcode.CodeEngineUnavailable, "Docker is not answering", "start Docker")

	r := c.run(t, "doctor")

	r.assertCode(t, exitcode.CodeEngineUnavailable)
	r.assertStdoutContains(t, "✗ Docker", "✓ Docker Compose")
}

func TestDoctorTakesNoArguments(t *testing.T) {
	r := sandbox(t).run(t, "doctor", "extra")

	r.assertCode(t, exitcode.CodeUsage)
	r.assertStderrContains(t, `error: unexpected argument "extra"`, "tamp doctor --help")
}

func TestDoctorIsListedInHelp(t *testing.T) {
	r := sandbox(t).run(t)

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "doctor")
}

// Exit 4 is for commands that need the engine. Everything else has to keep
// working on a machine with no Docker at all — tamp must be usable before it
// is configured, and `tamp version` in a bug report is filed from exactly
// such a machine.
func TestCommandsThatDoNotNeedTheEngineIgnoreItBeingDown(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"--help"}, {}} {
		c := sandbox(t)
		c.engine = enginetest.Unavailable()

		r := c.run(t, args...)

		r.assertCode(t, exitcode.CodeOK)
		if len(c.engine.Calls) != 0 {
			t.Errorf("tamp %v touched the engine: %v", args, c.engine.Calls)
		}
	}
}
