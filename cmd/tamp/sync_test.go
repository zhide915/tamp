package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/frappe"
	"github.com/zhide915/tamp/internal/syncer"
)

const session = "tamp-erp--dev"

// mutagen makes the sandbox build as on Windows and macOS, through the
// Mutagen fake.
func (c *cli) mutagen() *syncer.Fake {
	c.sync = &syncer.Fake{}
	return c.sync
}

// syncCalls are the Mutagen calls that act on a session, in order.
func (c *cli) syncCalls() []string {
	var out []string
	for _, call := range c.sync.Calls {
		if !strings.HasPrefix(call, "Session ") && call != "Installed" {
			out = append(out, call)
		}
	}
	return out
}

func TestOnWindowsAndMacOSStartSyncsAppsWithTheHostWinningAndWaitsForTheFirstSyncBeforeBenchInit(t *testing.T) {
	c := sandbox(t)
	s := c.mutagen()
	var before []string
	s.OnCall = func(call string) {
		if call == "Flush "+session && before == nil {
			before = c.docker.ExecLines()
		}
	}

	dir := c.newEnvironment(t, "--frappe", "version-15", "--site", "shop.localhost")

	want := syncer.Spec{
		Name:      session,
		Host:      filepath.Join(dir, "apps"),
		Container: frappeContainer,
		Path:      frappe.AppsDir,
		Ignore:    []string{"node_modules", "__pycache__", "*.pyc", "/*/build/", "/*/dist/", "*.egg-info/", "/.*/"},
	}
	if len(s.Creates) != 1 {
		t.Fatalf("sessions created = %+v, want one", s.Creates)
	}
	got := s.Creates[0]
	if got.Name != want.Name || got.Host != want.Host || got.Container != want.Container || got.Path != want.Path ||
		!slices.Equal(got.Ignore, want.Ignore) {
		t.Errorf("session = %+v, want %+v", got, want)
	}
	if want := []string{"Install", "Replicate " + session, "Create " + session, "Flush " + session}; !slices.Equal(c.syncCalls(), want) {
		t.Errorf("Mutagen calls = %v, want %v", c.syncCalls(), want)
	}
	if before == nil {
		t.Fatal("start never waited for the sync")
	}
	if slices.ContainsFunc(before, func(l string) bool { return strings.Contains(l, "bench init") }) {
		t.Errorf("bench init ran before the first sync finished: %q", before)
	}
	if !slices.ContainsFunc(c.docker.ExecLines(), func(l string) bool { return strings.Contains(l, "bench init") }) {
		t.Error("bench init never ran")
	}
	up := c.docker.UpsOf("erp-dev")[0].Compose
	if frappeBlock := services(t, up)["frappe"]; strings.Contains(frappeBlock, "./apps") {
		t.Errorf("the frappe service bind-mounts apps/ although Mutagen syncs it:\n%s", frappeBlock)
	}
}

func TestOnLinuxNoSyncSessionIsCreatedAndAppsIsBindMounted(t *testing.T) {
	c := sandbox(t)

	c.newEnvironment(t, "--frappe", "version-15")

	frappeBlock := services(t, c.docker.UpsOf("erp-dev")[0].Compose)["frappe"]
	if !strings.Contains(frappeBlock, "- ./apps:"+frappe.AppsDir+"\n") {
		t.Errorf("the frappe service does not bind-mount apps/:\n%s", frappeBlock)
	}
	r := c.run(t, "status")
	r.assertCode(t, exitcode.CodeOK)
	if !strings.Contains(r.stdout, "\nsync: apps/ is bind-mounted\n") {
		t.Errorf("status = %q, want the line `sync: apps/ is bind-mounted`", r.stdout)
	}
}

func TestEachEnvironmentHasItsOwnSessionWhateverItsNameHolds(t *testing.T) {
	c := sandbox(t)
	s := c.mutagen()

	c.newIn(t, "a_b")
	c.newIn(t, "a-b")

	var names []string
	for _, spec := range s.Creates {
		names = append(names, spec.Name)
	}
	if want := []string{"tamp-a-ub", "tamp-a--b"}; !slices.Equal(names, want) {
		t.Errorf("sessions = %v, want %v", names, want)
	}
}

func TestASecondStartResumesTheSessionAndWaitsForItWithoutCreatingAnother(t *testing.T) {
	c := sandbox(t)
	s := c.mutagen()
	c.newEnvironment(t, "--frappe", "version-15")
	c.run(t, "stop").assertCode(t, exitcode.CodeOK)
	s.Calls = nil

	c.run(t, "start").assertCode(t, exitcode.CodeOK)

	if want := []string{"Resume " + session, "Flush " + session}; !slices.Equal(c.syncCalls(), want) {
		t.Errorf("Mutagen calls = %v, want %v", c.syncCalls(), want)
	}
}

func TestStartRecreatesAHaltedSession(t *testing.T) {
	for _, paused := range []bool{false, true} {
		name := "listed as halted"
		if paused {
			// Mutagen lists it as disconnected until it resumes.
			name = "paused, which hides the halt"
		}
		t.Run(name, func(t *testing.T) {
			c := sandbox(t)
			s := c.mutagen()
			dir := c.newEnvironment(t, "--frappe", "version-15")
			s.SetSession(syncer.Session{Name: session, Host: filepath.Join(dir, "apps"), Container: frappeContainer,
				Path: frappe.AppsDir, Paused: paused, Status: "halted-on-root-deletion"})
			s.Calls = nil

			r := c.run(t, "start")

			r.assertCode(t, exitcode.CodeOK)
			calls := c.syncCalls()
			terminate, replicate, create := slices.Index(calls, "Terminate "+session), slices.Index(calls, "Replicate "+session), slices.Index(calls, "Create "+session)
			if terminate < 0 || replicate < terminate || create < replicate || calls[len(calls)-1] != "Flush "+session {
				t.Errorf("Mutagen calls = %v, want the session terminated, the container copied from the host, a session created, and flushed", calls)
			}
			if !strings.Contains(r.stderr, "warning: Mutagen halted the sync session of erp-dev (halted-on-root-deletion), so the host's files replace what only the container wrote since then\n") {
				t.Errorf("stderr = %q, want a warning that names what the new session replaces", r.stderr)
			}
			if !strings.Contains(r.stderr, "recreating the sync session of erp-dev") {
				t.Errorf("stderr = %q, want it to say the session is recreated", r.stderr)
			}
		})
	}
}

func TestStartRecreatesASessionThatSyncsAnotherFolderAsAfterAMove(t *testing.T) {
	c := sandbox(t)
	s := c.mutagen()
	dir := c.newEnvironment(t, "--frappe", "version-15")
	s.SetSession(syncer.Session{Name: session, Host: filepath.Join(c.dir, "old", "apps"), Container: frappeContainer,
		Path: frappe.AppsDir, Status: "watching"})
	s.Calls = nil

	c.run(t, "start").assertCode(t, exitcode.CodeOK)

	if want := []string{"Terminate " + session, "Replicate " + session, "Create " + session, "Flush " + session}; !slices.Equal(c.syncCalls(), want) {
		t.Errorf("Mutagen calls = %v, want %v", c.syncCalls(), want)
	}
	if got := s.Creates[len(s.Creates)-1].Host; got != filepath.Join(dir, "apps") {
		t.Errorf("the new session syncs %s, want %s", got, filepath.Join(dir, "apps"))
	}
}

func TestStopPausesTheSessionBeforeTheContainersStop(t *testing.T) {
	c := sandbox(t)
	s := c.mutagen()
	c.newEnvironment(t, "--frappe", "version-15")
	s.Calls = nil
	stopped := -1
	s.OnCall = func(call string) {
		if call == "Pause "+session {
			stopped = len(c.docker.Stopped)
		}
	}

	c.run(t, "stop").assertCode(t, exitcode.CodeOK)

	if want := []string{"Flush " + session, "Pause " + session}; !slices.Equal(c.syncCalls(), want) {
		t.Errorf("Mutagen calls = %v, want %v", c.syncCalls(), want)
	}
	if stopped != 0 {
		t.Errorf("containers stopped before the pause = %d, want 0", stopped)
	}
	if len(c.docker.Stopped) == 0 {
		t.Error("stop stopped no container")
	}
}

func TestRemoveTerminatesTheSessionBeforeTheContainersGo(t *testing.T) {
	c := sandbox(t)
	s := c.mutagen()
	c.newEnvironment(t, "--frappe", "version-15")
	s.Calls = nil
	removed := -1
	s.OnCall = func(call string) {
		if call == "Terminate "+session {
			removed = len(c.docker.Removed)
		}
	}

	c.run(t, "remove").assertCode(t, exitcode.CodeNeedsYes)
	if len(c.syncCalls()) != 0 {
		t.Errorf("remove without --yes made the Mutagen calls %v", c.syncCalls())
	}
	c.run(t, "remove", "--yes").assertCode(t, exitcode.CodeOK)

	if want := []string{"Terminate " + session}; !slices.Equal(c.syncCalls(), want) {
		t.Errorf("Mutagen calls = %v, want %v", c.syncCalls(), want)
	}
	if removed != 0 {
		t.Errorf("containers removed before the terminate = %d, want 0", removed)
	}
	if names := s.Names(); len(names) != 0 {
		t.Errorf("sessions left = %v, want none", names)
	}
}

func TestAMutagenThatCannotBeInstalledStopsTheBuildWithAFixBeforeAnyContainerStarts(t *testing.T) {
	c := sandbox(t)
	s := c.mutagen()
	s.InstallErr = exitcode.New(exitcode.CodeFailed, "the Mutagen download has the SHA-256 abc, but tamp pins def",
		"check that no proxy or security software changes downloads from github.com")

	r := c.run(t, "new", "erp-dev", "--frappe", "version-15")

	r.assertCode(t, exitcode.CodeFailed)
	if !strings.Contains(r.stderr, "error: the Mutagen download has the SHA-256 abc, but tamp pins def: check that no proxy or security software changes downloads from github.com, then run `tamp start` in erp-dev") {
		t.Errorf("stderr = %q, want the checksum error with its fix and the start that resumes", r.stderr)
	}
	if slices.Contains(c.docker.Calls, "ComposeUp") {
		t.Error("the build started containers without Mutagen")
	}
}

func TestAMutagenThatCannotRunStopsTheBuildWithAFixAndNoBindMount(t *testing.T) {
	c := sandbox(t)
	s := c.mutagen()
	s.CreateErr = exitcode.New(exitcode.CodeFailed, "Mutagen cannot create the sync session: unable to probe container",
		"fix what Mutagen says")
	r := c.run(t, "new", "erp-dev", "--frappe", "version-15")

	r.assertCode(t, exitcode.CodeFailed)
	if !strings.Contains(r.stderr, "error: Mutagen cannot create the sync session: unable to probe container: fix what Mutagen says, then run `tamp start` in erp-dev") {
		t.Errorf("stderr = %q, want Mutagen's error with its fix and the start that resumes", r.stderr)
	}
	for _, up := range c.docker.UpsOf("erp-dev") {
		if strings.Contains(up.Compose, "./apps") {
			t.Errorf("compose.yaml bind-mounts apps/ after Mutagen failed:\n%s", up.Compose)
		}
	}
	if slices.ContainsFunc(c.docker.ExecLines(), func(l string) bool { return strings.Contains(l, "bench init") }) {
		t.Error("bench init ran without a sync")
	}
}

func TestStatusShowsTheSyncState(t *testing.T) {
	c := sandbox(t)
	c.mutagen()
	c.newEnvironment(t, "--frappe", "version-15")

	syncLine := func(args ...string) string {
		t.Helper()
		r := c.run(t, append([]string{"status"}, args...)...)
		r.assertCode(t, exitcode.CodeOK)
		for _, line := range strings.Split(r.stdout, "\n") {
			if strings.HasPrefix(line, "sync: ") {
				return line
			}
		}
		t.Fatalf("status = %q, want a sync line", r.stdout)
		return ""
	}
	if got := syncLine(); got != "sync: watching" {
		t.Errorf("running: %q, want `sync: watching`", got)
	}
	c.run(t, "stop").assertCode(t, exitcode.CodeOK)
	t.Chdir(c.dir)
	if got := syncLine("-e", "erp-dev"); got != "sync: paused" {
		t.Errorf("stopped: %q, want `sync: paused`", got)
	}
	t.Chdir(filepath.Join(c.dir, "erp-dev"))
	c.run(t, "remove", "--yes").assertCode(t, exitcode.CodeOK)
	if got := syncLine(); got != "sync: none: run `tamp start`, which makes it" {
		t.Errorf("removed: %q, want `sync: none: run `tamp start`, which makes it`", got)
	}
}

func TestStartNamesEachConflictTheSyncCannotSettleWithAFixThatWorksUnderE(t *testing.T) {
	c := sandbox(t)
	s := c.mutagen()
	dir := c.newEnvironment(t, "--frappe", "version-15")
	s.SetSession(syncer.Session{Name: session, Host: filepath.Join(dir, "apps"), Container: frappeContainer,
		Path: frappe.AppsDir, Status: "watching", Conflicts: []syncer.Conflict{{Path: "acme", HostDeleted: true}}})
	t.Chdir(c.dir)

	r := c.run(t, "start", "-e", "erp-dev")

	r.assertCode(t, exitcode.CodeOK)
	if !strings.Contains(r.stderr, "warning: the sync cannot delete apps/acme in the container, because it holds files that the sync skips: delete them with `tamp shell -e erp-dev -- rm -rf apps/acme`\n") {
		t.Errorf("stderr = %q, want the conflict named with a fix under -e", r.stderr)
	}
	status := c.run(t, "status", "-e", "erp-dev")
	if !strings.Contains(status.stdout, "sync: watching, and cannot settle apps/acme\n") {
		t.Errorf("status = %q, want the conflict named", status.stdout)
	}
}

func TestASessionThatSyncsTheSameFolderSpelledAnotherWayIsKept(t *testing.T) {
	c := sandbox(t)
	s := c.mutagen()
	dir := c.newEnvironment(t, "--frappe", "version-15")
	sep := string(filepath.Separator)
	spellings := []string{dir + sep + "." + sep + "apps"}
	if runtime.GOOS == "windows" {
		spellings = append(spellings, strings.ToUpper(dir)+sep+"apps")
	}
	for _, host := range spellings {
		s.SetSession(syncer.Session{Name: session, Host: host, Container: frappeContainer, Path: frappe.AppsDir, Status: "watching"})
		s.Calls = nil

		c.run(t, "start").assertCode(t, exitcode.CodeOK)

		if want := []string{"Resume " + session, "Flush " + session}; !slices.Equal(c.syncCalls(), want) {
			t.Errorf("session at %s: Mutagen calls = %v, want %v", host, c.syncCalls(), want)
		}
	}
}

func TestStopRemoveAndStatusStillWorkWhenMutagenCannotRun(t *testing.T) {
	c := sandbox(t)
	s := c.mutagen()
	dir := c.newEnvironment(t, "--frappe", "version-15", "--site", "shop.localhost")
	s.Err = &syncer.RunError{Bin: "mutagen", Err: errors.New("the file is corrupt")}
	const trouble = "Mutagen cannot run: the file is corrupt: run `tamp start`, which downloads it again"

	status := c.run(t, "status")
	status.assertCode(t, exitcode.CodeOK)
	if !strings.Contains(status.stdout, "\nsync: unknown: "+trouble+"\nsites:\n  http://shop.localhost\n") {
		t.Errorf("status = %q, want the sync unknown, and the sites", status.stdout)
	}

	stop := c.run(t, "stop")
	stop.assertCode(t, exitcode.CodeOK)
	if !strings.Contains(stop.stderr, "warning: cannot pause the sync session of erp-dev: "+trouble+"\n") {
		t.Errorf("stderr = %q, want a warning that names the fix", stop.stderr)
	}
	if !slices.Equal(c.docker.Stopped, containersOf("erp-dev")) {
		t.Errorf("stopped = %v, want every container", c.docker.Stopped)
	}

	remove := c.run(t, "remove", "--yes")
	remove.assertCode(t, exitcode.CodeOK)
	if !strings.Contains(remove.stderr, "warning: cannot terminate the sync session of erp-dev: Mutagen cannot run: the file is corrupt: check that your security software lets programs in . run, then run `tamp remove --yes` in "+dir+"\n") {
		t.Errorf("stderr = %q, want a warning that names the fix", remove.stderr)
	}
	if !slices.Equal(c.docker.Removed, containersOf("erp-dev")) {
		t.Errorf("removed = %v, want every container", c.docker.Removed)
	}
}

func TestAFreshCloneGetsItsNodePackagesEvenWhereTheContainerKeptNodeModules(t *testing.T) {
	c := sandbox(t)
	c.mutagen()
	dir := c.newEnvironment(t, "--frappe", "version-15")
	if err := os.RemoveAll(filepath.Join(dir, "apps", "frappe")); err != nil {
		t.Fatal(err)
	}
	// The sync deletes the clone's files in the container, but keeps the
	// node_modules it skips.
	for _, marker := range []string{frappe.NodeInClone("frappe", true), frappe.AssetsInClone("frappe", true)} {
		c.docker.RemoveFile(frappeContainer, marker)
	}
	if _, ok := c.docker.File(frappeContainer, frappe.NodeInstalled("frappe")); !ok {
		t.Fatal("the first build left no node marker in node_modules")
	}
	execs := len(c.docker.Execs)

	c.run(t, "start").assertCode(t, exitcode.CodeOK)

	yarn := strings.Join(frappe.NodePackages("frappe"), " ")
	if !slices.Contains(c.docker.ExecLines()[execs:], yarn) {
		t.Errorf("execs = %q, want the node packages of the fresh clone installed", c.docker.ExecLines()[execs:])
	}
}

func TestRemoveAlsoTerminatesACopyThatAKilledStartLeftAndStatusNamesIt(t *testing.T) {
	c := sandbox(t)
	s := c.mutagen()
	dir := c.newEnvironment(t, "--frappe", "version-15")
	c.run(t, "remove", "--yes").assertCode(t, exitcode.CodeOK)
	replica := syncer.ReplicaName(session)
	s.SetSession(syncer.Session{Name: replica, Host: filepath.Join(dir, "apps"), Container: frappeContainer, Path: frappe.AppsDir, Paused: true})

	status := c.run(t, "status")
	if !strings.Contains(status.stdout, "\nsync: none, because its first copy into the container did not finish: run `tamp start`, which makes it\n") {
		t.Errorf("status = %q, want the unfinished copy named", status.stdout)
	}
	s.Calls = nil
	c.run(t, "remove", "--yes").assertCode(t, exitcode.CodeOK)

	if want := []string{"Terminate " + replica}; !slices.Equal(c.syncCalls(), want) {
		t.Errorf("Mutagen calls = %v, want %v", c.syncCalls(), want)
	}
	if names := s.Names(); len(names) != 0 {
		t.Errorf("sessions left = %v, want none", names)
	}
}

func TestRemoveDownloadsAMissingMutagenToEndTheSession(t *testing.T) {
	c := sandbox(t)
	s := c.mutagen()
	c.newEnvironment(t, "--frappe", "version-15")
	s.SetInstalled(false)
	s.Calls = nil

	c.run(t, "remove", "--yes").assertCode(t, exitcode.CodeOK)

	if want := []string{"Install", "Terminate " + session}; !slices.Equal(c.syncCalls(), want) {
		t.Errorf("Mutagen calls = %v, want %v", c.syncCalls(), want)
	}
	if names := s.Names(); len(names) != 0 {
		t.Errorf("sessions left = %v, want none", names)
	}
}

func TestRemoveThatCannotEndTheSessionStillRemovesAndNamesAFixThatBuildsNothing(t *testing.T) {
	c := sandbox(t)
	s := c.mutagen()
	dir := c.newIn(t, "erp-dev")
	s.SetInstalled(false)
	s.InstallErr = exitcode.New(exitcode.CodeFailed, "cannot download Mutagen", "check this machine's connection to github.com")
	t.Chdir(c.dir)

	r := c.run(t, "remove", "-e", "erp-dev", "--yes")

	r.assertCode(t, exitcode.CodeOK)
	if !strings.Contains(r.stderr, "warning: cannot terminate the sync session of erp-dev: cannot download Mutagen: check this machine's connection to github.com, then run `tamp remove --yes` in "+dir+"\n") {
		t.Errorf("stderr = %q, want a fix that reruns remove in the directory", r.stderr)
	}
	if !slices.Equal(c.docker.Removed, containersOf("erp-dev")) {
		t.Errorf("removed = %v, want every container", c.docker.Removed)
	}

	// The fix: remove again in the directory, with Mutagen back, builds nothing.
	s.InstallErr = nil
	t.Chdir(dir)
	ups := len(c.docker.Ups)
	c.run(t, "remove", "--yes").assertCode(t, exitcode.CodeOK)
	if names := s.Names(); len(names) != 0 || len(c.docker.Ups) != ups {
		t.Errorf("sessions left = %v and compose ups = %d, want none and no new up", names, len(c.docker.Ups)-ups)
	}
}

func TestStartNamesAnUnfinishedTempCloneAndSkipsOtherDotFolders(t *testing.T) {
	c := sandbox(t)
	dir := c.newEnvironment(t, "--frappe", "version-15")
	for _, folder := range []string{".frappe-123456", ".vscode"} {
		if err := os.Mkdir(filepath.Join(dir, "apps", folder), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	r := c.run(t, "start")

	r.assertCode(t, exitcode.CodeOK)
	if !strings.Contains(r.stderr, "warning: "+filepath.Join(dir, "apps", ".frappe-123456")+" is a clone that tamp did not finish: delete the folder\n") {
		t.Errorf("stderr = %q, want the unfinished clone named", r.stderr)
	}
	if strings.Contains(r.stderr, ".vscode") || strings.Contains(r.stderr, "app add") {
		t.Errorf("stderr = %q, want no warning for other dot folders", r.stderr)
	}
}

func TestStopOfContainersStoppedOutsideTampPausesTheSessionWithoutWaitingForTheSync(t *testing.T) {
	c := sandbox(t)
	s := c.mutagen()
	c.newEnvironment(t, "--frappe", "version-15")
	c.docker.Stop("erp-dev")
	s.Calls = nil

	c.run(t, "stop").assertCode(t, exitcode.CodeOK)

	if want := []string{"Pause " + session}; !slices.Equal(c.syncCalls(), want) {
		t.Errorf("Mutagen calls = %v, want %v", c.syncCalls(), want)
	}
}
