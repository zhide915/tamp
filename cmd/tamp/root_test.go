package main

import (
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/exitcode"
)

func TestVersionPrintsTheVersionCommitAndBuildDate(t *testing.T) {
	saved := [3]string{version, commit, buildDate}
	t.Cleanup(func() { version, commit, buildDate = saved[0], saved[1], saved[2] })
	version, commit, buildDate = "1.2.3", "abc1234", "2026-10-27T10:00:00Z"
	c := sandbox(t)

	r := c.run(t, "version")

	r.assertCode(t, exitcode.CodeOK)
	want := "tamp 1.2.3\ncommit: abc1234\nbuild date: 2026-10-27T10:00:00Z\n"
	if r.stdout != want {
		t.Errorf("stdout = %q, want %q", r.stdout, want)
	}
}

func TestVersionSurvivesQuiet(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "version", "--quiet")

	r.assertCode(t, exitcode.CodeOK)
	if !strings.HasPrefix(r.stdout, "tamp ") {
		t.Errorf("stdout = %q, want the version", r.stdout)
	}
}

func TestAnUnknownCommandExits2(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "create", "erp-dev")

	r.assertCode(t, exitcode.CodeUsage)
	r.assertOneLineError(t, "create", "`tamp --help`")
}

func TestAnUnknownFlagExits2(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "new", "erp-dev", "--frappe", "version-15", "--apps", "hrms")

	r.assertCode(t, exitcode.CodeUsage)
	r.assertOneLineError(t, "--apps", "`tamp new --help`")
	assertNotExist(t, c.dir+"/erp-dev")
}

func TestAnUnexpectedArgumentExits2(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "version", "now")

	r.assertCode(t, exitcode.CodeUsage)
	r.assertOneLineError(t, "now", "`tamp version --help`")
}

func TestHelpForAnUnknownTopicExits2(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "help", "create")

	r.assertCode(t, exitcode.CodeUsage)
	r.assertOneLineError(t, "create")
}

func TestTheGlobalFlagsAreAccepted(t *testing.T) {
	c := sandbox(t)

	c.run(t, "version", "-e", "erp-dev", "--quiet", "--no-color").assertCode(t, exitcode.CodeOK)
	c.run(t, "version", "--env", "erp-dev").assertCode(t, exitcode.CodeOK)
}

func TestErrorsAreColoredOnATerminal(t *testing.T) {
	c := sandbox(t)
	c.terminal = true

	r := c.run(t, "new")

	if !strings.Contains(r.stderr, "\x1b[") {
		t.Errorf("stderr = %q, want color", r.stderr)
	}
}

func TestNoColorAndNO_COLORTurnColorOff(t *testing.T) {
	t.Run("--no-color", func(t *testing.T) {
		c := sandbox(t)
		c.terminal = true

		ok := c.run(t, "new", "erp-dev", "--frappe", "version-15", "--no-color")
		failed := c.run(t, "new", "--no-color")

		if strings.Contains(ok.stdout, "\x1b[") {
			t.Errorf("stdout = %q, want no color", ok.stdout)
		}
		if strings.Contains(failed.stderr, "\x1b[") {
			t.Errorf("stderr = %q, want no color", failed.stderr)
		}
	})
	// pflag stops at the first unknown flag, so a later --no-color is never
	// parsed.
	t.Run("--no-color after an unknown flag", func(t *testing.T) {
		for _, args := range [][]string{
			{"--bogus", "--no-color"},
			{"version", "--bogus", "--no-color"},
			{"new", "erp-dev", "--bogus", "--no-color"},
			{"new", "--no-color", "--bogus"},
		} {
			c := sandbox(t)
			c.terminal = true

			r := c.run(t, args...)

			r.assertCode(t, exitcode.CodeUsage)
			if strings.Contains(r.stderr, "\x1b[") {
				t.Errorf("tamp %v: stderr = %q, want no color", args, r.stderr)
			}
		}
	})
	t.Run("--no-color after -- is an argument", func(t *testing.T) {
		c := sandbox(t)
		c.terminal = true

		r := c.run(t, "version", "--", "--no-color")

		r.assertCode(t, exitcode.CodeUsage)
		if !strings.Contains(r.stderr, "\x1b[") {
			t.Errorf("stderr = %q, want color", r.stderr)
		}
	})
	t.Run("NO_COLOR", func(t *testing.T) {
		c := sandbox(t)
		c.terminal = true
		t.Setenv("NO_COLOR", "1")

		r := c.run(t, "new")

		if strings.Contains(r.stderr, "\x1b[") {
			t.Errorf("stderr = %q, want no color", r.stderr)
		}
	})
}
