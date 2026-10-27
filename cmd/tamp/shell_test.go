package main

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/docker"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/frappe"
	"github.com/zhide915/tamp/internal/router"
)

// shellCommands are the command lines tamp asked the engine to run through
// shell, in order.
func (c *cli) shellCommands() []string {
	var lines []string
	for _, s := range c.docker.Shells {
		lines = append(lines, strings.Join(s.Cmd, " "))
	}
	return lines
}

func assertShells(t *testing.T, c *cli, want ...string) {
	t.Helper()
	if got := c.shellCommands(); !slices.Equal(got, want) {
		t.Errorf("shell commands = %q, want %q", got, want)
	}
}

func TestShellRunsTheCommandAsTheBenchUserInTheBenchDirectory(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev")

	r := c.run(t, "shell", "--", "bench", "migrate")

	r.assertCode(t, exitcode.CodeOK)
	assertShells(t, c, "bench migrate")
	s := c.docker.Shells[0]
	if s.Container != frappeContainer || s.User != "frappe" || s.Dir != frappe.BenchDir {
		t.Errorf("shell = %s as %q in %q, want %s as frappe in %s", s.Container, s.User, s.Dir, frappeContainer, frappe.BenchDir)
	}
	if s.TTY {
		t.Error("a command without a terminal got a TTY")
	}
}

func TestShellExitsWithTheCommandsCodeAndAddsNoErrorLine(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev")
	c.docker.ShellCodes = map[string]int{"run-tests": 7}

	r := c.run(t, "shell", "--", "bench", "run-tests")

	r.assertCode(t, 7)
	if strings.Contains(r.stderr, "error:") {
		t.Errorf("stderr = %q, want no error line: the command printed its own", r.stderr)
	}
}

func TestShellWithATerminalGivesTheCommandATTY(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev")
	c.terminal = true

	c.run(t, "shell", "--", "bench", "console").assertCode(t, exitcode.CodeOK)

	if len(c.docker.Shells) != 1 || !c.docker.Shells[0].TTY {
		t.Errorf("shells = %+v, want one with a TTY", c.docker.Shells)
	}
}

func TestShellWithoutTheDashesExits2WithTheRightForm(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev")

	r := c.run(t, "shell", "-e", "erp-dev", "bench", "build", "--force")

	r.assertCode(t, exitcode.CodeUsage)
	r.assertOneLineError(t, "`tamp shell -e erp-dev -- bench build --force`")
	assertShells(t, c)
}

func TestAForceAfterTheDashesIsTheCommandsNotTamps(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev")

	c.run(t, "shell", "--", "bench", "build", "--force").assertCode(t, exitcode.CodeOK)
	c.run(t, "shell", "--", "bench", "start", "--force").assertCode(t, exitcode.CodeFailed)

	assertShells(t, c, "bench build --force")
}

func TestShellRefusesBenchStartAndUpdateAndForceRunsThem(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev")

	start := c.run(t, "shell", "--", "bench", "start")
	update := c.run(t, "shell", "--", "bench", "update")

	start.assertCode(t, exitcode.CodeFailed)
	start.assertOneLineError(t, "a second copy of the processes", "`tamp shell --force -- bench start`")
	update.assertCode(t, exitcode.CodeFailed)
	update.assertOneLineError(t, "credentials", "then run `tamp shell -- bench --site all migrate`", "`tamp shell --force -- bench update`")
	assertShells(t, c)

	c.run(t, "shell", "--force", "--", "bench", "start").assertCode(t, exitcode.CodeOK)
	assertShells(t, c, "bench start")
}

func TestShellWarnsOnBenchNewSiteAndGitAndRunsThem(t *testing.T) {
	c := sandbox(t)
	dir := c.newIn(t, "erp-dev")

	site := c.run(t, "shell", "--", "bench", "new-site", "blog.localhost")
	git := c.run(t, "shell", "--", "git", "status")

	site.assertCode(t, exitcode.CodeOK)
	if !strings.Contains(site.stderr, "warning:") || !strings.Contains(site.stderr, "`tamp site add <host>`") {
		t.Errorf("stderr = %q, want a warning naming `tamp site add <host>`", site.stderr)
	}
	git.assertCode(t, exitcode.CodeOK)
	if !strings.Contains(git.stderr, "warning:") || !strings.Contains(git.stderr, filepath.Join(dir, "apps", "<app>")) {
		t.Errorf("stderr = %q, want a warning to run git on the host in %s", git.stderr, dir)
	}
	assertShells(t, c, "bench new-site blog.localhost", "git status")

	forced := c.run(t, "shell", "--force", "--", "git", "status")
	if strings.Contains(forced.stderr, "warning:") {
		t.Errorf("stderr = %q, want no warning with --force", forced.stderr)
	}
}

func TestABareShellWithoutATerminalExits2AndRunsNothing(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev")

	r := c.run(t, "shell")

	r.assertCode(t, exitcode.CodeUsage)
	r.assertOneLineError(t, "terminal", "`tamp shell -- <command>`")
	assertShells(t, c)
}

func TestABareShellOnATerminalOpensBashAsTheBenchUserInTheBenchDirectory(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev")
	c.terminal = true

	c.run(t, "shell").assertCode(t, exitcode.CodeOK)

	assertShells(t, c, "bash")
	if s := c.docker.Shells[0]; !s.TTY || s.User != "frappe" || s.Dir != frappe.BenchDir {
		t.Errorf("shell = %+v, want a TTY as frappe in %s", s, frappe.BenchDir)
	}
}

func TestShellOnAStoppedEnvironmentExits1NamesStartAndWarnsOfNothingFirst(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev")
	c.run(t, "stop").assertCode(t, exitcode.CodeOK)
	t.Chdir(c.dir)

	// git draws a warning on a running environment.
	r := c.run(t, "shell", "-e", "erp-dev", "--", "git", "status")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertOneLineError(t, "`tamp start -e erp-dev`")
	assertShells(t, c)
}

func TestLogsReadsTheFrappeContainerByDefault(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev")

	r := c.run(t, "logs")

	r.assertCode(t, exitcode.CodeOK)
	want := []docker.Logs{{Container: frappeContainer}}
	assertLogs(t, c, want)
	if !strings.Contains(r.stdout, frappeContainer) {
		t.Errorf("stdout = %q, want the container's logs", r.stdout)
	}
}

func TestLogsPassesFollowTailAndEachServiceThrough(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev")

	for _, args := range [][]string{
		{"logs", "-f", "--tail", "20"},
		{"logs", "mariadb", "--tail", "0"},
		{"logs", "redis-cache", "--follow"},
		{"logs", "redis-queue"},
		{"logs", "router", "--tail", "5"},
	} {
		c.run(t, args...).assertCode(t, exitcode.CodeOK)
	}

	assertLogs(t, c, []docker.Logs{
		{Container: frappeContainer, Follow: true, Tail: "20"},
		{Container: "erp-dev-mariadb-1", Tail: "0"},
		{Container: "erp-dev-redis-cache-1", Follow: true},
		{Container: "erp-dev-redis-queue-1"},
		{Container: router.Container, Tail: "5"},
	})
}

func TestLogsOfAnUnknownServiceOrABadTailExits2(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev")

	unknown := c.run(t, "logs", "web")
	tail := c.run(t, "logs", "--tail", "-3")

	unknown.assertCode(t, exitcode.CodeUsage)
	unknown.assertOneLineError(t, "web", "frappe, mariadb, redis-cache, redis-queue, router")
	tail.assertCode(t, exitcode.CodeUsage)
	tail.assertOneLineError(t, "-3")
	assertLogs(t, c, nil)
}

func assertLogs(t *testing.T, c *cli, want []docker.Logs) {
	t.Helper()
	var got []docker.Logs
	for _, l := range c.docker.Logged {
		got = append(got, docker.Logs{Container: l.Container, Follow: l.Follow, Tail: l.Tail})
	}
	if !slices.Equal(got, want) {
		t.Errorf("logs = %+v, want %+v", got, want)
	}
}

func TestTheRunAnywayFixKeepsTheWholeCommandAndE(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev")

	r := c.run(t, "shell", "-e", "erp-dev", "--", "bench", "update", "--reset")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertOneLineError(t, "`tamp shell -e erp-dev --force -- bench update --reset`")
}

func TestABareShellWithStdoutRedirectedExits2(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev")
	c.terminalIn = true

	r := c.run(t, "shell")

	r.assertCode(t, exitcode.CodeUsage)
	r.assertOneLineError(t, "terminal")
	assertShells(t, c)
}

func TestTheRefusalsLookPastBenchsGlobalFlags(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev")

	c.run(t, "shell", "--", "bench", "--verbose", "start").assertCode(t, exitcode.CodeFailed)
	c.run(t, "shell", "--", "bench", "--site", "shop.localhost", "update").assertCode(t, exitcode.CodeFailed)

	assertShells(t, c)
}

func TestShellWithStderrRedirectedGetsNoTTYAndABareShellExits2(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev")
	c.terminal, c.plainErr = true, true

	c.run(t, "shell", "--", "bench", "console").assertCode(t, exitcode.CodeOK)
	c.run(t, "shell").assertCode(t, exitcode.CodeUsage)

	if len(c.docker.Shells) != 1 || c.docker.Shells[0].TTY {
		t.Errorf("shells = %+v, want one without a TTY", c.docker.Shells)
	}
}

func TestLogsHelpNamesFrappeAsTheDefaultService(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "logs", "--help")

	r.assertCode(t, exitcode.CodeOK)
	if !strings.Contains(r.stdout, "Without a service, logs shows frappe's logs.") {
		t.Errorf("logs --help = %q, want the default service named", r.stdout)
	}
}
