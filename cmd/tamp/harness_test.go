package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/engine/enginetest"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/hosts"
	"github.com/zhide915/tamp/internal/syncer/synctest"
)

// CLI test harness: drive the real cobra root in-process against a temp HOME
// and working directory, asserting only on stdout, stderr and exit code.

// cli is a sandboxed tamp installation; state persists across run calls.
type cli struct {
	home string
	dir  string
	// engine and sync are tamp's two fake points, both recording. They start
	// healthy; failure tests replace them. The browser below is a seam of the
	// same kind: recorded, never run.
	engine *enginetest.Fake
	sync   *synctest.Fake
	// stdin defaults to an exhausted pipe: no terminal, nothing to read.
	stdin io.Reader
	// opened records the URLs handed to the browser. Real in a test would
	// open a window on the developer's screen.
	opened []string
	// openErr fails the browser launch, for the machine with none.
	openErr error
}

func sandbox(t *testing.T) *cli {
	t.Helper()
	home := t.TempDir()
	dir := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // what os.UserHomeDir reads on Windows
	t.Setenv("NO_COLOR", "")      // empty counts as unset: colour neither forced nor blocked
	// The bridge runs host git for real; point it away from the developer's
	// own credential manager. Tests that want credentials write this file.
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, "gitconfig"))
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
	// Git Bash exports SSH_ASKPASS; left alone, a fill with no helper opens
	// a GUI prompt on the developer's screen and hangs the test on it.
	t.Setenv("GIT_ASKPASS", "true")
	// Never the machine's own hosts file: 'tamp hosts sync' writes for real,
	// and a test that reached /etc/hosts would be editing the developer's
	// system. The redirect also forbids elevation, so no test can raise a
	// UAC or sudo prompt.
	t.Setenv(hosts.PathVar, filepath.Join(home, "hosts"))
	t.Chdir(dir)
	return &cli{
		home:   home,
		dir:    dir,
		engine: enginetest.Running(),
		sync:   synctest.Installed(),
		stdin:  strings.NewReader(""),
	}
}

type result struct {
	code   exitcode.Code
	stdout string
	stderr string
}

func (c *cli) run(t *testing.T, args ...string) result {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, c.stdin, &stdout, &stderr, os.LookupEnv, c.engine, c.sync, c.open)
	return result{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

func (r result) assertCode(t *testing.T, want exitcode.Code) {
	t.Helper()
	if r.code != want {
		t.Errorf("exit code = %d, want %d\nstdout: %s\nstderr: %s", r.code, want, r.stdout, r.stderr)
	}
}

func (r result) assertStdoutContains(t *testing.T, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(r.stdout, w) {
			t.Errorf("stdout does not contain %q\nstdout: %s", w, r.stdout)
		}
	}
}

func (r result) assertStderrContains(t *testing.T, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(r.stderr, w) {
			t.Errorf("stderr does not contain %q\nstderr: %s", w, r.stderr)
		}
	}
}

// open stands in for the machine's browser, recording what it was handed.
func (c *cli) open(_ context.Context, url string) error {
	if c.openErr != nil {
		return c.openErr
	}
	c.opened = append(c.opened, url)
	return nil
}

// mark is where the recorded commands stand now, so a later assertion can ask
// only about what the next command ran — create touches many of the same
// paths.
func (c *cli) mark() int { return len(c.engine.Execs) }
