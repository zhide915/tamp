package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/engine/enginetest"
	"github.com/zhide915/tamp/internal/exitcode"
)

// This file is tamp's CLI test harness and the house style for every later
// command test: drive the real cobra root in-process, against a temp HOME and
// a temp working directory, and assert only on stdout, stderr and exit code.
// Nothing here touches the developer's real ~/.tamp.

// cli is a sandboxed tamp installation. Create one per test with sandbox(t)
// and call run as many times as the scenario needs — state written by an
// earlier command is still there for the next one.
type cli struct {
	// home is the temp HOME; tamp's global state lives in <home>/.tamp.
	home string
	// dir is the temp working directory commands start in.
	dir string
	// engine is the recording fake standing in for Docker — tamp's only fake
	// point. It starts healthy; a test about a broken engine replaces it, and
	// afterwards reads engine.Calls to assert what tamp asked it to do.
	engine *enginetest.Fake
}

func sandbox(t *testing.T) *cli {
	t.Helper()
	home := t.TempDir()
	dir := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir consults this on Windows
	t.Setenv("NO_COLOR", "")      // empty reads as unset, so it neither forces nor blocks colour
	t.Chdir(dir)
	return &cli{home: home, dir: dir, engine: enginetest.Running()}
}

type result struct {
	code   exitcode.Code
	stdout string
	stderr string
}

func (c *cli) run(t *testing.T, args ...string) result {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr, os.LookupEnv, c.engine)
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
