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

func TestVersionPrintsVersionCommitAndBuildDate(t *testing.T) {
	r := sandbox(t).run(t, "version")

	r.assertCode(t, exitcode.CodeOK)
	// Exact match pins the three-labelled-line shape, not just the values.
	want := "tamp " + version + "\ncommit:     " + commit + "\nbuild date: " + buildDate + "\n"
	if r.stdout != want {
		t.Errorf("stdout = %q, want %q", r.stdout, want)
	}
}

func TestUnknownCommandIsAUsageError(t *testing.T) {
	r := sandbox(t).run(t, "nope")

	r.assertCode(t, exitcode.CodeUsage)
	r.assertStderrContains(t, `error: unknown command "nope"`, "tamp --help")
	if lines := strings.Count(strings.TrimSpace(r.stderr), "\n"); lines != 0 {
		t.Errorf("stderr spans %d lines, want one:\n%s", lines+1, r.stderr)
	}
	if r.stdout != "" {
		t.Errorf("stdout = %q, want empty — errors never go to stdout", r.stdout)
	}
}

func TestUnexpectedArgumentIsAUsageError(t *testing.T) {
	r := sandbox(t).run(t, "version", "extra")

	r.assertCode(t, exitcode.CodeUsage)
	r.assertStderrContains(t, `error: unexpected argument "extra"`, "tamp version --help")
}

// cobra's built-in help would report an unknown topic at exit 0.
func TestUnknownHelpTopicIsAUsageError(t *testing.T) {
	r := sandbox(t).run(t, "help", "nope")

	r.assertCode(t, exitcode.CodeUsage)
	r.assertStderrContains(t, `error: unknown command "nope"`, "tamp --help")
}
