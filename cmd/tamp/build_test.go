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

	if len(f.Ups) != 1 {
		t.Fatalf("compose ups = %d, want 1", len(f.Ups))
	}
	up := f.Ups[0]
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
	if again := c.docker.ExecLines()[execs:]; len(again) > 0 {
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

func TestStartStopsAtAPartlyCreatedSiteWithTheRemoveThenStartFix(t *testing.T) {
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
		r.assertOneLineError(t, "shop.localhost", "run `tamp site remove shop.localhost --yes`, then run `tamp start`")
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
