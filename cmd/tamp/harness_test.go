package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/docker"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/syncer"
)

// cli runs tamp in-process through the cobra root, against a temp home, a
// temp working directory, and the Docker fake.
type cli struct {
	dir, home string
	docker    *docker.Fake
	// sync is the Mutagen fake, set for a Windows or macOS build; nil bind-mounts
	// apps/, as on Linux.
	sync *syncer.Fake
	// opened records each URL handed to the browser, which no test opens.
	opened []string
	// terminal makes stdin, stdout and stderr claim to be a terminal, for
	// the color rules and shell's TTY.
	terminal bool
	// terminalIn makes only stdin a terminal, as with stdout redirected.
	terminalIn bool
	// plainErr keeps stderr off the terminal that terminal sets, as with 2>file.
	plainErr bool
}

func sandbox(t *testing.T) *cli {
	t.Helper()
	home := t.TempDir()
	dir := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // what os.UserHomeDir reads on Windows
	t.Setenv("NO_COLOR", "")      // empty counts as unset
	t.Chdir(dir)

	// Host git runs for real, with GitHub and acme's SSH host rewritten to
	// the local remotes.
	gitconfig := filepath.Join(home, ".gitconfig")
	body := fmt.Sprintf("[url %q]\n\tinsteadOf = https://github.com/frappe/\n[url %q]\n\tinsteadOf = %s\n",
		fileURL(remotes)+"/", fileURL(filepath.Join(remotes, "acme"))+"/", acmeSSH)
	if err := os.WriteFile(gitconfig, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", gitconfig)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	return &cli{dir: dir, home: home, docker: &docker.Fake{}}
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
		var stdin, stdout terminalBuffer
		var stderr interface {
			io.Writer
			String() string
		} = &terminalBuffer{}
		if c.plainErr {
			stderr = &bytes.Buffer{}
		}
		code := run(args, &stdin, &stdout, stderr, os.LookupEnv, c.docker, c.syncClient(), c.openURL)
		return result{code: code, stdout: stdout.String(), stderr: stderr.String()}
	}
	var stdin io.Reader = &bytes.Buffer{}
	if c.terminalIn {
		stdin = &terminalBuffer{}
	}
	var stdout, stderr bytes.Buffer
	code := run(args, stdin, &stdout, &stderr, os.LookupEnv, c.docker, c.syncClient(), c.openURL)
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
	err = makeRemotes(dir)
	code := 1
	if err == nil {
		code = m.Run()
	} else {
		fmt.Fprintln(os.Stderr, err)
	}
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// acmeSSH is the SSH URL prefix of acme's private repositories, which the
// sandbox rewrites to remotes/acme.
const acmeSSH = "git@host:acme/"

// makeRemotes builds the bare repositories that stand in for GitHub's frappe
// organisation and acme's private host:
//   - frappe, with every supported branch and a package.json;
//   - erpnext, named by its setup.py;
//   - hrms, which requires erpnext and has a package.json;
//   - acme/acme-erp.git, which holds the package acme, with branches main and
//     develop and main as its default;
//   - acme/acme-hr.git, which requires acme by the URL of acme-erp.
func makeRemotes(dir string) error {
	pyproject := func(name string) string { return "[project]\nname = \"" + name + "\"\n" }
	remotes := []struct {
		path     string
		branches []string
		files    map[string]string
	}{
		{"frappe", []string{"version-15", "version-16", "develop"}, map[string]string{"package.json": "{}\n"}},
		{"erpnext", []string{"develop", "version-15"}, map[string]string{
			"setup.py": "from setuptools import setup\n\nsetup(name=\"erpnext\", version=\"15.0.0\")\n",
		}},
		{"hrms", []string{"develop", "version-15"}, map[string]string{
			"pyproject.toml": pyproject("hrms"),
			"package.json":   "{}\n",
			"hrms/hooks.py":  "app_name = \"hrms\"\nrequired_apps = [\n\t\"frappe/erpnext\",\n]\n",
		}},
		{"acme/acme-erp.git", []string{"main", "develop"}, map[string]string{
			"pyproject.toml": pyproject("acme"),
			"acme/hooks.py":  "app_name = \"acme\"\n",
		}},
		{"acme/acme-hr.git", []string{"main"}, map[string]string{
			"pyproject.toml":   pyproject("acme_hr"),
			"acme_hr/hooks.py": "app_name = \"acme_hr\"\nrequired_apps = [\"" + acmeSSH + "acme-erp.git\"]\n",
		}},
	}
	for _, r := range remotes {
		if err := makeRemote(filepath.Join(dir, r.path), r.branches, r.files); err != nil {
			return err
		}
	}
	return nil
}

// makeRemote builds a bare repository whose default branch is the first of
// branches, with files in its first commit and two commits on each branch,
// so a depth-1 clone is visibly shallow.
func makeRemote(bare string, branches []string, files map[string]string) error {
	work := bare + "-work"
	for name, body := range files {
		path := filepath.Join(work, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return err
		}
	}
	steps := [][]string{
		{"init", "--quiet", "--initial-branch", branches[0], work},
		{"-C", work, "add", "--all"},
	}
	for i, branch := range branches {
		if i > 0 {
			steps = append(steps, []string{"-C", work, "checkout", "--quiet", "-b", branch})
		}
		for i := 1; i <= 2; i++ {
			steps = append(steps, []string{"-C", work, "commit", "--quiet", "--allow-empty", "-m", fmt.Sprintf("%s %d", branch, i)})
		}
	}
	// The bare clone's default branch is the one checked out.
	steps = append(steps, []string{"-C", work, "checkout", "--quiet", branches[0]},
		[]string{"clone", "--quiet", "--bare", work, bare})
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

// syncClient is nil, not a nil *syncer.Fake, when no test set sync.
func (c *cli) syncClient() syncer.Client {
	if c.sync == nil {
		return nil
	}
	return c.sync
}

func (c *cli) openURL(_ context.Context, url string) error {
	c.opened = append(c.opened, url)
	return nil
}
