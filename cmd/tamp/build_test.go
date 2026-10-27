package main

import (
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/docker"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/frappe"
)

const frappeContainer = "erp-dev-frappe-1"

// newEnvironment runs `tamp new erp-dev` with args, expects it to succeed,
// and moves into the environment's directory.
func (c *cli) newEnvironment(t *testing.T, args ...string) string {
	t.Helper()
	c.run(t, append([]string{"new", "erp-dev"}, args...)...).assertCode(t, exitcode.CodeOK)
	dir := filepath.Join(c.dir, "erp-dev")
	t.Chdir(dir)
	return dir
}

// services splits compose.yaml's services section into each service's
// block, keyed by service name.
func services(t *testing.T, compose string) map[string]string {
	t.Helper()
	_, rest, ok := strings.Cut(compose, "\nservices:\n")
	if !ok {
		t.Fatalf("compose.yaml has no services section:\n%s", compose)
	}
	section, _, _ := strings.Cut(rest, "\nvolumes:\n")
	blocks := map[string]string{}
	header := regexp.MustCompile(`(?m)^  ([a-z-]+):$`)
	starts := header.FindAllStringSubmatchIndex(section, -1)
	for i, m := range starts {
		end := len(section)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		blocks[section[m[2]:m[3]]] = section[m[0]:end]
	}
	return blocks
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return lines[len(lines)-1]
}

func benchSettings(t *testing.T, f *docker.Fake) map[string]any {
	t.Helper()
	body, ok := f.File(frappeContainer, frappe.CommonSiteConfig)
	if !ok {
		t.Fatal("common_site_config.json was never written")
	}
	var settings map[string]any
	if err := json.Unmarshal([]byte(body), &settings); err != nil {
		t.Fatalf("common_site_config.json is not JSON: %v\n%s", err, body)
	}
	return settings
}

func TestStartWithDockerUnreachableExits4(t *testing.T) {
	c := sandbox(t)
	c.newEnvironment(t, "--frappe", "version-15")
	c.docker = &docker.Fake{PingErr: exitcode.New(exitcode.CodeDockerUnavailable,
		"Docker is not answering", "start Docker")}

	r := c.run(t, "start")

	r.assertCode(t, exitcode.CodeDockerUnavailable)
	r.assertOneLineError(t, "Docker is not answering", "`tamp start`")
	if !slices.Equal(c.docker.Calls, []string{"Ping"}) {
		t.Errorf("Docker calls = %v, want only Ping", c.docker.Calls)
	}
}

func TestVolumesAndNetworksFollowComposesNaming(t *testing.T) {
	c := sandbox(t)

	c.newEnvironment(t, "--frappe", "version-15", "--site", "shop.localhost")

	f := c.docker
	names := f.VolumeNames()
	all, err := f.AllContainers(t.Context(), "tamp.env")
	if err != nil {
		t.Fatal(err)
	}
	all = append(all, docker.Container{Name: "tamp-router"})
	for _, listed := range all {
		c, err := f.Inspect(t.Context(), listed.Name)
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, c.Name)
		names = append(names, slices.Collect(maps.Keys(c.Networks))...)
	}
	for _, want := range []string{"erp-dev_db", "erp-dev_bench", "erp-dev_default", "tamp-router_default"} {
		if !slices.Contains(names, want) {
			t.Errorf("Docker holds no %s among %v", want, names)
		}
	}
}

func TestNewBuildsTheImageTheContainersTheBenchAndTheSite(t *testing.T) {
	c := sandbox(t)

	dir := c.newEnvironment(t, "--frappe", "version-15", "--site", "shop.localhost")

	f := c.docker
	if len(f.Builds) != 1 {
		t.Fatalf("image builds = %d, want 1", len(f.Builds))
	}
	b := f.Builds[0]
	if !strings.HasPrefix(b.Dockerfile, "FROM frappe/bench:") {
		t.Errorf("Dockerfile does not build from the official bench image:\n%s", b.Dockerfile)
	}
	if b.Args["PYTHON"] != "3.11" || b.Args["NODE"] != "18" {
		t.Errorf("build args = %v, want the version-15 Python 3.11 and Node 18", b.Args)
	}
	if !strings.Contains(b.Tag, "version-15") {
		t.Errorf("image tag = %q, want it to name version-15", b.Tag)
	}

	wantVolumes := []docker.Volume{
		{Name: "erp-dev_bench", Labels: map[string]string{"tamp.env": "erp-dev", "tamp.frappe": "version-15"}},
		{Name: "tamp-cache"},
	}
	if len(f.Volumes) != len(wantVolumes) {
		t.Fatalf("volumes = %v, want %v", f.Volumes, wantVolumes)
	}
	for i, want := range wantVolumes {
		got := f.Volumes[i]
		if got.Name != want.Name || len(got.Labels) != len(want.Labels) {
			t.Errorf("volume %d = %v, want %v", i, got, want)
			continue
		}
		for k, v := range want.Labels {
			if got.Labels[k] != v {
				t.Errorf("volume %s label %s = %q, want %q", got.Name, k, got.Labels[k], v)
			}
		}
	}

	ups := f.UpsOf("erp-dev")
	if len(ups) != 1 {
		t.Fatalf("compose ups of erp-dev = %d, want 1", len(ups))
	}
	up := ups[0]
	if up.Project.Name != "erp-dev" || up.Project.File != filepath.Join(dir, "compose.yaml") {
		t.Errorf("compose project = %+v, want erp-dev at %s", up.Project, filepath.Join(dir, "compose.yaml"))
	}
	blocks := services(t, up.Compose)
	names := slices.Sorted(maps.Keys(blocks))
	if want := []string{"frappe", "mariadb", "redis-cache", "redis-queue"}; !slices.Equal(names, want) {
		t.Errorf("services = %v, want %v", names, want)
	}
	pathLabel := "tamp.path: '" + dir + "'"
	for name, block := range blocks {
		if !strings.Contains(block, "tamp.env: 'erp-dev'") || !strings.Contains(block, pathLabel) {
			t.Errorf("service %s lacks the labels tamp.env and %s:\n%s", name, pathLabel, block)
		}
	}
	for _, mount := range []string{"- bench:/home/frappe/frappe-bench\n", "- ./apps:/home/frappe/frappe-bench/apps\n", "- tamp-cache:/home/frappe/.cache\n"} {
		if !strings.Contains(blocks["frappe"], mount) {
			t.Errorf("the frappe service lacks the mount %q:\n%s", mount, blocks["frappe"])
		}
	}
	if !strings.Contains(blocks["frappe"], "image: "+b.Tag+"\n") {
		t.Errorf("the frappe service does not run the built image %s", b.Tag)
	}
	if !strings.Contains(blocks["mariadb"], "- db:/var/lib/mysql\n") {
		t.Errorf("the mariadb service lacks the db volume:\n%s", blocks["mariadb"])
	}
	if !strings.Contains(up.Compose, "  bench:\n    name: erp-dev_bench\n    external: true\n") ||
		!strings.Contains(up.Compose, "  tamp-cache:\n    name: tamp-cache\n    external: true\n") ||
		!strings.Contains(up.Compose, "  db: {}\n") {
		t.Errorf("compose.yaml does not declare the db, bench and tamp-cache volumes:\n%s", up.Compose)
	}

	lines := f.ExecLines()
	init := slices.IndexFunc(lines, func(l string) bool { return strings.Contains(l, "bench init") })
	site := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, "bench new-site shop.localhost ") })
	if init < 0 || site < 0 || init > site {
		t.Errorf("execs = %q, want bench init and then bench new-site shop.localhost", lines)
	}
	if got, want := readFile(t, filepath.Join(dir, "tamp.toml")), "frappe = \"version-15\"\n\n[[sites]]\n  host = \"shop.localhost\"\n"; got != want {
		t.Errorf("tamp.toml = %q, want %q", got, want)
	}
}

func TestTheImageRunsAsTheHostUserOnLinuxAndAs1000Elsewhere(t *testing.T) {
	c := sandbox(t)

	c.newEnvironment(t, "--frappe", "version-16")

	uid, gid := "1000", "1000"
	if runtime.GOOS == "linux" {
		uid, gid = strconv.Itoa(os.Getuid()), strconv.Itoa(os.Getgid())
	}
	args := c.docker.Builds[0].Args
	if args["UID"] != uid || args["GID"] != gid {
		t.Errorf("build args UID=%s GID=%s, want %s and %s", args["UID"], args["GID"], uid, gid)
	}
	if args["PYTHON"] != "3.14" || args["NODE"] != "24" {
		t.Errorf("build args = %v, want the version-16 Python 3.14 and Node 24", args)
	}
}

func TestFrappeIsClonedOnTheHostTheWayBenchGetAppClones(t *testing.T) {
	c := sandbox(t)

	dir := c.newEnvironment(t, "--frappe", "version-16")

	clone := filepath.Join(dir, "apps", "frappe")
	if got := git(t, clone, "rev-parse", "--is-shallow-repository"); got != "true" {
		t.Errorf("shallow = %s, want a depth-1 clone", got)
	}
	if got := git(t, clone, "rev-list", "--count", "HEAD"); got != "1" {
		t.Errorf("commits = %s, want 1", got)
	}
	if got := git(t, clone, "remote"); got != "upstream" {
		t.Errorf("remotes = %q, want only upstream", got)
	}
	if got := git(t, clone, "branch", "--show-current"); got != "version-16" {
		t.Errorf("branch = %s, want the recipe's version-16", got)
	}
	if runtime.GOOS == "windows" {
		if got := git(t, clone, "config", "--local", "core.autocrlf"); got != "false" {
			t.Errorf("core.autocrlf = %s, want false", got)
		}
	}
}

func TestStartLeavesAnExistingCloneUntouched(t *testing.T) {
	c := sandbox(t)
	dir := c.newEnvironment(t, "--frappe", "version-15")
	clone := filepath.Join(dir, "apps", "frappe")
	git(t, clone, "-c", "user.name=me", "-c", "user.email=me@example.com", "commit", "--allow-empty", "-m", "mine")
	head := git(t, clone, "rev-parse", "HEAD")
	dirty := filepath.Join(clone, "work.py")
	if err := os.WriteFile(dirty, []byte("draft"), 0o644); err != nil {
		t.Fatal(err)
	}

	c.run(t, "start").assertCode(t, exitcode.CodeOK)

	if got := git(t, clone, "rev-parse", "HEAD"); got != head {
		t.Errorf("HEAD = %s, want %s", got, head)
	}
	if got := readFile(t, dirty); got != "draft" {
		t.Errorf("work.py = %q, want it unchanged", got)
	}
}

func TestASecondStartRepeatsNoCompletedWork(t *testing.T) {
	c := sandbox(t)
	c.newEnvironment(t, "--frappe", "version-15", "--site", "shop.localhost")
	builds, execs := len(c.docker.Builds), len(c.docker.Execs)

	r := c.run(t, "start")

	r.assertCode(t, exitcode.CodeOK)
	if len(c.docker.Builds) != builds {
		t.Errorf("start built the image again")
	}
	if again := benchExecs(c.docker.Execs[execs:]); len(again) > 0 {
		t.Errorf("start ran %q again, want no bench command", again)
	}
	if strings.Contains(r.stderr, "Cloning") {
		t.Errorf("start cloned again:\n%s", r.stderr)
	}
}

func TestAFailurePartwayInNewNamesTheDirectoryToStartInAndStartThereFinishesTheSite(t *testing.T) {
	c := sandbox(t)
	c.docker.ExecFails = map[string]error{"set-config": exitcode.New(exitcode.CodeFailed,
		"bench set-config exited 1 in erp-dev-frappe-1", "fix what the output above names")}

	r := c.run(t, "new", "erp-dev", "--frappe", "version-15", "--site", "shop.localhost")

	r.assertCode(t, exitcode.CodeFailed)
	if line := lastLine(r.stderr); !strings.HasPrefix(line, "error: ") || !strings.HasSuffix(line, "then run `tamp start` in erp-dev") {
		t.Errorf("last stderr line = %q, want an error whose fix ends in `tamp start` in erp-dev", line)
	}

	// The user follows the fix.
	c.docker.ExecFails = nil
	t.Chdir(filepath.Join(c.dir, "erp-dev"))
	execs := len(c.docker.Execs)
	c.run(t, "start").assertCode(t, exitcode.CodeOK)

	again := c.docker.ExecLines()[execs:]
	if slices.ContainsFunc(again, func(l string) bool { return strings.Contains(l, "bench init") || strings.Contains(l, "new-site") }) {
		t.Errorf("start repeated finished work: %q", again)
	}
	config, _ := c.docker.File(frappeContainer, frappe.SiteConfig("shop.localhost"))
	if on, err := frappe.DeveloperModeOn([]byte(config)); err != nil || !on {
		t.Errorf("site_config.json = %s, want developer_mode 1 set by the resumed start", config)
	}
}

func TestStartStopsAtAPartlyCreatedSiteWithTheRemoveThenAddFix(t *testing.T) {
	c := sandbox(t)
	c.docker.ExecFails = map[string]error{"new-site": exitcode.New(exitcode.CodeFailed,
		"bench new-site shop.localhost exited 1 in erp-dev-frappe-1", "fix what the output above names")}
	c.run(t, "new", "erp-dev", "--frappe", "version-15", "--site", "shop.localhost").assertCode(t, exitcode.CodeFailed)
	c.docker.ExecFails = nil
	t.Chdir(filepath.Join(c.dir, "erp-dev"))

	for range 2 {
		execs := len(c.docker.Execs)

		r := c.run(t, "start", "--quiet")

		r.assertCode(t, exitcode.CodeFailed)
		r.assertOneLineError(t, "shop.localhost", "run `tamp site remove shop.localhost --yes`, then `tamp site add shop.localhost`")
		if strings.Contains(r.stderr, "tamp start") {
			t.Errorf("stderr = %q, want no `tamp start`, which would not rebuild the removed site", r.stderr)
		}
		if again := c.docker.ExecLines()[execs:]; len(again) > 0 {
			t.Errorf("start ran %q, want it to neither recreate nor finish the site", again)
		}
	}
}

func TestASettingsFileHoldingNullIsReplacedWithTampsSettings(t *testing.T) {
	c := sandbox(t)
	c.newEnvironment(t, "--frappe", "version-15")
	c.docker.SetFile(frappeContainer, frappe.CommonSiteConfig, "null")

	c.run(t, "start").assertCode(t, exitcode.CodeOK)

	if got := benchSettings(t, c.docker)["webserver_host"]; got != "127.0.0.1" {
		t.Errorf("webserver_host = %v, want 127.0.0.1", got)
	}
}

func TestNewWithASiteThatIsNotLocalhostExits2AndWritesNothing(t *testing.T) {
	for _, host := range []string{"shop.example.com", "localhost", "Shop.localhost", "shop..localhost"} {
		t.Run(host, func(t *testing.T) {
			c := sandbox(t)

			r := c.run(t, "new", "erp-dev", "--frappe", "version-15", "--site", host)

			r.assertCode(t, exitcode.CodeUsage)
			r.assertOneLineError(t, host, ".localhost")
			assertNotExist(t, filepath.Join(c.dir, "erp-dev"))
			if len(c.docker.Calls) > 0 {
				t.Errorf("Docker calls = %v, want none", c.docker.Calls)
			}
		})
	}
}

func TestNewWithTheSameSiteTwiceExits2(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "new", "erp-dev", "--frappe", "version-15", "--site", "shop.localhost", "--site", "shop.localhost")

	r.assertCode(t, exitcode.CodeUsage)
	r.assertOneLineError(t, "shop.localhost")
	assertNotExist(t, filepath.Join(c.dir, "erp-dev"))
}

func TestEachSiteIsADeveloperSiteWithTheFixedPasswords(t *testing.T) {
	c := sandbox(t)

	c.newEnvironment(t, "--frappe", "version-15", "--site", "shop.localhost", "--site", "erp.localhost")

	lines := c.docker.ExecLines()
	for _, host := range []string{"shop.localhost", "erp.localhost"} {
		i := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, "bench new-site "+host+" ") })
		if i < 0 {
			t.Errorf("no new-site for %s in %q", host, lines)
			continue
		}
		for _, flag := range []string{" --db-root-password password", " --admin-password password"} {
			if !strings.Contains(lines[i], flag) {
				t.Errorf("new-site %q lacks %q", lines[i], flag)
			}
		}
		if want := "bench --site " + host + " set-config -p developer_mode 1"; i+1 >= len(lines) || lines[i+1] != want {
			t.Errorf("after new-site %s, execs = %q, want %q", host, lines[i:], want)
		}
	}
	if compose := c.docker.Ups[0].Compose; !strings.Contains(services(t, compose)["mariadb"], "MARIADB_ROOT_PASSWORD: password\n") {
		t.Errorf("MariaDB's root password is not password:\n%s", compose)
	}
}

func TestTheBenchServesSocketIOFromLoopbackAndNothingSetsDevServer(t *testing.T) {
	c := sandbox(t)

	c.newEnvironment(t, "--frappe", "version-15", "--site", "shop.localhost")

	if got := benchSettings(t, c.docker)["webserver_host"]; got != "127.0.0.1" {
		t.Errorf("webserver_host = %v, want 127.0.0.1", got)
	}
	procfile, _ := c.docker.File(frappeContainer, frappe.Procfile)
	if !strings.Contains(procfile, "web: ") {
		t.Fatalf("Procfile = %q, want the bench's processes", procfile)
	}
	for what, text := range map[string]string{
		"compose.yaml": c.docker.Ups[0].Compose,
		"the Procfile": procfile,
		"the execs":    strings.Join(c.docker.ExecLines(), "\n"),
	} {
		if strings.Contains(text, "DEV_SERVER") {
			t.Errorf("%s sets DEV_SERVER:\n%s", what, text)
		}
	}
}

func TestEveryBuildRewritesComposeAndTheProcfileAndKeepsOtherSettings(t *testing.T) {
	c := sandbox(t)
	dir := c.newEnvironment(t, "--frappe", "version-15")
	compose := c.docker.Ups[0].Compose
	procfile, _ := c.docker.File(frappeContainer, frappe.Procfile)
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.docker.SetFile(frappeContainer, frappe.Procfile, "web: bench start\n")
	c.docker.SetFile(frappeContainer, frappe.CommonSiteConfig,
		`{"live_reload": true, "my_key": "mine", "db_host": "localhost", "webserver_host": "0.0.0.0"}`)

	c.run(t, "start").assertCode(t, exitcode.CodeOK)

	if got := readFile(t, filepath.Join(dir, "compose.yaml")); got != compose {
		t.Errorf("compose.yaml = %q, want it rewritten as %q", got, compose)
	}
	if got, _ := c.docker.File(frappeContainer, frappe.Procfile); got != procfile {
		t.Errorf("Procfile = %q, want it rewritten as %q", got, procfile)
	}
	settings := benchSettings(t, c.docker)
	for key, want := range map[string]any{
		"live_reload": true, "my_key": "mine", "db_host": "mariadb", "webserver_host": "127.0.0.1",
	} {
		if settings[key] != want {
			t.Errorf("setting %s = %v, want %v", key, settings[key], want)
		}
	}
}

func TestTheFirstBuildKeepsTheSettingsBenchInitWrote(t *testing.T) {
	c := sandbox(t)

	c.newEnvironment(t, "--frappe", "version-15")

	if got := benchSettings(t, c.docker)["live_reload"]; got != true {
		t.Errorf("live_reload = %v, want bench init's true", got)
	}
}

func TestStartOutsideAnEnvironmentExits2(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "start")

	r.assertCode(t, exitcode.CodeUsage)
	r.assertOneLineError(t, "no recipe")
	if len(c.docker.Calls) > 0 {
		t.Errorf("Docker calls = %v, want none", c.docker.Calls)
	}
}

func TestStartWithARecipeNamingAnUnsupportedBranchExits1(t *testing.T) {
	c := sandbox(t)
	if err := os.WriteFile(filepath.Join(c.dir, "tamp.toml"), []byte("frappe = \"version-14\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := c.run(t, "start")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertOneLineError(t, "version-14", "version-15, version-16, develop")
}

// YAML reads a bare 2024, true or null as a number, a bool or nothing, and
// compose wants the project name as a string.
func TestComposeNamesTheProjectAsAStringWhateverTheEnvironmentIsCalled(t *testing.T) {
	for _, name := range []string{"2024", "true", "null"} {
		t.Run(name, func(t *testing.T) {
			c := sandbox(t)

			c.run(t, "new", name, "--frappe", "version-15").assertCode(t, exitcode.CodeOK)

			if compose := c.docker.Ups[0].Compose; !strings.Contains(compose, "\nname: '"+name+"'\n") {
				t.Errorf("compose.yaml does not quote the project name %s:\n%s", name, compose)
			}
		})
	}
}

// isSiteAppsList reports whether cmd only lists a site's apps.
func isSiteAppsList(cmd []string) bool {
	return len(cmd) > 3 && slices.Equal(cmd, frappe.SiteApps(cmd[2]))
}

// isSiteWait reports whether cmd is part of every build's wait for its sites:
// the reset of honcho's exits, the wait itself, or the read of the exits.
func isSiteWait(cmd []string) bool {
	return slices.Equal(cmd, []string{"rm", "-f", frappe.HonchoExits}) ||
		slices.Equal(cmd, frappe.RecentExits()) ||
		len(cmd) == 7 && cmd[2] == frappe.WaitForSite("", 0)[2]
}

// benchExecs are the commands run in the frappe container, without the
// read-only listings of its sites and their apps, and the wait for the sites.
func benchExecs(execs []docker.Exec) []string {
	var lines []string
	for _, e := range execs {
		if e.Container == frappeContainer && !slices.Equal(e.Cmd, frappe.ListSites()) && !isSiteAppsList(e.Cmd) && !isSiteWait(e.Cmd) {
			lines = append(lines, strings.Join(e.Cmd, " "))
		}
	}
	return lines
}

// crashing makes the frappe container report two exits of honcho on its own
// in the last two minutes: a crash loop.
func crashing(c *cli) {
	c.docker.ExecStdout = map[string]string{frappe.HonchoExits: "1\n1\n"}
}

const crashMsg = "the bench processes of erp-dev keep crashing: honcho exited 2 times in the last 2 minutes"

func TestStartWaitsForEachSiteToAnswerAfterTheRouterServesIt(t *testing.T) {
	c := sandbox(t)
	c.newEnvironment(t, "--frappe", "version-15", "--site", "shop.localhost", "--site", "erp.localhost")
	execs := len(c.docker.Execs)

	r := c.run(t, "start")

	r.assertCode(t, exitcode.CodeOK)
	lines := c.docker.ExecLines()[execs:]
	reload := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, "caddy reload") })
	var waited []string
	for i, e := range c.docker.Execs[execs:] {
		if len(e.Cmd) == 7 && e.Cmd[2] == frappe.WaitForSite("", 0)[2] {
			if i < reload {
				t.Errorf("the wait for %s ran before the router reloaded", e.Cmd[4])
			}
			if e.Container != frappeContainer {
				t.Errorf("the wait ran in %s, want %s", e.Container, frappeContainer)
			}
			if secs, err := strconv.Atoi(e.Cmd[6]); err != nil || secs < 110 || secs > 120 {
				t.Errorf("the wait for %s gets %q seconds, want about 120", e.Cmd[4], e.Cmd[6])
			}
			waited = append(waited, e.Cmd[4])
		}
	}
	if reload < 0 || !slices.Equal(waited, []string{"shop.localhost", "erp.localhost"}) {
		t.Errorf("execs = %q, want a router reload, then a wait for each site", lines)
	}
}

func TestASiteThatNeverAnswersFailsStartWithExit1AndTheLogsFix(t *testing.T) {
	c := sandbox(t)
	c.newEnvironment(t, "--frappe", "version-15", "--site", "shop.localhost")
	c.docker.ExecFails = map[string]error{"/api/method/ping": probeGaveUp}

	r := c.run(t, "start", "--quiet")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertOneLineError(t, "the site shop.localhost did not answer in 2 minutes: find out why with `tamp logs frappe`, then run `tamp start`")
}

func TestACrashLoopWhileWaitingFailsStartWithTheCrashAndTheLogsFix(t *testing.T) {
	c := sandbox(t)
	c.newEnvironment(t, "--frappe", "version-15", "--site", "shop.localhost")
	c.docker.ExecFails = map[string]error{"/api/method/ping": probeGaveUp}
	crashing(c)

	r := c.run(t, "start", "-e", "erp-dev", "--quiet")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertOneLineError(t, crashMsg+": find out why with `tamp logs frappe -e erp-dev`, then run `tamp start -e erp-dev`")
}

func TestACrashLoopAfterTheSitesAnswerIsAWarningAndStartExits0(t *testing.T) {
	c := sandbox(t)
	c.docker.ExecStdout = map[string]string{frappe.HonchoExits: "1\n1\n"}

	r := c.run(t, "new", "erp-dev", "--frappe", "version-15", "--site", "shop.localhost")

	r.assertCode(t, exitcode.CodeOK)
	if want := "warning: " + crashMsg + ": find out why with `tamp logs frappe` in erp-dev\n"; !strings.Contains(r.stderr, want) {
		t.Errorf("stderr = %q, want %q", r.stderr, want)
	}
}

func TestEachBuildForgetsTheExitsOfHonchoBeforeItWaits(t *testing.T) {
	c := sandbox(t)
	c.newEnvironment(t, "--frappe", "version-15", "--site", "shop.localhost")
	execs := len(c.docker.Execs)

	c.run(t, "start").assertCode(t, exitcode.CodeOK)

	lines := c.docker.ExecLines()[execs:]
	forget := slices.Index(lines, "rm -f "+frappe.HonchoExits)
	wait := slices.IndexFunc(c.docker.Execs[execs:], func(e docker.Exec) bool {
		return len(e.Cmd) == 7 && e.Cmd[2] == frappe.WaitForSite("", 0)[2]
	})
	if forget < 0 || wait < forget {
		t.Errorf("execs = %q, want honcho's exits removed before the wait", lines)
	}
}

func TestTheFrappeContainerLogsEachExitOfHonchoThatNoSignalCaused(t *testing.T) {
	c := sandbox(t)
	c.newEnvironment(t, "--frappe", "version-15")

	svc := services(t, c.docker.UpsOf("erp-dev")[0].Compose)["frappe"]

	for _, want := range []string{
		"rm -f /tmp/tamp-honcho-exits\n",
		"code=$$?\n",
		"130 | 143) ;;\n",
		`*) echo "$$(date +%s) $$code" >>/tmp/tamp-honcho-exits ;;`,
	} {
		if !strings.Contains(svc, want) {
			t.Errorf("the frappe service lacks %q:\n%s", want, svc)
		}
	}
}

// probeGaveUp is the wait for a site exiting 1, as it does on its timeout.
var probeGaveUp = &docker.ExitError{Status: 1, Err: exitcode.New(exitcode.CodeFailed,
	"bash -c ... exited 1 in erp-dev-frappe-1", "fix what the output above names")}

func TestAWaitDockerCannotRunFailsStartWithDockersOwnErrorNotATimeout(t *testing.T) {
	c := sandbox(t)
	c.newEnvironment(t, "--frappe", "version-15", "--site", "shop.localhost")
	c.docker.ExecFails = map[string]error{"/api/method/ping": exitcode.New(exitcode.CodeFailed,
		"cannot run bash -c ... in erp-dev-frappe-1: container is not running", "check that the container is running with `docker ps`")}

	r := c.run(t, "start", "--quiet")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertOneLineError(t, "cannot run bash -c ... in erp-dev-frappe-1: container is not running: check that the container is running with `docker ps`, then run `tamp start`")
	if strings.Contains(r.stderr, "did not answer") {
		t.Errorf("stderr = %q, want Docker's error, not a timeout", r.stderr)
	}
}

func TestAWaitThatExitsWithAnotherCodeThanItsGiveUpFailsStartWithThatExit(t *testing.T) {
	c := sandbox(t)
	c.newEnvironment(t, "--frappe", "version-15", "--site", "shop.localhost")
	c.docker.ExecFails = map[string]error{"/api/method/ping": &docker.ExitError{Status: 127, Err: exitcode.New(exitcode.CodeFailed,
		"bash -c ... exited 127 in erp-dev-frappe-1", "fix what the output above names")}}

	r := c.run(t, "start", "--quiet")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertOneLineError(t, "bash -c ... exited 127 in erp-dev-frappe-1: fix what the output above names, then run `tamp start`")
	if strings.Contains(r.stderr, "did not answer") {
		t.Errorf("stderr = %q, want the exit 127, not a timeout", r.stderr)
	}
}
