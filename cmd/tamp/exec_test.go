package main

import (
	"io"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/engine/enginetest"
	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/frappe"
	"github.com/zhide915/tamp/internal/toolchain"
)

// exec is the bridge agents drive the bench through; these tests pin its
// contract: where the command lands, what comes back, what tamp refuses.

func (c *cli) container(t *testing.T, name, service string) string {
	t.Helper()
	res, err := env.NewResources(env.Name(name), c.path(name))
	if err != nil {
		t.Fatalf("cannot derive %s's resource names: %v", name, err)
	}
	return res.Container(service)
}

// lastExec returns the newest container exec — create runs many of its own
// first, so the interesting one is always the last.
func (c *cli) lastExec(t *testing.T) enginetest.Exec {
	t.Helper()
	if len(c.engine.Execs) == 0 {
		t.Fatal("tamp never ran anything inside a container")
	}
	return c.engine.Execs[len(c.engine.Execs)-1]
}

func (c *cli) execCount() int { return len(c.engine.Execs) }

func TestExecRunsTheCommandOnTheBench(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")

	r := c.run(t, "exec", "demo", "--", "bench", "--version")

	r.assertCode(t, exitcode.CodeOK)
	got := c.lastExec(t)
	if got.Line() != "bench --version" {
		t.Errorf("tamp ran %q, want %q", got.Line(), "bench --version")
	}
	if want := c.container(t, "demo", env.FrappeService); got.Container != want {
		t.Errorf("tamp ran it in %s, want %s", got.Container, want)
	}
	if got.WorkDir != frappe.BenchDir {
		t.Errorf("working directory = %q, want the bench root %q", got.WorkDir, frappe.BenchDir)
	}
	if got.User != toolchain.User {
		t.Errorf("user = %q, want %q", got.User, toolchain.User)
	}
}

// Callers branch on the inner command's exit code; tamp adds nothing to it.
func TestExecPropagatesTheCommandsExitCode(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.engine.ExecFails = map[string]error{
		"bench migrate": &engine.ExitError{
			Container: c.container(t, "demo", env.FrappeService),
			Cmd:       []string{"bench", "migrate"},
			Status:    42,
		},
	}

	r := c.run(t, "exec", "demo", "--", "bench", "migrate")

	r.assertCode(t, 42)
	if strings.Contains(r.stderr, "error:") {
		t.Errorf("tamp added an error line to a command that failed on its own:\n%s", r.stderr)
	}
}

func TestExecAgainstAStoppedEnvironmentRefusesAndSaysHowToStartIt(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.run(t, "stop", "demo").assertCode(t, exitcode.CodeOK)
	before := c.execCount()

	r := c.run(t, "exec", "demo", "--", "bench", "--version")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, "tamp start")
	if c.execCount() != before {
		t.Errorf("tamp ran %q against a stopped environment", c.lastExec(t).Line())
	}
}

// honcho already runs the bench's processes; these would fight it.
func TestExecRefusesTheCommandsThatWouldFightTheEnvironment(t *testing.T) {
	tests := []struct {
		cmd  []string
		says string
	}{
		{[]string{"bench", "start"}, "already runs"},
		{[]string{"bench", "serve"}, "already runs"},
		// The hint quotes the && chain so the user's host shell cannot claim
		// the trailing commands.
		{[]string{"bench", "update"}, `bash -c "bench setup requirements && bench build && bench migrate"`},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.cmd, " "), func(t *testing.T) {
			c := sandbox(t)
			c.create(t, "demo")
			before := c.execCount()

			r := c.run(t, append([]string{"exec", "demo", "--"}, tt.cmd...)...)

			r.assertCode(t, exitcode.CodeFailed)
			r.assertStderrContains(t, tt.says)
			if c.execCount() != before {
				t.Errorf("tamp ran %q anyway", c.lastExec(t).Line())
			}
		})
	}
}

func TestRawRunsWhatTampWouldOtherwiseRefuseOrCommentOn(t *testing.T) {
	tests := [][]string{
		{"bench", "update"},
		{"git", "status"},
		{"bench", "new-site", "shop.localhost"},
	}
	for _, cmd := range tests {
		t.Run(strings.Join(cmd, " "), func(t *testing.T) {
			c := sandbox(t)
			c.create(t, "demo")

			r := c.run(t, append([]string{"exec", "--raw", "demo", "--"}, cmd...)...)

			r.assertCode(t, exitcode.CodeOK)
			if got := c.lastExec(t).Line(); got != strings.Join(cmd, " ") {
				t.Errorf("tamp ran %q, want %q", got, strings.Join(cmd, " "))
			}
			// --raw promises both streams belong to the command alone.
			if r.stderr != "" || r.stdout != "" {
				t.Errorf("--raw still said something:\nstdout: %s\nstderr: %s", r.stdout, r.stderr)
			}
		})
	}
}

// Warnings go to stderr; stdout belongs to the command being run.
func TestExecWarnsAboutGitAndNewSiteButStillRunsThem(t *testing.T) {
	tests := []struct {
		cmd  []string
		says string
	}{
		{[]string{"git", "status"}, "host"},
		{[]string{"bench", "new-site", "shop.localhost"}, "tamp site new"},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.cmd, " "), func(t *testing.T) {
			c := sandbox(t)
			c.create(t, "demo")

			r := c.run(t, append([]string{"exec", "demo", "--"}, tt.cmd...)...)

			r.assertCode(t, exitcode.CodeOK)
			r.assertStderrContains(t, tt.says)
			if got := c.lastExec(t).Line(); got != strings.Join(tt.cmd, " ") {
				t.Errorf("tamp ran %q, want %q", got, strings.Join(tt.cmd, " "))
			}
			if strings.Contains(r.stdout, tt.says) {
				t.Errorf("tamp's own words landed on stdout, which belongs to the command:\n%s", r.stdout)
			}
		})
	}
}

// A terminal on both ends: same-size pty, tamp's console raw for the duration.
func TestExecGivesAnInteractiveCommandTheTerminal(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	console := &fakeConsole{width: 120, height: 40}
	c.stdin = console

	r := c.run(t, "exec", "demo", "--", "bash")

	r.assertCode(t, exitcode.CodeOK)
	got := c.lastExec(t)
	if !got.TTY {
		t.Error("tamp ran an interactive command without a pseudo-terminal")
	}
	if want := (engine.ConsoleSize{Width: 120, Height: 40}); got.Size != want {
		t.Errorf("pseudo-terminal size = %+v, want %+v", got.Size, want)
	}
	if !got.Stdin {
		t.Error("tamp ran an interactive command with no standard input attached")
	}
	if console.raw != 1 || console.restored != 1 {
		t.Errorf("console raw %d times, restored %d times; want 1 and 1", console.raw, console.restored)
	}
}

// A pty over a pipe would corrupt output with escapes nothing interprets.
func TestExecWithoutATerminalStillFeedsTheCommandItsInput(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.stdin = strings.NewReader("a line\n")

	r := c.run(t, "exec", "demo", "--", "cat")

	r.assertCode(t, exitcode.CodeOK)
	got := c.lastExec(t)
	if got.TTY {
		t.Error("tamp asked for a pseudo-terminal with nothing but a pipe on this end")
	}
	if !got.Stdin {
		t.Error("tamp did not attach the piped input")
	}
}

// Everything after -- belongs to the command; tamp must not claim its flags.
func TestExecNeedsTheSeparatorAndACommandAfterIt(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"no separator", []string{"exec", "demo", "bench", "--version"}},
		{"nothing after it", []string{"exec", "demo", "--"}},
		{"two environments", []string{"exec", "demo", "other", "--", "bench"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := sandbox(t)
			c.create(t, "demo")

			r := c.run(t, tt.args...)

			r.assertCode(t, exitcode.CodeUsage)
		})
	}
}

// fakeConsole answers Size and Raw without a real pty, recording the switch.
type fakeConsole struct {
	width, height uint
	raw, restored int
}

func (*fakeConsole) Read([]byte) (int, error) { return 0, io.EOF }

func (f *fakeConsole) Size() (uint, uint) { return f.width, f.height }

func (f *fakeConsole) Raw() (func(), error) {
	f.raw++
	return func() { f.restored++ }, nil
}
