package main

import (
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/exitcode"
)

func TestNoArgumentsPrintsHelp(t *testing.T) {
	r := sandbox(t).run(t)

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "environment manager for Frappe Framework", "Usage:", "version")
	if r.stderr != "" {
		t.Errorf("stderr = %q, want empty", r.stderr)
	}
}

func TestHelpFlagPrintsHelp(t *testing.T) {
	r := sandbox(t).run(t, "--help")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "Usage:", "--no-color", "--quiet")
}

func TestVersionPrintsVersionCommitAndBuildDate(t *testing.T) {
	r := sandbox(t).run(t, "version")

	r.assertCode(t, exitcode.CodeOK)
	// Pin the shape, not just the values: three labelled lines, so dropping a
	// label or collapsing them onto one line goes red.
	want := "tamp " + version + "\ncommit:     " + commit + "\nbuild date: " + buildDate + "\n"
	if r.stdout != want {
		t.Errorf("stdout = %q, want %q", r.stdout, want)
	}
}

// --quiet silences progress, never the thing the caller asked for.
func TestQuietStillPrintsResults(t *testing.T) {
	r := sandbox(t).run(t, "--quiet", "version")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, version)
}

func TestNoColorIsAcceptedEverywhere(t *testing.T) {
	r := sandbox(t).run(t, "--no-color", "version")

	r.assertCode(t, exitcode.CodeOK)
	if strings.Contains(r.stdout, "\x1b[") {
		t.Errorf("stdout = %q, want no escape sequences", r.stdout)
	}
}

func TestUnknownCommandIsAUsageError(t *testing.T) {
	r := sandbox(t).run(t, "nope")

	r.assertCode(t, exitcode.CodeUsage)
	// One line, "error:" prefix, and it always names the fix.
	r.assertStderrContains(t, `error: unknown command "nope"`, "tamp --help")
	if lines := strings.Count(strings.TrimSpace(r.stderr), "\n"); lines != 0 {
		t.Errorf("stderr spans %d lines, want one:\n%s", lines+1, r.stderr)
	}
	if r.stdout != "" {
		t.Errorf("stdout = %q, want empty — errors never go to stdout", r.stdout)
	}
}

func TestUnknownFlagIsAUsageError(t *testing.T) {
	r := sandbox(t).run(t, "--nope")

	r.assertCode(t, exitcode.CodeUsage)
	r.assertStderrContains(t, "error: unknown flag: --nope", "tamp --help")
}

func TestUnexpectedArgumentIsAUsageError(t *testing.T) {
	r := sandbox(t).run(t, "version", "extra")

	r.assertCode(t, exitcode.CodeUsage)
	r.assertStderrContains(t, `error: unexpected argument "extra"`, "tamp version --help")
}

// Naming a command that does not exist is a usage error however it is spelled;
// cobra's built-in help command would have reported it at exit 0.
func TestUnknownHelpTopicIsAUsageError(t *testing.T) {
	r := sandbox(t).run(t, "help", "nope")

	r.assertCode(t, exitcode.CodeUsage)
	r.assertStderrContains(t, `error: unknown command "nope"`, "tamp --help")
}

func TestHelpForAKnownCommandPrintsItsHelp(t *testing.T) {
	r := sandbox(t).run(t, "help", "version")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "tamp version", "build date")
}
