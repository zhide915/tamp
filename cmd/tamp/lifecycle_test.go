package main

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/docker"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/frappe"
	"github.com/zhide915/tamp/internal/router"
)

func containersOf(name string) []string {
	var out []string
	for _, service := range []string{"frappe", "mariadb", "redis-cache", "redis-queue"} {
		out = append(out, name+"-"+service+"-1")
	}
	return out
}

func (c *cli) inspect(t *testing.T, name string) docker.Container {
	t.Helper()
	got, err := c.docker.Inspect(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// callsSince is the Docker calls made after the first n.
func (c *cli) callsSince(n int) []string { return c.docker.Calls[n:] }

func assertOnlyReads(t *testing.T, calls []string) {
	t.Helper()
	for _, call := range calls {
		if !slices.Contains([]string{"Ping", "ComposeVersion", "Publishing", "Containers", "AllContainers", "Inspect", "InspectVolume", "ReadFile"}, call) {
			t.Errorf("Docker calls = %v, want only reads", calls)
			return
		}
	}
}

func TestRemoveWithoutYesExits5AndChangesNothing(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev", "--site", "shop.localhost")
	calls := len(c.docker.Calls)
	before := c.caddyfile(t)

	r := c.run(t, "remove")

	r.assertCode(t, exitcode.CodeNeedsYes)
	r.assertOneLineError(t, "erp-dev", "`tamp remove --yes`")
	assertOnlyReads(t, c.callsSince(calls))
	if got := c.caddyfile(t); got != before {
		t.Errorf("the Caddyfile changed:\n%s", got)
	}
}

func TestRemoveDetachesTheRouterThenDeletesTheContainersNetworkAndVolumesAndKeepsTheDirectory(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "other", "--site", "blog.localhost")
	dir := c.newIn(t, "erp-dev", "--site", "shop.localhost")
	calls := len(c.docker.Calls)

	r := c.run(t, "remove", "--yes")

	r.assertCode(t, exitcode.CodeOK)
	assertNetworks(t, "disconnects", c.docker.Disconnects, []docker.Network{{Network: "erp-dev_default", Container: "tamp-router"}})
	after := c.callsSince(calls)
	if d, n := slices.Index(after, "DisconnectNetwork"), slices.Index(after, "RemoveNetwork"); d < 0 || n < d {
		t.Errorf("Docker calls = %v, want DisconnectNetwork before RemoveNetwork", after)
	}
	if !slices.Equal(c.docker.Removed, containersOf("erp-dev")) {
		t.Errorf("removed containers = %v, want %v", c.docker.Removed, containersOf("erp-dev"))
	}
	if !slices.Equal(c.docker.RemovedNetworks, []string{"erp-dev_default"}) {
		t.Errorf("removed networks = %v, want erp-dev_default", c.docker.RemovedNetworks)
	}
	if want := []string{"other_bench", "other_db", "tamp-cache"}; !slices.Equal(c.docker.VolumeNames(), want) {
		t.Errorf("volumes left = %v, want %v", c.docker.VolumeNames(), want)
	}
	for _, path := range []string{"tamp.toml", ".gitignore", filepath.Join("apps", "frappe", ".git")} {
		if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
			t.Errorf("%s is gone from the directory: %v", path, err)
		}
	}
	caddyfile := c.caddyfile(t)
	if _, ok := siteBlocks(caddyfile)["shop.localhost"]; ok {
		t.Errorf("the Caddyfile still routes the removed shop.localhost:\n%s", caddyfile)
	}
	assertRoutedTo(t, caddyfile, "blog.localhost", "other-frappe-1")
	if !c.inspect(t, router.Container).Running {
		t.Error("remove stopped the router")
	}
	if !strings.Contains(r.stdout, dir) {
		t.Errorf("stdout = %q, want it to name the directory that remains", r.stdout)
	}
}

func TestListPrintsEachEnvironmentWithItsSitesNestedIncludingAStoppedOne(t *testing.T) {
	c := sandbox(t)
	erp := c.newIn(t, "erp-dev", "--site", "shop.localhost", "--site", "erp.localhost")
	other := c.newIn(t, "other", "--site", "blog.localhost")
	c.run(t, "stop").assertCode(t, exitcode.CodeOK)
	t.Chdir(c.dir)

	r := c.run(t, "list")

	r.assertCode(t, exitcode.CodeOK)
	lines := strings.Split(strings.TrimSuffix(r.stdout, "\n"), "\n")
	want := []string{
		`^erp-dev +running +` + regexp.QuoteMeta(erp) + `$`,
		`^  http://shop\.localhost$`,
		`^  http://erp\.localhost$`,
		`^other +stopped +` + regexp.QuoteMeta(other) + `$`,
		`^  http://blog\.localhost$`,
	}
	if len(lines) != len(want) {
		t.Fatalf("stdout =\n%s\nwant %d lines", r.stdout, len(want))
	}
	for i, w := range want {
		if !regexp.MustCompile(w).MatchString(lines[i]) {
			t.Errorf("line %d = %q, want %s", i+1, lines[i], w)
		}
	}
}

func TestStatusWithEFromAnotherDirectoryPrintsThatEnvironmentsContainersAndSiteURLs(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev", "--site", "shop.localhost")
	c.newIn(t, "other", "--site", "blog.localhost")
	t.Chdir(c.dir)

	r := c.run(t, "status", "-e", "erp-dev")

	r.assertCode(t, exitcode.CodeOK)
	for _, name := range containersOf("erp-dev") {
		if !regexp.MustCompile(`(?m)^  ` + name + ` +running$`).MatchString(r.stdout) {
			t.Errorf("stdout lacks %s as running:\n%s", name, r.stdout)
		}
	}
	if !strings.Contains(r.stdout, "\n  http://shop.localhost\n") {
		t.Errorf("stdout lacks the site URL:\n%s", r.stdout)
	}
	if strings.Contains(r.stdout, "other-") || strings.Contains(r.stdout, "blog.localhost") {
		t.Errorf("stdout names the other environment:\n%s", r.stdout)
	}
}

var environmentCommands = [][]string{{"start"}, {"stop"}, {"remove", "--yes"}, {"status"}, {"open"}, {"context"}}

func TestNoEOutsideAnEnvironmentDirectoryExits2WithTheFixUseE(t *testing.T) {
	for _, args := range environmentCommands {
		t.Run(args[0], func(t *testing.T) {
			c := sandbox(t)
			c.newIn(t, "erp-dev", "--site", "shop.localhost")
			t.Chdir(c.dir)
			calls := len(c.docker.Calls)

			r := c.run(t, args...)

			r.assertCode(t, exitcode.CodeUsage)
			r.assertOneLineError(t, "use `-e <name>`")
			if len(c.callsSince(calls)) > 0 {
				t.Errorf("Docker calls = %v, want none", c.callsSince(calls))
			}
		})
	}
}

func TestAnUnknownENameExits3(t *testing.T) {
	for _, args := range environmentCommands {
		t.Run(args[0], func(t *testing.T) {
			c := sandbox(t)
			c.newIn(t, "erp-dev", "--site", "shop.localhost")
			calls := len(c.docker.Calls)

			r := c.run(t, append(args, "-e", "nope")...)

			r.assertCode(t, exitcode.CodeNotFound)
			r.assertOneLineError(t, "nope", "`tamp list`")
			assertOnlyReads(t, c.callsSince(calls))
		})
	}
}

func TestStartWithEBuildsThatEnvironmentFromAnotherDirectory(t *testing.T) {
	c := sandbox(t)
	dir := c.newIn(t, "erp-dev", "--site", "shop.localhost")
	writeRecipe(t, dir, "frappe = \"version-15\"\n\n[[sites]]\n  host = \"shop.localhost\"\n\n[[sites]]\n  host = \"erp.localhost\"\n")
	t.Chdir(c.dir)

	c.run(t, "start", "-e", "erp-dev").assertCode(t, exitcode.CodeOK)

	if !slices.ContainsFunc(benchExecs(c.docker.Execs), func(l string) bool { return strings.HasPrefix(l, "bench new-site erp.localhost ") }) {
		t.Errorf("execs = %q, want erp.localhost created", benchExecs(c.docker.Execs))
	}
}

func TestASameNamedEnvironmentAtAnotherPathThatStillHasARecipeMakesStartExit1(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, filepath.Join("a", "erp-dev"), "--site", "shop.localhost")
	second := filepath.Join(c.dir, "b", "erp-dev")
	if err := os.MkdirAll(second, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRecipe(t, second, "frappe = \"version-15\"\n")
	t.Chdir(second)
	calls := len(c.docker.Calls)

	r := c.run(t, "start")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertOneLineError(t, filepath.Join(c.dir, "a", "erp-dev"), "rename the directory")
	assertOnlyReads(t, c.callsSince(calls))
}

func TestAMovedDirectorysStartRecreatesTheContainersWithTheNewPathAndKeepsTheVolumes(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, filepath.Join("a", "erp-dev"), "--site", "shop.localhost")
	// t.Chdir would hold the directory open, and Windows renames no open
	// directory; the earlier t.Chdir restores the working directory.
	if err := os.Chdir(c.dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(c.dir, "a"), filepath.Join(c.dir, "b")); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(c.dir, "b", "erp-dev")
	t.Chdir(moved)
	execs := len(c.docker.Execs)

	r := c.run(t, "start")

	r.assertCode(t, exitcode.CodeOK)
	if !strings.Contains(r.stderr, "warning: erp-dev moved from "+filepath.Join(c.dir, "a", "erp-dev")) {
		t.Errorf("stderr = %q, want a warning that erp-dev moved", r.stderr)
	}
	for _, name := range containersOf("erp-dev") {
		if got := c.inspect(t, name).Labels["tamp.path"]; got != moved {
			t.Errorf("%s has tamp.path %q, want %q", name, got, moved)
		}
	}
	if len(c.docker.Removed)+len(c.docker.RemovedVolumes) > 0 {
		t.Errorf("start removed containers %v and volumes %v, want none", c.docker.Removed, c.docker.RemovedVolumes)
	}
	if again := benchExecs(c.docker.Execs[execs:]); len(again) > 0 {
		t.Errorf("start ran %q, want the bench and site kept", again)
	}
}

func TestAVersionLabelMismatchExits1WithTheFixAndBuildsNothing(t *testing.T) {
	c := sandbox(t)
	dir := c.newIn(t, "erp-dev")
	writeRecipe(t, dir, "frappe = \"version-16\"\n")
	calls := len(c.docker.Calls)

	r := c.run(t, "start")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertOneLineError(t, "version-15", versionFix+", then run `tamp remove --yes` and `tamp start`")
	if !strings.HasSuffix(r.stderr, "and `tamp start`\n") {
		t.Errorf("stderr = %q, want the fix to end at `tamp start`", r.stderr)
	}
	assertOnlyReads(t, c.callsSince(calls))
	if got := git(t, filepath.Join(dir, "apps", "frappe"), "branch", "--show-current"); got != "version-15" {
		t.Errorf("apps/frappe is on %s, want it untouched on version-15", got)
	}
}

const versionFix = "run `tamp stop`, fetch and check out version-16 with `git -C apps/frappe remote set-branches --add upstream version-16`, `git -C apps/frappe fetch --depth 1 upstream version-16` and `git -C apps/frappe checkout version-16`"

func TestFollowingTheVersionMismatchFixOnTampsShallowCloneBuildsTheRecipesBranch(t *testing.T) {
	c := sandbox(t)
	sync := c.mutagen()
	dir := c.newIn(t, "erp-dev")
	writeRecipe(t, dir, "frappe = \"version-16\"\n")
	r := c.run(t, "start", "--quiet")
	r.assertCode(t, exitcode.CodeFailed)

	// The user runs each command the fix names, as printed, in order.
	commands := regexp.MustCompile("`([^`]*)`").FindAllStringSubmatch(r.stderr, -1)
	if len(commands) != 6 {
		t.Fatalf("stderr = %q, want a fix of six commands", r.stderr)
	}
	for _, cmd := range commands {
		words := strings.Fields(cmd[1])
		switch words[0] {
		case "git":
			// On Windows, git writing packs into a watching sync fails.
			if s, err := sync.Peek(context.Background(), "tamp-erp--dev"); err != nil || s == nil || !s.Paused {
				t.Errorf("the sync session = %v, %v when the fix runs git, want it paused", s, err)
			}
			git(t, dir, words[1:]...)
		case "tamp":
			c.run(t, words[1:]...).assertCode(t, exitcode.CodeOK)
		default:
			t.Fatalf("the fix names %q, want only git and tamp", cmd[1])
		}
	}

	if got := git(t, filepath.Join(dir, "apps", "frappe"), "branch", "--show-current"); got != "version-16" {
		t.Errorf("apps/frappe is on %s, want version-16", got)
	}
	if got := git(t, filepath.Join(dir, "apps", "frappe"), "rev-parse", "--abbrev-ref", "version-16@{upstream}"); got != "upstream/version-16" {
		t.Errorf("version-16 tracks %q, want upstream/version-16", got)
	}
	v, err := c.docker.InspectVolume(context.Background(), "erp-dev_bench")
	if err != nil || v.Labels["tamp.frappe"] != "version-16" {
		t.Errorf("bench volume = %v, %v, want one built for version-16", v, err)
	}
}

func TestStartInADirectoryWhoseNameIsNotAValidEnvironmentNameExits1(t *testing.T) {
	c := sandbox(t)
	dir := filepath.Join(c.dir, "Erp Dev")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRecipe(t, dir, "frappe = \"version-15\"\n")
	t.Chdir(dir)

	r := c.run(t, "start")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertOneLineError(t, `"Erp Dev" is not a valid environment name`)
	assertOnlyReads(t, c.docker.Calls)
}

func TestStopStopsOnlyThatEnvironmentsContainers(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "other", "--site", "blog.localhost")
	c.newIn(t, "erp-dev", "--site", "shop.localhost")

	r := c.run(t, "stop")

	r.assertCode(t, exitcode.CodeOK)
	if !slices.Equal(c.docker.Stopped, containersOf("erp-dev")) {
		t.Errorf("stopped = %v, want %v", c.docker.Stopped, containersOf("erp-dev"))
	}
	for _, name := range containersOf("erp-dev") {
		if c.inspect(t, name).Running {
			t.Errorf("%s still runs", name)
		}
	}
	for _, name := range append(containersOf("other"), router.Container) {
		if !c.inspect(t, name).Running {
			t.Errorf("%s stopped too", name)
		}
	}
}

func TestAfterStopTheStoppedEnvironmentsSitesLeaveTheCaddyfile(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "other", "--site", "blog.localhost")
	c.newIn(t, "erp-dev", "--site", "shop.localhost")
	// Linux CI's engine still listed the stopped containers as running.
	c.docker.StaleList = true
	execs := len(c.docker.Execs)

	c.run(t, "stop").assertCode(t, exitcode.CodeOK)

	caddyfile := c.caddyfile(t)
	if _, ok := siteBlocks(caddyfile)["shop.localhost"]; ok {
		t.Errorf("the Caddyfile still routes the stopped shop.localhost:\n%s", caddyfile)
	}
	assertRoutedTo(t, caddyfile, "blog.localhost", "other-frappe-1")
	if got := routerExecs(c.docker.Execs[execs:]); !slices.Equal(got, []string{reload}) {
		t.Errorf("router execs = %q, want one %q", got, reload)
	}
}

// Port 80 taken would otherwise fail a stop that already stopped everything.
func TestStopAndRemoveNeverStartAStoppedRouter(t *testing.T) {
	for _, args := range [][]string{{"stop"}, {"remove", "--yes"}} {
		t.Run(args[0], func(t *testing.T) {
			c := sandbox(t)
			c.newIn(t, "erp-dev", "--site", "shop.localhost")
			c.docker.Stop(router.Project)
			ups, execs := len(c.docker.UpsOf(router.Project)), len(c.docker.Execs)

			c.run(t, args...).assertCode(t, exitcode.CodeOK)

			if len(c.docker.UpsOf(router.Project)) != ups {
				t.Error("the stopped router was brought up")
			}
			if got := routerExecs(c.docker.Execs[execs:]); len(got) > 0 {
				t.Errorf("router execs = %q, want none on a stopped router", got)
			}
			if _, ok := siteBlocks(c.caddyfile(t))["shop.localhost"]; ok {
				t.Error("the Caddyfile still routes shop.localhost")
			}
		})
	}
}

func TestADollarInTheParentPathSurvivesComposeInterpolation(t *testing.T) {
	c := sandbox(t)

	dir := c.newIn(t, filepath.Join("pay$HOME", "erp-dev"))

	compose := c.docker.UpsOf("erp-dev")[0].Compose
	if !strings.Contains(compose, "pay$$HOME") {
		t.Errorf("compose.yaml does not escape the $ as $$:\n%s", compose)
	}
	if got := c.inspect(t, frappeContainer).Labels["tamp.path"]; got != dir {
		t.Errorf("tamp.path = %q, want %q", got, dir)
	}
}

func TestARunningRouterWhoseComposeFileChangedIsBroughtUpAgain(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev", "--site", "shop.localhost")
	file := filepath.Join(router.Dir(c.home), "compose.yaml")
	want := readFile(t, file)
	if err := os.WriteFile(file, []byte("# an older template\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c.run(t, "start").assertCode(t, exitcode.CodeOK)

	ups := c.docker.UpsOf(router.Project)
	if len(ups) != 2 || ups[1].Compose != want {
		t.Errorf("router compose ups = %d, want a second one with the current template", len(ups))
	}
}

func TestEveryCommandFromADirectoryRefusesASameNamedEnvironmentAtAnotherPathThatStillHasARecipe(t *testing.T) {
	for _, args := range [][]string{{"stop"}, {"remove", "--yes"}, {"status"}, {"open"}, {"context"}} {
		t.Run(args[0], func(t *testing.T) {
			c := sandbox(t)
			c.newIn(t, filepath.Join("a", "erp-dev"), "--site", "shop.localhost")
			second := filepath.Join(c.dir, "b", "erp-dev")
			if err := os.MkdirAll(second, 0o755); err != nil {
				t.Fatal(err)
			}
			writeRecipe(t, second, "frappe = \"version-15\"\n\n[[sites]]\n  host = \"shop.localhost\"\n")
			t.Chdir(second)
			calls := len(c.docker.Calls)

			r := c.run(t, args...)

			r.assertCode(t, exitcode.CodeFailed)
			r.assertOneLineError(t, filepath.Join(c.dir, "a", "erp-dev"), "rename the directory")
			assertOnlyReads(t, c.callsSince(calls))
			if len(c.opened) > 0 {
				t.Errorf("opened = %q, want nothing", c.opened)
			}
		})
	}
}

// A moved directory is the same environment, as start treats it.
func TestRemoveInAMovedDirectoryRemovesTheEnvironment(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, filepath.Join("a", "erp-dev"), "--site", "shop.localhost")
	if err := os.Chdir(c.dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(c.dir, "a"), filepath.Join(c.dir, "b")); err != nil {
		t.Fatal(err)
	}
	t.Chdir(filepath.Join(c.dir, "b", "erp-dev"))

	r := c.run(t, "remove", "--yes")

	r.assertCode(t, exitcode.CodeOK)
	if !strings.Contains(r.stderr, "warning: erp-dev moved from "+filepath.Join(c.dir, "a", "erp-dev")) {
		t.Errorf("stderr = %q, want a warning that erp-dev moved", r.stderr)
	}
	if !slices.Equal(c.docker.Removed, containersOf("erp-dev")) {
		t.Errorf("removed containers = %v, want %v", c.docker.Removed, containersOf("erp-dev"))
	}
}

func TestWithEAVersionMismatchFixNamesTheDirectoryBecauseEFindsNothingAfterTheRemove(t *testing.T) {
	c := sandbox(t)
	dir := c.newIn(t, "erp-dev")
	writeRecipe(t, dir, "frappe = \"version-16\"\n")
	t.Chdir(c.dir)

	r := c.run(t, "start", "-e", "erp-dev")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertOneLineError(t, "in "+dir+", "+versionFix+", then run `tamp remove --yes` and `tamp start`")
	if strings.Contains(r.stderr, "-e erp-dev") {
		t.Errorf("stderr = %q, want no command with -e", r.stderr)
	}
}

func TestNewWithANameAnotherEnvironmentHoldsExits1AndWritesNothing(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, filepath.Join("a", "erp-dev"))
	t.Chdir(c.dir)

	r := c.run(t, "new", filepath.Join("b", "erp-dev"), "--frappe", "version-15")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertOneLineError(t, filepath.Join(c.dir, "a", "erp-dev"), "rename the directory")
	if strings.Contains(r.stderr, "tamp start") {
		t.Errorf("stderr = %q, want no `tamp start` in the fix: nothing was written", r.stderr)
	}
	assertNotExist(t, filepath.Join(c.dir, "b", "erp-dev"))
}

func TestCaddysReloadLogReachesStderrOnlyWhenTheReloadFails(t *testing.T) {
	const caddyLog = `{"level":"info","msg":"using config from file"}` + "\n"
	c := sandbox(t)
	c.newIn(t, "erp-dev", "--site", "shop.localhost")
	c.docker.ExecOutput = map[string]string{"caddy reload": caddyLog}

	ok := c.run(t, "stop")

	ok.assertCode(t, exitcode.CodeOK)
	if strings.Contains(ok.stderr, caddyLog) {
		t.Errorf("stderr = %q, want no Caddy log from a reload that worked", ok.stderr)
	}

	c.docker.ExecFails = map[string]error{"caddy reload": exitcode.New(exitcode.CodeFailed,
		"caddy reload exited 1 in tamp-router", "fix what the output above names")}
	failed := c.run(t, "start")

	failed.assertCode(t, exitcode.CodeFailed)
	if !strings.Contains(failed.stderr, caddyLog) {
		t.Errorf("stderr = %q, want the Caddy log above the error", failed.stderr)
	}
}

func TestAContainerDeletedByHandReadsAsPartlyRunningInListStatusContextAndDoctor(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev", "--site", "shop.localhost")
	if err := c.docker.RemoveContainer(context.Background(), "erp-dev-frappe-1"); err != nil {
		t.Fatal(err)
	}

	list := c.run(t, "list")
	status := c.run(t, "status")
	ctx := c.run(t, "context")
	doctor := c.run(t, "doctor")

	if !regexp.MustCompile(`(?m)^erp-dev +partly running `).MatchString(list.stdout) {
		t.Errorf("list stdout = %q, want erp-dev partly running", list.stdout)
	}
	status.assertContains(t, "environment erp-dev: partly running\n")
	if !regexp.MustCompile(`(?m)^  erp-dev-frappe-1 +missing$`).MatchString(status.stdout) {
		t.Errorf("status stdout = %q, want erp-dev-frappe-1 missing", status.stdout)
	}
	ctx.assertContains(t, "The environment is partly running: erp-dev-frappe-1 is missing. Start the rest with `tamp start -e erp-dev`")
	doctor.assertContains(t, "the environment erp-dev is partly running: run `tamp start`")
}

func TestStatusReadsACrashLoopingBenchAsSuchAndNamesTheLogsCommand(t *testing.T) {
	c := sandbox(t)
	c.newEnvironment(t, "--frappe", "version-15", "--site", "shop.localhost")
	crashing(c)

	r := c.run(t, "status")

	r.assertCode(t, exitcode.CodeOK)
	r.assertContains(t, "environment erp-dev: crash looping\n")
	if want := "warning: " + crashMsg + ": see why with `tamp logs frappe`\n"; !strings.Contains(r.stderr, want) {
		t.Errorf("stderr = %q, want %q", r.stderr, want)
	}
}

func TestOneExitOfHonchoIsNotACrashLoop(t *testing.T) {
	c := sandbox(t)
	c.newEnvironment(t, "--frappe", "version-15", "--site", "shop.localhost")
	c.docker.ExecStdout = map[string]string{frappe.HonchoExits: "1\n"}

	r := c.run(t, "status")

	r.assertCode(t, exitcode.CodeOK)
	r.assertContains(t, "environment erp-dev: running\n")
	if r.stderr != "" {
		t.Errorf("stderr = %q, want nothing", r.stderr)
	}
}

func TestListShowsACrashLoopingEnvironmentWithTheLogsFixByE(t *testing.T) {
	c := sandbox(t)
	dir := c.newEnvironment(t, "--frappe", "version-15", "--site", "shop.localhost")
	other := c.newIn(t, "other")
	c.run(t, "stop").assertCode(t, exitcode.CodeOK)
	t.Chdir(c.dir)
	crashing(c)
	execs := len(c.docker.Execs)

	r := c.run(t, "list")

	r.assertCode(t, exitcode.CodeOK)
	if !regexp.MustCompile(`(?m)^erp-dev +crash looping +` + regexp.QuoteMeta(dir) + `$`).MatchString(r.stdout) {
		t.Errorf("stdout = %q, want erp-dev crash looping", r.stdout)
	}
	if !regexp.MustCompile(`(?m)^other +stopped +` + regexp.QuoteMeta(other) + `$`).MatchString(r.stdout) {
		t.Errorf("stdout = %q, want the stopped other as before", r.stdout)
	}
	if want := "warning: " + crashMsg + ": see why with `tamp logs frappe -e erp-dev`\n"; r.stderr != want {
		t.Errorf("stderr = %q, want %q", r.stderr, want)
	}
	for _, e := range c.docker.Execs[execs:] {
		if e.Container != frappeContainer {
			t.Errorf("list ran %q in %s, want nothing run in a stopped environment", e.Cmd, e.Container)
		}
	}
}

func TestListShowsEveryEnvironmentWhenOneBenchCannotBeAskedAboutCrashes(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev", "--site", "shop.localhost")
	c.newIn(t, "other")
	t.Chdir(c.dir)
	c.docker.ExecFails = map[string]error{frappe.HonchoExits: exitcode.New(exitcode.CodeFailed,
		"cannot run bash -c ... in erp-dev-frappe-1: container is paused", "check that the container is running with `docker ps`")}

	r := c.run(t, "list")

	r.assertCode(t, exitcode.CodeOK)
	for _, name := range []string{"erp-dev", "other"} {
		if !regexp.MustCompile(`(?m)^` + name + ` +running `).MatchString(r.stdout) {
			t.Errorf("stdout = %q, want %s listed as its containers show it", r.stdout, name)
		}
		if !strings.Contains(r.stderr, "warning: cannot tell whether the bench processes of "+name+" keep crashing: cannot run") {
			t.Errorf("stderr = %q, want a warning for %s", r.stderr, name)
		}
	}
}

const pausedWarning = "warning: erp-dev-frappe-1 is paused, so the environment's sites do not answer: unpause it with `docker unpause erp-dev-frappe-1`\n"

func TestStatusNamesAPausedContainerPausedWithTheUnpauseFix(t *testing.T) {
	c := sandbox(t)
	c.newEnvironment(t, "--frappe", "version-15", "--site", "shop.localhost")
	c.docker.Pause(frappeContainer)

	r := c.run(t, "status")

	r.assertCode(t, exitcode.CodeOK)
	r.assertContains(t, "environment erp-dev: paused\n")
	if !regexp.MustCompile(`(?m)^  erp-dev-frappe-1 +paused$`).MatchString(r.stdout) {
		t.Errorf("stdout = %q, want erp-dev-frappe-1 paused", r.stdout)
	}
	if r.stderr != pausedWarning {
		t.Errorf("stderr = %q, want %q", r.stderr, pausedWarning)
	}
}

func TestListShowsAnEnvironmentWithAPausedContainerAsPausedWithTheUnpauseFix(t *testing.T) {
	c := sandbox(t)
	dir := c.newEnvironment(t, "--frappe", "version-15", "--site", "shop.localhost")
	t.Chdir(c.dir)
	c.docker.Pause(frappeContainer)

	r := c.run(t, "list")

	r.assertCode(t, exitcode.CodeOK)
	if !regexp.MustCompile(`(?m)^erp-dev +paused +` + regexp.QuoteMeta(dir) + `$`).MatchString(r.stdout) {
		t.Errorf("stdout = %q, want erp-dev paused", r.stdout)
	}
	if r.stderr != pausedWarning {
		t.Errorf("stderr = %q, want %q", r.stderr, pausedWarning)
	}
}
