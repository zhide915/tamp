package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/docker"
	"github.com/zhide915/tamp/internal/exitcode"
)

// cli runs tamp in-process through the cobra root, against a temp home, a
// temp working directory, and the Docker fake.
type cli struct {
	dir    string
	docker *docker.Fake
	// terminal makes stdout and stderr claim to be a terminal, for the
	// color rules.
	terminal bool
}

func sandbox(t *testing.T) *cli {
	t.Helper()
	home := t.TempDir()
	dir := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // what os.UserHomeDir reads on Windows
	t.Setenv("NO_COLOR", "")      // empty counts as unset
	t.Chdir(dir)
	return &cli{dir: dir, docker: &docker.Fake{}}
}

type result struct {
	code   exitcode.Code
	stdout string
	stderr string
}

// terminalBuffer claims to be a terminal without a pty.
type terminalBuffer struct{ bytes.Buffer }

func (*terminalBuffer) IsTerminal() bool { return true }

func (c *cli) run(t *testing.T, args ...string) result {
	t.Helper()
	if c.terminal {
		var stdout, stderr terminalBuffer
		code := run(args, &stdout, &stderr, os.LookupEnv, c.docker)
		return result{code: code, stdout: stdout.String(), stderr: stderr.String()}
	}
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr, os.LookupEnv, c.docker)
	return result{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

func (r result) assertCode(t *testing.T, want exitcode.Code) {
	t.Helper()
	if r.code != want {
		t.Errorf("exit code = %d, want %d\nstdout: %s\nstderr: %s", r.code, want, r.stdout, r.stderr)
	}
}

// assertOneLineError checks the error contract: nothing on stdout, and one
// "error:" line on stderr that holds each of want.
func (r result) assertOneLineError(t *testing.T, want ...string) {
	t.Helper()
	if r.stdout != "" {
		t.Errorf("stdout = %q, want empty", r.stdout)
	}
	line, rest, _ := strings.Cut(r.stderr, "\n")
	if !strings.HasPrefix(line, "error: ") || rest != "" {
		t.Errorf("stderr = %q, want one line starting with \"error: \"", r.stderr)
	}
	for _, w := range want {
		if !strings.Contains(line, w) {
			t.Errorf("stderr = %q, want it to contain %q", r.stderr, w)
		}
	}
}
