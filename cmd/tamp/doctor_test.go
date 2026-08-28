package main

import (
	"os"
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
		"✓ Hostnames",
		"*.localhost",
		"✓ Paths",
	)
	if r.stderr != "" {
		t.Errorf("stderr = %q, want empty — a healthy report is not a diagnostic", r.stderr)
	}
}

// The create-time path warning must not be the only one: a directory can
// start syncing to the cloud long after create.
func TestDoctorWarnsAboutAnEnvironmentInACloudSyncedPath(t *testing.T) {
	c := sandbox(t)
	parent := c.path("OneDrive")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	c.run(t, "create", "demo", "--frappe", "version-15", "--sync", "bind", "--dir", parent).
		assertCode(t, exitcode.CodeOK)

	r := c.run(t, "doctor")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "! Paths", "OneDrive")
}

// The report must survive redirection; failures on stderr would leave an
// empty file.
func TestDoctorWritesFailuresToStdout(t *testing.T) {
	c := sandbox(t)
	c.engine = enginetest.Unavailable()

	r := c.run(t, "doctor")

	r.assertStdoutContains(t, "✗ Docker", "no Docker engine found")
	if strings.Contains(r.stderr, "✗") {
		t.Errorf("stderr = %q, want the report on stdout", r.stderr)
	}
}

func TestDoctorDoesNotAppendAnErrorLineToItsOwnReport(t *testing.T) {
	c := sandbox(t)
	c.engine = enginetest.Unavailable()

	r := c.run(t, "doctor")

	if strings.Contains(r.stdout+r.stderr, "error:") {
		t.Errorf("output repeats the failure as an error line:\n%s%s", r.stdout, r.stderr)
	}
}

func TestDoctorPrintsTheFixForEveryFailingCheck(t *testing.T) {
	c := sandbox(t)
	c.engine = enginetest.Unavailable()

	r := c.run(t, "doctor")

	r.assertStdoutContains(t, "start Docker Desktop", "install Docker Desktop")
}

// The report is the result, not progress.
func TestDoctorReportSurvivesQuiet(t *testing.T) {
	c := sandbox(t)
	c.engine = enginetest.Unavailable()

	r := c.run(t, "--quiet", "doctor")

	r.assertCode(t, exitcode.CodeEngineUnavailable)
	r.assertStdoutContains(t, "✗ Docker", "start Docker Desktop")
}

// Docker and compose are separate installs; report both problems in one pass.
func TestDoctorStillChecksComposeWhenDockerIsDown(t *testing.T) {
	c := sandbox(t)
	c.engine.PingErr = exitcode.New(exitcode.CodeEngineUnavailable, "Docker is not answering", "start Docker")

	r := c.run(t, "doctor")

	r.assertCode(t, exitcode.CodeEngineUnavailable)
	r.assertStdoutContains(t, "✗ Docker", "✓ Docker Compose")
}

// tamp must work before it is configured: 'tamp version' in a bug report is
// filed from a machine with no Docker.
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
