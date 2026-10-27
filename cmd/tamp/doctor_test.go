package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/frappe"
	"github.com/zhide915/tamp/internal/router"
	"github.com/zhide915/tamp/internal/syncer"
)

// doctor runs `tamp doctor` with args and checks that it asked both fakes
// only to read.
func (c *cli) doctor(t *testing.T, args ...string) result {
	t.Helper()
	calls, execs := len(c.docker.Calls), len(c.docker.Execs)
	syncCalls := 0
	if c.sync != nil {
		syncCalls = len(c.sync.Calls)
	}
	r := c.run(t, append([]string{"doctor"}, args...)...)
	// The one command doctor may run is the read of honcho's exits.
	for _, e := range c.docker.Execs[execs:] {
		if !slices.Equal(e.Cmd, frappe.RecentExits()) {
			t.Errorf("doctor ran %q, want only the read of honcho's exits", e.Cmd)
		}
	}
	assertOnlyReads(t, slices.DeleteFunc(slices.Clone(c.callsSince(calls)), func(call string) bool { return call == "Exec" }))
	if c.sync != nil {
		for _, call := range c.sync.Calls[syncCalls:] {
			if call != "Installed" && !strings.HasPrefix(call, "Peek ") {
				t.Errorf("Mutagen calls = %v, want only Installed and Peek", c.sync.Calls[syncCalls:])
				break
			}
		}
	}
	return r
}

func (r result) assertLine(t *testing.T, want string) {
	t.Helper()
	if !slices.Contains(strings.Split(r.stdout, "\n"), want) {
		t.Errorf("stdout = %q, want the line %q", r.stdout, want)
	}
}

// holdPort points the router's port at a listener of the test's own.
func holdPort(t *testing.T) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	router.Loopback = l.Addr().String()
}

func TestOnAHealthyMachineEveryCheckPassesAndDoctorExits0(t *testing.T) {
	c := sandbox(t)
	c.mutagen()
	c.newEnvironment(t, "--frappe", "version-15", "--site", "shop.localhost")

	r := c.doctor(t)

	r.assertCode(t, exitcode.CodeOK)
	for _, line := range strings.Split(strings.TrimSpace(r.stdout), "\n") {
		if !strings.HasPrefix(line, "✓ ") {
			t.Errorf("line %q does not pass", line)
		}
	}
	for _, want := range []string{"✓ Docker answers", "✓ Docker Compose 2.40.3", "✓ Mutagen " + syncer.Version,
		"✓ the router is running, on port 80", "✓ the environment erp-dev is running",
		"✓ the sync session of erp-dev is watching"} {
		r.assertLine(t, want)
	}
	if !strings.Contains(r.stdout, "✓ git version ") {
		t.Errorf("stdout = %q, want git's version", r.stdout)
	}
	if r.stderr != "" {
		t.Errorf("stderr = %q, want empty", r.stderr)
	}
}

func TestWithDockerUnreachableTheDockerCheckFailsWithAFixAndDoctorExits4(t *testing.T) {
	c := sandbox(t)
	c.newEnvironment(t, "--frappe", "version-15")
	c.docker.PingErr = exitcode.New(exitcode.CodeDockerUnavailable, "Docker is not answering at npipe:////./pipe/docker_engine", "start Docker")

	r := c.doctor(t)

	r.assertCode(t, exitcode.CodeDockerUnavailable)
	r.assertLine(t, "✗ Docker is not answering at npipe:////./pipe/docker_engine: start Docker")
	r.assertLine(t, "! port 80, the router and the environment are not checked, as Docker does not answer: start Docker, then run `tamp doctor` again")

	t.Chdir(c.dir)
	c.doctor(t, "-e", "erp-dev").assertLine(t, "! port 80, the router and the environment are not checked, as Docker does not answer: start Docker, then run `tamp doctor -e erp-dev` again")
	if !strings.Contains(r.stderr, "error: 1 of tamp doctor's checks failed") {
		t.Errorf("stderr = %q, want the count of failed checks", r.stderr)
	}
}

func TestMissingHostGitFailsWithAFix(t *testing.T) {
	c := sandbox(t)
	t.Setenv("PATH", t.TempDir())

	r := c.doctor(t)

	r.assertCode(t, exitcode.CodeFailed)
	r.assertLine(t, "✗ git is not on PATH, and tamp clones apps with it: install git from https://git-scm.com/downloads")
}

func TestTheExitCodeIsThatOfTheFirstFailingCheck(t *testing.T) {
	c := sandbox(t)
	t.Setenv("PATH", t.TempDir())

	r := c.doctor(t, "-e", "nope")

	r.assertCode(t, exitcode.CodeFailed)
	if !strings.Contains(r.stderr, "error: 2 of tamp doctor's checks failed") {
		t.Errorf("stderr = %q, want git and the environment to fail", r.stderr)
	}
}

func TestWithDockerUnreachableAPortHeldIsNotBlamedOnSomethingOtherThanTheRouter(t *testing.T) {
	c := sandbox(t)
	holdPort(t)
	c.docker.PingErr = exitcode.New(exitcode.CodeDockerUnavailable, "Docker is not answering", "start Docker")

	r := c.doctor(t)

	if strings.Contains(r.stdout, "port 80 is taken") {
		t.Errorf("stdout = %q, want port 80 left unchecked", r.stdout)
	}
}

func TestPort80HeldBySomethingOtherThanTheRouterFailsWithAFix(t *testing.T) {
	c := sandbox(t)
	holdPort(t)

	r := c.doctor(t)

	r.assertCode(t, exitcode.CodeFailed)
	if !strings.Contains(r.stdout, "\n✗ port 80 is taken by something other than the router: stop what listens on port 80 (find it with `") {
		t.Errorf("stdout = %q, want port 80 to fail with a fix", r.stdout)
	}
}

func TestPort80HeldByTheRunningRouterPasses(t *testing.T) {
	c := sandbox(t)
	c.newEnvironment(t, "--frappe", "version-15")
	holdPort(t)

	r := c.doctor(t)

	r.assertCode(t, exitcode.CodeOK)
	r.assertLine(t, "✓ the router is running, on port 80")
}

func TestAStoppedRouterWarnsWithTheStartThatWorksFromWhereTheUserStands(t *testing.T) {
	c := sandbox(t)
	c.newEnvironment(t, "--frappe", "version-15")
	c.docker.Stop(router.Project)

	r := c.doctor(t)
	r.assertCode(t, exitcode.CodeOK)
	r.assertLine(t, "✓ port 80 is free for the router")
	r.assertLine(t, "! the router is not running, so no site of erp-dev answers: run `tamp start`, which starts it")

	t.Chdir(c.dir)
	c.doctor(t).assertLine(t, "! the router is not running, so no site of erp-dev answers: run `tamp start -e erp-dev`, which starts it")
}

func TestAnUnhealthySyncSessionWarnsWithTheFixTampStart(t *testing.T) {
	cases := []struct {
		name string
		set  func(s *syncer.Fake, dir string)
		want string
	}{
		{"halted", func(s *syncer.Fake, dir string) {
			s.SetSession(syncer.Session{Name: session, Host: filepath.Join(dir, "apps"), Container: frappeContainer,
				Path: frappe.AppsDir, Status: "halted-on-root-deletion"})
		}, "! the sync session of erp-dev is halted, as a synced folder was deleted: run `tamp start`, which makes it anew"},
		{"paused while running", func(s *syncer.Fake, _ string) {
			_ = s.Pause(context.Background(), session)
		}, "! the sync session of erp-dev is paused: run `tamp start`, which resumes it"},
		{"disconnected", func(s *syncer.Fake, dir string) {
			s.SetSession(syncer.Session{Name: session, Host: filepath.Join(dir, "apps"), Container: frappeContainer,
				Path: frappe.AppsDir, Status: "connecting-beta"})
		}, "! the sync session of erp-dev is connecting to the frappe container: run `tamp start`, which connects it again"},
		{"missing", func(s *syncer.Fake, _ string) {
			_ = s.Terminate(context.Background(), session)
		}, "! erp-dev has no sync session: run `tamp start`, which makes it"},
		{"syncing another folder", func(s *syncer.Fake, dir string) {
			s.SetSession(syncer.Session{Name: session, Host: filepath.Join(dir, "old"), Container: frappeContainer,
				Path: frappe.AppsDir, Status: "watching"})
		}, "! the sync session of erp-dev syncs OLD: run `tamp start`, which makes it sync APPS"},
		{"first copy unfinished", func(s *syncer.Fake, dir string) {
			_ = s.Terminate(context.Background(), session)
			s.SetSession(syncer.Session{Name: syncer.ReplicaName(session), Host: filepath.Join(dir, "apps"),
				Container: frappeContainer, Path: frappe.AppsDir, Status: "watching"})
		}, "! the first copy of apps/ into the container of erp-dev never finished: run `tamp start`, which makes the sync session"},
		{"no daemon", func(s *syncer.Fake, _ string) {
			s.NoDaemon = true
		}, "! Mutagen's daemon is not running, so apps/ of erp-dev does not sync: run `tamp start`, which starts it"},
		{"conflicts", func(s *syncer.Fake, dir string) {
			s.SetSession(syncer.Session{Name: session, Host: filepath.Join(dir, "apps"), Container: frappeContainer,
				Path: frappe.AppsDir, Status: "watching", Conflicts: []syncer.Conflict{{Path: "hrms"}}})
		}, "! the sync session of erp-dev cannot settle apps/hrms: run `tamp start`, which names how to settle each"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := sandbox(t)
			s := c.mutagen()
			dir := c.newEnvironment(t, "--frappe", "version-15")
			tc.set(s, dir)
			want := strings.NewReplacer("OLD", filepath.Join(dir, "old"), "APPS", filepath.Join(dir, "apps")).Replace(tc.want)

			r := c.doctor(t)
			r.assertCode(t, exitcode.CodeOK)
			r.assertLine(t, want)

			t.Chdir(c.dir)
			c.doctor(t, "-e", "erp-dev").assertLine(t, strings.Replace(want, "`tamp start`", "`tamp start -e erp-dev`", 1))
		})
	}
}

func TestAPausedSessionOfAStoppedEnvironmentPasses(t *testing.T) {
	c := sandbox(t)
	c.mutagen()
	c.newEnvironment(t, "--frappe", "version-15")
	c.run(t, "stop").assertCode(t, exitcode.CodeOK)

	r := c.doctor(t)

	r.assertCode(t, exitcode.CodeOK)
	r.assertLine(t, "✓ the environment erp-dev is stopped")
	r.assertLine(t, "✓ the sync session of erp-dev is paused, as its frappe container is stopped")
}

func TestABenchLeftPausedWarnsWithTheFixTampStart(t *testing.T) {
	c := sandbox(t)
	c.newEnvironment(t, "--frappe", "version-15")
	c.docker.SetFile(frappeContainer, frappe.Paused, "")

	r := c.doctor(t)

	r.assertCode(t, exitcode.CodeOK)
	r.assertLine(t, "! the bench of erp-dev keeps its processes down, as a build stopped partway: run `tamp start`, which finishes it")
}

func TestAMissingMutagenWarnsAndDoctorDownloadsNothing(t *testing.T) {
	c := sandbox(t)
	c.mutagen()

	r := c.doctor(t)

	r.assertCode(t, exitcode.CodeOK)
	r.assertLine(t, "! Mutagen "+syncer.Version+" is not installed, or does not run: run `tamp start` in an environment's directory, which downloads it")

	c.docker.PingErr = exitcode.New(exitcode.CodeDockerUnavailable, "Docker is not answering", "start Docker")
	c.doctor(t, "-e", "erp-dev").assertLine(t, "! Mutagen "+syncer.Version+" is not installed, or does not run: run `tamp start -e erp-dev`, which downloads it")
}

func TestOnLinuxDoctorNeedsNoMutagen(t *testing.T) {
	c := sandbox(t)

	r := c.doctor(t)

	r.assertCode(t, exitcode.CodeOK)
	r.assertLine(t, "✓ no Mutagen needed: apps/ is bind-mounted")
}

func TestAComposeThatDoesNotRunFailsWithAFix(t *testing.T) {
	c := sandbox(t)
	c.docker.ComposeErr = exitcode.New(exitcode.CodeDockerUnavailable, "the docker command is not on PATH",
		"install Docker Desktop, or add the docker CLI with its compose plugin to PATH")

	r := c.doctor(t)

	r.assertCode(t, exitcode.CodeDockerUnavailable)
	r.assertLine(t, "✗ the docker command is not on PATH: install Docker Desktop, or add the docker CLI with its compose plugin to PATH")
}

func TestDoctorWithAnUnknownEExits3(t *testing.T) {
	c := sandbox(t)

	r := c.doctor(t, "-e", "nope")

	r.assertCode(t, exitcode.CodeNotFound)
	if !strings.Contains(r.stdout, "✗ no environment named nope has containers") {
		t.Errorf("stdout = %q, want the environment check to fail", r.stdout)
	}
}

func TestInADirectoryWhoseNameAnotherBuiltEnvironmentHoldsDoctorFailsWithTheRenameFix(t *testing.T) {
	c := sandbox(t)
	c.mutagen()
	c.newIn(t, filepath.Join("a", "erp-dev"))
	second := filepath.Join(c.dir, "b", "erp-dev")
	if err := os.MkdirAll(second, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRecipe(t, second, "frappe = \"version-15\"\n")
	t.Chdir(second)

	r := c.doctor(t)

	r.assertCode(t, exitcode.CodeFailed)
	r.assertLine(t, "✗ the environment erp-dev already exists at "+filepath.Join(c.dir, "a", "erp-dev")+": rename the directory")
	if strings.Contains(r.stdout, "sync session") || strings.Contains(r.stdout, "environment erp-dev is") {
		t.Errorf("stdout = %q, want nothing of the other environment's containers or sync", r.stdout)
	}
}

func TestOutsideAnEnvironmentDoctorSaysNoEnvironmentWasChecked(t *testing.T) {
	c := sandbox(t)

	r := c.doctor(t)

	r.assertCode(t, exitcode.CodeOK)
	r.assertLine(t, "✓ no environment checked, as this directory holds no recipe: run `tamp doctor -e <name>` with a name from `tamp list`")
}

func TestAPortHeldByAContainerNamesItInTheFix(t *testing.T) {
	c := sandbox(t)
	holdPort(t)
	c.docker.Publishers = map[int][]string{80: {"web-1"}}

	r := c.doctor(t)

	r.assertCode(t, exitcode.CodeFailed)
	r.assertLine(t, "✗ port 80 is taken by something other than the router: stop the container that publishes port 80 with `docker stop web-1`")
}

func TestTheEnvironmentCheckKeepsItsPlaceAfterTheRouter(t *testing.T) {
	c := sandbox(t)
	c.mutagen()

	r := c.doctor(t, "-e", "nope")

	lines := strings.Split(strings.TrimSpace(r.stdout), "\n")
	if last := lines[len(lines)-1]; !strings.HasPrefix(last, "✗ no environment named nope") {
		t.Errorf("stdout = %q, want the environment line last", r.stdout)
	}
}

func TestAPartlyRunningEnvironmentWithoutMutagensDaemonWarnsThatNothingSyncs(t *testing.T) {
	c := sandbox(t)
	s := c.mutagen()
	c.newEnvironment(t, "--frappe", "version-15")
	if err := c.docker.StopContainer(context.Background(), "erp-dev-mariadb-1"); err != nil {
		t.Fatal(err)
	}
	s.NoDaemon = true

	r := c.doctor(t)

	r.assertLine(t, "! Mutagen's daemon is not running, so apps/ of erp-dev does not sync: run `tamp start`, which starts it")
}

func TestInADirectoryWhoseNameAnotherEnvironmentHoldsTheRouterWarningStartsThatEnvironmentByE(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, filepath.Join("a", "erp-dev"))
	c.docker.Stop(router.Project)
	second := filepath.Join(c.dir, "b", "erp-dev")
	if err := os.MkdirAll(second, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRecipe(t, second, "frappe = \"version-15\"\n")
	t.Chdir(second)

	r := c.doctor(t)

	r.assertLine(t, "! the router is not running, so no site of erp-dev answers: run `tamp start -e erp-dev`, which starts it")
}

func TestDoctorFailsARecipeItCannotReadWithTheFix(t *testing.T) {
	c := sandbox(t)
	dir := c.newIn(t, "erp-dev", "--site", "shop.localhost")
	recipe := filepath.Join(dir, "tamp.toml")
	if err := os.WriteFile(recipe, []byte(readFile(t, recipe)+"colour = \"red\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := c.run(t, "doctor")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertContains(t, `holds the unknown key "sites.colour": remove it from `+recipe)
}

func TestACrashLoopingBenchFailsDoctorWithTheLogsFix(t *testing.T) {
	c := sandbox(t)
	c.newEnvironment(t, "--frappe", "version-15", "--site", "shop.localhost")
	crashing(c)

	r := c.doctor(t)

	r.assertCode(t, exitcode.CodeFailed)
	r.assertLine(t, "✗ "+crashMsg+": see why with `tamp logs frappe`")
	if strings.Contains(r.stdout, "erp-dev is running") {
		t.Errorf("stdout = %q, want the environment not passed as running", r.stdout)
	}
}

func TestAPausedContainerWarnsWithTheUnpauseFix(t *testing.T) {
	c := sandbox(t)
	c.newEnvironment(t, "--frappe", "version-15", "--site", "shop.localhost")
	c.docker.Pause(frappeContainer)

	r := c.doctor(t)

	r.assertCode(t, exitcode.CodeOK)
	r.assertLine(t, "! erp-dev-frappe-1 is paused, so the environment's sites do not answer: unpause it with `docker unpause erp-dev-frappe-1`")
}
