package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

	// Host git runs for real, with GitHub rewritten to the local remotes.
	gitconfig := filepath.Join(home, ".gitconfig")
	body := fmt.Sprintf("[url %q]\n\tinsteadOf = https://github.com/frappe/\n", fileURL(remotes)+"/")
	if err := os.WriteFile(gitconfig, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", gitconfig)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
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

// remotes holds the bare repositories that stand in for github.com/frappe,
// built once for the test binary.
var remotes string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "tamp-remotes-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	remotes = dir
	err = makeFrappeRemote(filepath.Join(dir, "frappe"))
	code := 1
	if err == nil {
		code = m.Run()
	} else {
		fmt.Fprintln(os.Stderr, err)
	}
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// makeFrappeRemote builds a bare repository with two commits on each
// supported branch, so a depth-1 clone is visibly shallow.
func makeFrappeRemote(bare string) error {
	work := bare + "-work"
	steps := [][]string{{"init", "--quiet", "--initial-branch", "version-15", work}}
	for _, branch := range []string{"version-15", "version-16", "develop"} {
		if branch != "version-15" {
			steps = append(steps, []string{"-C", work, "checkout", "--quiet", "-b", branch})
		}
		for i := 1; i <= 2; i++ {
			steps = append(steps, []string{"-C", work, "commit", "--quiet", "--allow-empty", "-m", fmt.Sprintf("%s %d", branch, i)})
		}
	}
	steps = append(steps, []string{"clone", "--quiet", "--bare", work, bare})
	for _, args := range steps {
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=tamp", "GIT_AUTHOR_EMAIL=tamp@example.com",
			"GIT_COMMITTER_NAME=tamp", "GIT_COMMITTER_EMAIL=tamp@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %v: %v\n%s", args, err, out)
		}
	}
	return os.RemoveAll(work)
}

// fileURL is a file:// URL for a local path: git ignores --depth on a plain
// path.
func fileURL(path string) string {
	return "file:///" + strings.TrimPrefix(filepath.ToSlash(path), "/")
}
