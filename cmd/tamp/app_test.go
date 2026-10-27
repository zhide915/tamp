package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/frappe"
)

const acmeURL = acmeSSH + "acme-erp.git"

// indexOf is the position of the first exec line with prefix, or -1.
func indexOf(lines []string, prefix string) int {
	return slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, prefix) })
}

// appsFolders lists the folders in an environment's apps/.
func appsFolders(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, "apps"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestNewRecordsABareAppNameAsTheFrappeOrganisationsRepository(t *testing.T) {
	c := sandbox(t)

	dir := c.newEnvironment(t, "--frappe", "version-15", "--app", "erpnext", "--app", "hrms")

	want := `frappe = "version-15"

[[apps]]
  name = "erpnext"
  url = "https://github.com/frappe/erpnext"
  branch = "develop"

[[apps]]
  name = "hrms"
  url = "https://github.com/frappe/hrms"
  branch = "develop"
`
	if got := readFile(t, filepath.Join(dir, "tamp.toml")); got != want {
		t.Errorf("tamp.toml = %q, want %q", got, want)
	}
}

func TestAnAppURLWithABranchIsRecordedWithThatBranchAndClonedAtIt(t *testing.T) {
	for _, branch := range []string{"main", "develop"} {
		t.Run(branch, func(t *testing.T) {
			c := sandbox(t)

			dir := c.newEnvironment(t, "--frappe", "version-15", "--app", acmeURL+"#"+branch)

			want := "[[apps]]\n  name = \"acme\"\n  url = \"" + acmeURL + "\"\n  branch = \"" + branch + "\"\n"
			if got := readFile(t, filepath.Join(dir, "tamp.toml")); !strings.Contains(got, want) {
				t.Errorf("tamp.toml = %q, want it to hold %q", got, want)
			}
			if got := git(t, filepath.Join(dir, "apps", "acme"), "branch", "--show-current"); got != branch {
				t.Errorf("the clone is on %s, want %s", got, branch)
			}
		})
	}
}

func TestAnAppWithoutABranchRecordsTheRepositorysDefaultBranch(t *testing.T) {
	c := sandbox(t)

	dir := c.newEnvironment(t, "--frappe", "version-15", "--app", acmeURL)

	if got := readFile(t, filepath.Join(dir, "tamp.toml")); !strings.Contains(got, "  branch = \"main\"\n") {
		t.Errorf("tamp.toml = %q, want the default branch main", got)
	}
	clone := filepath.Join(dir, "apps", "acme")
	if got := git(t, clone, "branch", "--show-current"); got != "main" {
		t.Errorf("the clone is on %s, want main", got)
	}
}

func TestAnAppIsClonedTheWayBenchGetAppClones(t *testing.T) {
	c := sandbox(t)

	dir := c.newEnvironment(t, "--frappe", "version-15", "--app", acmeURL)

	clone := filepath.Join(dir, "apps", "acme")
	if got := git(t, clone, "rev-list", "--count", "HEAD"); got != "1" {
		t.Errorf("commits = %s, want a depth-1 clone", got)
	}
	if got := git(t, clone, "remote"); got != "upstream" {
		t.Errorf("remotes = %q, want only upstream", got)
	}
}

func TestARepositoryIsRecordedUnderTheNameOfThePackageItHolds(t *testing.T) {
	c := sandbox(t)

	dir := c.newEnvironment(t, "--frappe", "version-15", "--app", acmeURL)

	if got := readFile(t, filepath.Join(dir, "tamp.toml")); !strings.Contains(got, "  name = \"acme\"\n") {
		t.Errorf("tamp.toml = %q, want the app named acme", got)
	}
	if got := appsFolders(t, dir); !slices.Equal(got, []string{"acme", "frappe"}) {
		t.Errorf("apps/ holds %q, want acme and frappe", got)
	}
	if !slices.Contains(c.docker.ExecLines(), strings.Join(frappe.InstallApp("acme"), " ")) {
		t.Errorf("execs = %q, want acme installed into the bench", c.docker.ExecLines())
	}
}

func TestAppsInstallIntoTheBenchAndOnEachSiteInRecipeOrder(t *testing.T) {
	c := sandbox(t)

	c.newEnvironment(t, "--frappe", "version-15", "--app", "erpnext", "--app", "hrms", "--site", "shop.localhost")

	lines := c.docker.ExecLines()
	for _, order := range [][2]string{
		{strings.Join(frappe.InstallApp("erpnext"), " "), strings.Join(frappe.InstallApp("hrms"), " ")},
		{"bench --site shop.localhost install-app erpnext", "bench --site shop.localhost install-app hrms"},
	} {
		first, second := indexOf(lines, order[0]), indexOf(lines, order[1])
		if first < 0 || second < 0 || first > second {
			t.Errorf("execs = %q, want %q before %q", lines, order[0], order[1])
		}
	}
	if got := c.docker.SiteApps(frappeContainer, "shop.localhost"); !slices.Equal(got, []string{"frappe", "erpnext", "hrms"}) {
		t.Errorf("shop.localhost has %q, want frappe, erpnext and hrms", got)
	}
	if got, _ := c.docker.File(frappeContainer, frappe.AppsTxt); got != "frappe\nerpnext\nhrms\n" {
		t.Errorf("apps.txt = %q, want frappe, erpnext and hrms in recipe order", got)
	}
}

func TestEachAppIsSetUpOnceAndASecondStartRepeatsNone(t *testing.T) {
	c := sandbox(t)

	c.newEnvironment(t, "--frappe", "version-15", "--app", "erpnext", "--app", "hrms", "--site", "shop.localhost")

	lines := c.docker.ExecLines()
	for _, want := range []string{
		strings.Join(frappe.NodePackages("frappe"), " "),
		strings.Join(frappe.BuildAssets("frappe"), " "),
		strings.Join(frappe.BuildAssets("erpnext"), " "),
		strings.Join(frappe.NodePackages("hrms"), " "),
		strings.Join(frappe.BuildAssets("hrms"), " "),
	} {
		if n := len(slices.DeleteFunc(slices.Clone(lines), func(l string) bool { return l != want })); n != 1 {
			t.Errorf("%q ran %d times, want once", want, n)
		}
	}
	// erpnext has no package.json, so no yarn runs for it.
	if slices.Contains(lines, strings.Join(frappe.NodePackages("erpnext"), " ")) {
		t.Errorf("yarn ran for erpnext, which has no package.json")
	}
	execs := len(c.docker.Execs)

	c.run(t, "start").assertCode(t, exitcode.CodeOK)

	if again := benchExecs(c.docker.Execs[execs:]); len(again) > 0 {
		t.Errorf("start ran %q again", again)
	}
}

func TestAnAppRequiringOneTheRecipeLacksExits1WithAFixThatWorksFromTheParentDirectory(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "new", "erp-dev", "--frappe", "version-15", "--app", "hrms", "--quiet")

	r.assertCode(t, exitcode.CodeFailed)
	line := lastLine(r.stderr)
	for _, want := range []string{"error: ", "hrms requires the app erpnext",
		"add it with `tamp app add https://github.com/frappe/erpnext` in erp-dev, then run `tamp start` in erp-dev"} {
		if !strings.Contains(line, want) {
			t.Errorf("last stderr line = %q, want %q", line, want)
		}
	}
	if len(c.docker.Ups) > 0 || len(c.docker.Execs) > 0 {
		t.Errorf("tamp built %d projects and ran %q, want nothing", len(c.docker.Ups), c.docker.ExecLines())
	}

	// The user follows the fix: the dependency goes before the app that needs it.
	dir := filepath.Join(c.dir, "erp-dev")
	t.Chdir(dir)
	c.run(t, "app", "add", "https://github.com/frappe/erpnext").assertCode(t, exitcode.CodeOK)
	got := readFile(t, filepath.Join(dir, "tamp.toml"))
	if erpnext, hrms := strings.Index(got, `name = "erpnext"`), strings.Index(got, `name = "hrms"`); erpnext < 0 || hrms < 0 || erpnext > hrms {
		t.Errorf("tamp.toml = %q, want erpnext listed before hrms", got)
	}
	c.run(t, "start").assertCode(t, exitcode.CodeOK)
}

func TestAFreshCloneOfAnAppWithoutPackageJSONGetsItsAssetsBuiltAgain(t *testing.T) {
	c := sandbox(t)
	dir := c.newIn(t, "erp-dev", "--app", "erpnext")
	if err := os.RemoveAll(filepath.Join(dir, "apps", "erpnext")); err != nil {
		t.Fatal(err)
	}
	execs := len(c.docker.Execs)

	c.run(t, "start").assertCode(t, exitcode.CodeOK)

	if again := benchExecs(c.docker.Execs[execs:]); !slices.Contains(again, strings.Join(frappe.BuildAssets("erpnext"), " ")) {
		t.Errorf("execs = %q, want the assets of the fresh erpnext clone built", again)
	}
}

func TestAnAppGivenAsAWindowsStylePathIsClonedAndNamedFromItsPackage(t *testing.T) {
	c := sandbox(t)
	dir := c.newIn(t, "erp-dev")
	// filepath.Join gives backslashes on Windows.
	path := filepath.Join(remotes, "acme", "acme-erp.git")

	c.run(t, "app", "add", path).assertCode(t, exitcode.CodeOK)

	if got := readFile(t, filepath.Join(dir, "tamp.toml")); !strings.Contains(got, `name = "acme"`) {
		t.Errorf("tamp.toml = %q, want the app acme", got)
	}
	clone := filepath.Join(dir, "apps", "acme")
	if got := git(t, clone, "rev-parse", "--is-shallow-repository"); got != "true" {
		t.Errorf("the clone of a local path is not shallow: is-shallow-repository = %s", got)
	}
	if got := git(t, clone, "config", "--get", "remote.upstream.url"); got != path {
		t.Errorf("upstream = %q, want the path as given, %q", got, path)
	}
}

func TestAnUnreadableFolderInAppsWarnsWithWhatGitSaid(t *testing.T) {
	c := sandbox(t)
	dir := c.newIn(t, "erp-dev")
	// An empty .git is not a repository git can read.
	if err := os.MkdirAll(filepath.Join(dir, "apps", "stray", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	r := c.run(t, "start")

	r.assertCode(t, exitcode.CodeOK)
	if !strings.Contains(r.stderr, "warning: git cannot read "+filepath.Join(dir, "apps", "stray")+": fatal:") {
		t.Errorf("stderr = %q, want a warning with what git said", r.stderr)
	}
}

func TestAppAddOnTwoSitesInstallsTheAppOnBothAndRunsTheProcessesAgain(t *testing.T) {
	c := sandbox(t)
	dir := c.newIn(t, "erp-dev", "--site", "shop.localhost", "--site", "blog.localhost")
	execs := len(c.docker.Execs)

	r := c.run(t, "app", "add", acmeURL)

	r.assertCode(t, exitcode.CodeOK)
	if got := readFile(t, filepath.Join(dir, "tamp.toml")); !strings.Contains(got, "  name = \"acme\"\n") {
		t.Errorf("tamp.toml = %q, want it to list acme", got)
	}
	for _, host := range []string{"shop.localhost", "blog.localhost"} {
		if got := c.docker.SiteApps(frappeContainer, host); !slices.Equal(got, []string{"frappe", "acme"}) {
			t.Errorf("%s has %q, want frappe and acme", host, got)
		}
	}
	again := c.docker.ExecLines()[execs:]
	assertPausedAround(t, c, again, strings.Join(frappe.BuildAssets("acme"), " "))
}

func TestTheFirstBuildRestartsNoProcesses(t *testing.T) {
	c := sandbox(t)

	c.newEnvironment(t, "--frappe", "version-15", "--app", acmeURL)

	if !slices.Contains(c.docker.ExecLines(), strings.Join(frappe.InstallApp("acme"), " ")) {
		t.Fatalf("acme was not installed: %q", c.docker.ExecLines())
	}
	if slices.Contains(c.docker.ExecLines(), strings.Join(frappe.RestartProcesses(), " ")) {
		t.Errorf("execs = %q, want no restart", c.docker.ExecLines())
	}
}

// newApp makes apps/acme the way `bench new-app acme` does: a git repository
// of its own with one commit, and no remote.
func newApp(t *testing.T, dir string) string {
	t.Helper()
	app := filepath.Join(dir, "apps", "acme")
	if err := os.MkdirAll(filepath.Join(app, "acme"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"pyproject.toml": "[project]\nname = \"acme\"\n",
		"acme/hooks.py":  "app_name = \"acme\"\n",
	} {
		if err := os.WriteFile(filepath.Join(app, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("git", "init", "--quiet", "--initial-branch", "develop", app)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	git(t, app, "add", "--all")
	git(t, app, "-c", "user.name=me", "-c", "user.email=me@example.com", "commit", "--quiet", "-m", "new app")
	return app
}

func TestAppAddAdoptsAnExistingFolderWithoutCloning(t *testing.T) {
	c := sandbox(t)
	dir := c.newIn(t, "erp-dev", "--site", "shop.localhost")
	app := newApp(t, dir)
	head := git(t, app, "rev-parse", "HEAD")

	r := c.run(t, "app", "add", "acme")

	r.assertCode(t, exitcode.CodeOK)
	if strings.Contains(r.stderr, "Cloning") || strings.Contains(r.stderr, "cloning") {
		t.Errorf("app add cloned:\n%s", r.stderr)
	}
	if got := git(t, app, "rev-parse", "HEAD"); got != head {
		t.Errorf("HEAD = %s, want %s", got, head)
	}
	if got := appsFolders(t, dir); !slices.Equal(got, []string{"acme", "frappe"}) {
		t.Errorf("apps/ holds %q, want acme and frappe", got)
	}
	want := "[[apps]]\n  name = \"acme\"\n  url = \"https://github.com/frappe/acme\"\n  branch = \"develop\"\n"
	if got := readFile(t, filepath.Join(dir, "tamp.toml")); !strings.Contains(got, want) {
		t.Errorf("tamp.toml = %q, want it to hold %q", got, want)
	}
	if got := c.docker.SiteApps(frappeContainer, "shop.localhost"); !slices.Equal(got, []string{"frappe", "acme"}) {
		t.Errorf("shop.localhost has %q, want frappe and acme", got)
	}
}

func TestAppAddOfAnAppTheRecipeListsExits1(t *testing.T) {
	c := sandbox(t)
	dir := c.newIn(t, "erp-dev", "--app", acmeURL)
	before := readFile(t, filepath.Join(dir, "tamp.toml"))

	r := c.run(t, "app", "add", acmeURL+"#develop")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertOneLineError(t, "erp-dev already lists the app acme")
	if got := readFile(t, filepath.Join(dir, "tamp.toml")); got != before {
		t.Errorf("tamp.toml = %q, want it unchanged", got)
	}
}

func TestAppAddOfAnUnreachableRepositoryExits1AndLeavesNoFolder(t *testing.T) {
	c := sandbox(t)
	dir := c.newIn(t, "erp-dev")
	before := readFile(t, filepath.Join(dir, "tamp.toml"))

	r := c.run(t, "app", "add", "nosuchapp", "--quiet")

	r.assertCode(t, exitcode.CodeFailed)
	if line := lastLine(r.stderr); !strings.Contains(line, "cannot clone https://github.com/frappe/nosuchapp") ||
		!strings.HasSuffix(line, "fix what git says above, then run `tamp app add nosuchapp`") {
		t.Errorf("last stderr line = %q, want the failed clone and the fix to run app add again", line)
	}
	if got := appsFolders(t, dir); !slices.Equal(got, []string{"frappe"}) {
		t.Errorf("apps/ holds %q, want only frappe", got)
	}
	if got := readFile(t, filepath.Join(dir, "tamp.toml")); got != before {
		t.Errorf("tamp.toml = %q, want it unchanged", got)
	}
}

func TestAFailedAppCloneInNewLeavesTheRecipeAndStartResumesOnceTheURLIsFixed(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "new", "erp-dev", "--frappe", "version-15", "--app", acmeURL, "--app", "nosuchapp", "--quiet")

	r.assertCode(t, exitcode.CodeFailed)
	dir := filepath.Join(c.dir, "erp-dev")
	recipe := filepath.Join(dir, "tamp.toml")
	wantFix := "fix what git says above, or correct the app's url in " + recipe + ", then run `tamp start` in erp-dev"
	if line := lastLine(r.stderr); !strings.Contains(line, "cannot clone https://github.com/frappe/nosuchapp") || !strings.HasSuffix(line, wantFix) {
		t.Errorf("last stderr line = %q, want the failed clone and the fix %q", line, wantFix)
	}
	if got := readFile(t, recipe); !strings.Contains(got, "  name = \"acme\"\n") || !strings.Contains(got, "  url = \"https://github.com/frappe/nosuchapp\"\n") {
		t.Errorf("tamp.toml = %q, want acme cloned and recorded, and nosuchapp kept by its url", got)
	}

	// Until the url is fixed, start fails the same way.
	t.Chdir(dir)
	again := c.run(t, "start", "--quiet")
	again.assertCode(t, exitcode.CodeFailed)
	if line := lastLine(again.stderr); !strings.HasSuffix(line, "or correct the app's url in "+recipe+", then run `tamp start`") {
		t.Errorf("last stderr line = %q, want the same fix", line)
	}

	// The user follows the fix.
	body := strings.Replace(readFile(t, recipe), "https://github.com/frappe/nosuchapp", "https://github.com/frappe/erpnext", 1)
	if err := os.WriteFile(recipe, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	c.run(t, "start").assertCode(t, exitcode.CodeOK)
	if got := readFile(t, recipe); !strings.Contains(got, "  name = \"erpnext\"\n  url = \"https://github.com/frappe/erpnext\"\n  branch = \"develop\"\n") {
		t.Errorf("tamp.toml = %q, want erpnext recorded with its name and default branch", got)
	}
}

func TestAddingFrappeAsAnAppExits2AndClonesNothing(t *testing.T) {
	for _, spec := range []string{"frappe", "https://github.com/frappe/frappe#develop"} {
		t.Run(spec, func(t *testing.T) {
			c := sandbox(t)
			dir := c.newIn(t, "erp-dev")
			calls := len(c.docker.Calls)

			r := c.run(t, "app", "add", spec)

			r.assertCode(t, exitcode.CodeUsage)
			r.assertOneLineError(t, "Frappe is not an app to add", "`frappe` key")
			if got := c.callsSince(calls); len(got) > 0 {
				t.Errorf("Docker calls = %v, want none", got)
			}
			if got := appsFolders(t, dir); !slices.Equal(got, []string{"frappe"}) {
				t.Errorf("apps/ holds %q, want only frappe", got)
			}
		})
	}
}

func TestARequiredAppNamedByARepositoryThatHoldsAnotherPackageNamesTheHooksToFix(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "new", "erp-dev", "--frappe", "version-15", "--app", acmeURL, "--app", acmeSSH+"acme-hr.git", "--quiet")

	r.assertCode(t, exitcode.CodeFailed)
	hooks := filepath.Join(c.dir, "erp-dev", "apps", "acme_hr", "acme_hr", "hooks.py")
	if line := lastLine(r.stderr); !strings.Contains(line, `name it "acme" in required_apps in `+hooks) {
		t.Errorf("last stderr line = %q, want the fix to name acme in %s", line, hooks)
	}
}

func TestANewBenchVolumeRebuildsTheAssetsOfKeptClones(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev", "--app", "erpnext", "--app", "hrms")
	c.run(t, "remove", "--yes").assertCode(t, exitcode.CodeOK)
	execs := len(c.docker.Execs)

	c.run(t, "start").assertCode(t, exitcode.CodeOK)

	again := benchExecs(c.docker.Execs[execs:])
	for _, app := range []string{"frappe", "erpnext", "hrms"} {
		if !slices.Contains(again, strings.Join(frappe.BuildAssets(app), " ")) {
			t.Errorf("execs = %q, want the assets of %s built into the new volume", again, app)
		}
	}
	// The clones kept their node packages.
	for _, app := range []string{"frappe", "hrms"} {
		if slices.Contains(again, strings.Join(frappe.NodePackages(app), " ")) {
			t.Errorf("execs = %q, want the node packages of %s left as the clone holds them", again, app)
		}
	}
}

func TestAnAppSpecWithAnEmptyBranchExits2AndWritesNothing(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "new", "erp-dev", "--frappe", "version-15", "--app", "hrms#")

	r.assertCode(t, exitcode.CodeUsage)
	r.assertOneLineError(t, "hrms#")
	assertNotExist(t, filepath.Join(c.dir, "erp-dev"))
}

func TestAppRemoveWithoutYesExits5AndChangesNothing(t *testing.T) {
	c := sandbox(t)
	dir := c.newIn(t, "erp-dev", "--app", acmeURL, "--site", "shop.localhost")
	before := readFile(t, filepath.Join(dir, "tamp.toml"))
	calls := len(c.docker.Calls)

	r := c.run(t, "app", "remove", "acme")

	r.assertCode(t, exitcode.CodeNeedsYes)
	r.assertOneLineError(t, "acme", "`tamp app remove acme --yes`")
	assertOnlyReads(t, c.callsSince(calls))
	if got := readFile(t, filepath.Join(dir, "tamp.toml")); got != before {
		t.Errorf("tamp.toml = %q, want it unchanged", got)
	}
}

func TestAppRemoveUninstallsFromEverySiteAndTheBenchAndKeepsTheClone(t *testing.T) {
	c := sandbox(t)
	dir := c.newIn(t, "erp-dev", "--app", "erpnext", "--app", acmeURL, "--site", "shop.localhost", "--site", "blog.localhost")
	clone := filepath.Join(dir, "apps", "acme")
	head := git(t, clone, "rev-parse", "HEAD")
	execs := len(c.docker.Execs)

	r := c.run(t, "app", "remove", "acme", "--yes")

	r.assertCode(t, exitcode.CodeOK)
	again := c.docker.ExecLines()[execs:]
	for _, host := range []string{"shop.localhost", "blog.localhost"} {
		uninstall := indexOf(again, strings.Join(frappe.UninstallSiteApp(host, "acme"), " "))
		if uninstall < 0 || uninstall > indexOf(again, strings.Join(frappe.UninstallApp("acme"), " ")) {
			t.Errorf("execs = %q, want acme uninstalled from %s before the bench", again, host)
		}
		if got := c.docker.SiteApps(frappeContainer, host); !slices.Equal(got, []string{"frappe", "erpnext"}) {
			t.Errorf("%s has %q, want frappe and erpnext", host, got)
		}
	}
	if got, _ := c.docker.File(frappeContainer, frappe.AppsTxt); got != "frappe\nerpnext\n" {
		t.Errorf("apps.txt = %q, want acme gone", got)
	}
	if got := readFile(t, filepath.Join(dir, "tamp.toml")); strings.Contains(got, "acme") || !strings.Contains(got, "erpnext") {
		t.Errorf("tamp.toml = %q, want acme gone and erpnext kept", got)
	}
	if got := git(t, clone, "rev-parse", "HEAD"); got != head {
		t.Errorf("the clone's HEAD = %s, want %s", got, head)
	}
	if !strings.Contains(r.stdout, clone+"\n") {
		t.Errorf("stdout = %q, want the clone's path %s", r.stdout, clone)
	}

	// The kept clone is now unlisted, and app add adopts it again.
	start := c.run(t, "start")
	if !strings.Contains(start.stderr, "warning: "+clone+" is not in the recipe") {
		t.Errorf("start stderr = %q, want a warning about %s", start.stderr, clone)
	}
	add := c.run(t, "app", "add", acmeURL)
	add.assertCode(t, exitcode.CodeOK)
	if strings.Contains(add.stderr, "Cloning") {
		t.Errorf("app add cloned the kept clone again:\n%s", add.stderr)
	}
	if got := git(t, clone, "rev-parse", "HEAD"); got != head {
		t.Errorf("the clone's HEAD = %s after adding it again, want %s", got, head)
	}
	if got := c.docker.SiteApps(frappeContainer, "shop.localhost"); !slices.Equal(got, []string{"frappe", "erpnext", "acme"}) {
		t.Errorf("shop.localhost has %q after adding acme again", got)
	}
}

func TestAppRemoveOfAnAppTheRecipeLacksExits3(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev")

	r := c.run(t, "app", "remove", "acme", "--yes")

	r.assertCode(t, exitcode.CodeNotFound)
	r.assertOneLineError(t, "erp-dev has no app acme", "`tamp app list`")
}

func TestStartWarnsAboutBranchDriftAndUnlistedFoldersAndTouchesNoClone(t *testing.T) {
	c := sandbox(t)
	dir := c.newIn(t, "erp-dev", "--app", acmeURL)
	acme, fw := filepath.Join(dir, "apps", "acme"), filepath.Join(dir, "apps", "frappe")
	git(t, fw, "checkout", "--quiet", "-b", "mine")
	git(t, acme, "remote", "set-branches", "upstream", "develop")
	git(t, acme, "fetch", "--quiet", "--depth", "1", "upstream", "develop")
	git(t, acme, "checkout", "--quiet", "-b", "develop", "upstream/develop")
	draft := filepath.Join(acme, "draft.py")
	if err := os.WriteFile(draft, []byte("draft"), 0o644); err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(dir, "apps", "stray")
	if err := os.MkdirAll(stray, 0o755); err != nil {
		t.Fatal(err)
	}
	heads := map[string]string{acme: git(t, acme, "rev-parse", "HEAD"), fw: git(t, fw, "rev-parse", "HEAD")}

	r := c.run(t, "start")

	r.assertCode(t, exitcode.CodeOK)
	for _, want := range []string{
		"warning: " + fw + " is on mine, but the recipe names version-15",
		"warning: " + acme + " is on develop, but the recipe names main",
		"warning: " + stray + " is not in the recipe: add it with `tamp app add <url>`, or delete the folder",
	} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("stderr = %q, want %q", r.stderr, want)
		}
	}
	for clone, head := range heads {
		if got := git(t, clone, "rev-parse", "HEAD"); got != head {
			t.Errorf("%s HEAD = %s, want %s", clone, got, head)
		}
	}
	if got := git(t, acme, "branch", "--show-current"); got != "develop" {
		t.Errorf("acme is on %s, want develop", got)
	}
	if got := readFile(t, draft); got != "draft" {
		t.Errorf("draft.py = %q, want it unchanged", got)
	}
}

func TestTheUnlistedFolderWarningNamesTheCloneURL(t *testing.T) {
	c := sandbox(t)
	dir := c.newIn(t, "erp-dev")
	other := filepath.Join(c.dir, "other")
	c.run(t, "new", other, "--frappe", "version-15", "--app", acmeURL).assertCode(t, exitcode.CodeOK)
	if err := os.Rename(filepath.Join(other, "apps", "acme"), filepath.Join(dir, "apps", "acme")); err != nil {
		t.Fatal(err)
	}

	r := c.run(t, "start")

	if want := "add it with `tamp app add " + acmeURL + "`"; !strings.Contains(r.stderr, want) {
		t.Errorf("stderr = %q, want %q", r.stderr, want)
	}
}

func TestABareAppCommandPrintsHelpAndExits2(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "app")

	r.assertCode(t, exitcode.CodeUsage)
	if r.stdout != "" {
		t.Errorf("stdout = %q, want empty", r.stdout)
	}
	for _, want := range []string{"add", "list", "remove", "error: missing an app command"} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("stderr = %q, want %q", r.stderr, want)
		}
	}
}

func TestAppListPrintsFrappeAndEachAppWithItsBranch(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev", "--app", "erpnext", "--app", acmeURL+"#develop")

	r := c.run(t, "app", "list")

	r.assertCode(t, exitcode.CodeOK)
	want := "frappe   version-15  https://github.com/frappe/frappe\n" +
		"erpnext  develop     https://github.com/frappe/erpnext\n" +
		"acme     develop     " + acmeURL + "\n"
	if r.stdout != want {
		t.Errorf("stdout = %q, want %q", r.stdout, want)
	}
}

func TestSiteAddAndRemoveKeepTheRecipesApps(t *testing.T) {
	c := sandbox(t)
	dir := c.newIn(t, "erp-dev", "--app", acmeURL)
	apps := "[[apps]]\n  name = \"acme\"\n  url = \"" + acmeURL + "\"\n  branch = \"main\"\n"

	c.run(t, "site", "add", "shop.localhost").assertCode(t, exitcode.CodeOK)
	if got := readFile(t, filepath.Join(dir, "tamp.toml")); !strings.Contains(got, apps) {
		t.Errorf("after site add, tamp.toml = %q, want it to keep %q", got, apps)
	}
	if got := c.docker.SiteApps(frappeContainer, "shop.localhost"); !slices.Equal(got, []string{"frappe", "acme"}) {
		t.Errorf("shop.localhost has %q, want frappe and acme", got)
	}
	c.run(t, "site", "remove", "shop.localhost", "--yes").assertCode(t, exitcode.CodeOK)
	if got := readFile(t, filepath.Join(dir, "tamp.toml")); !strings.Contains(got, apps) {
		t.Errorf("after site remove, tamp.toml = %q, want it to keep %q", got, apps)
	}
}

func TestARecipeWithAnUnknownKeyExits1RatherThanLoseIt(t *testing.T) {
	c := sandbox(t)
	dir := c.newIn(t, "erp-dev")
	body := readFile(t, filepath.Join(dir, "tamp.toml")) + "\n[[sites]]\n  host = \"shop.localhost\"\n  title = \"Shop\"\n"
	if err := os.WriteFile(filepath.Join(dir, "tamp.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	r := c.run(t, "site", "add", "blog.localhost")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertOneLineError(t, "sites.title")
	if got := readFile(t, filepath.Join(dir, "tamp.toml")); got != body {
		t.Errorf("tamp.toml = %q, want it unchanged", got)
	}
}

func TestAFreshFrappeCloneOnAnInheritedBenchGetsItsNodePackagesAndAssets(t *testing.T) {
	c := sandbox(t)
	dir := c.newIn(t, "erp-dev", "--app", "hrms", "--app", "erpnext")
	// The directory is deleted without `tamp remove` and made again: the
	// bench volume survives, and the clones are new.
	if err := os.RemoveAll(filepath.Join(dir, "apps")); err != nil {
		t.Fatal(err)
	}
	execs := len(c.docker.Execs)

	c.run(t, "start").assertCode(t, exitcode.CodeOK)

	again := benchExecs(c.docker.Execs[execs:])
	for _, app := range []string{"frappe", "hrms"} {
		yarn, build := indexOf(again, strings.Join(frappe.NodePackages(app), " ")), indexOf(again, strings.Join(frappe.BuildAssets(app), " "))
		if yarn < 0 || build < yarn {
			t.Errorf("execs = %q, want the node packages of %s installed and then its assets built", again, app)
		}
	}
	if slices.ContainsFunc(again, func(l string) bool {
		return strings.Contains(l, "bench init") || strings.HasPrefix(l, "uv pip install")
	}) {
		t.Errorf("execs = %q, want no bench init and no Python install", again)
	}
}

// assertPausedAround checks that the bench's processes stopped before step
// and run again after it.
func assertPausedAround(t *testing.T, c *cli, execs []string, step string) {
	t.Helper()
	stop, at, resume := indexOf(execs, strings.Join(frappe.StopProcesses(), " ")), indexOf(execs, step), indexOf(execs, "rm -f "+frappe.Paused)
	if stop < 0 || at < stop || resume < at {
		t.Errorf("execs = %q, want the processes stopped, then %q, then the pause lifted", execs, step)
	}
	if _, ok := c.docker.File(frappeContainer, frappe.Paused); ok {
		t.Error("the bench is still paused")
	}
}

func TestAFreshCloneOnARunningBenchGetsItsNodeStepsWithTheProcessesStopped(t *testing.T) {
	c := sandbox(t)
	dir := c.newIn(t, "erp-dev", "--app", "hrms", "--app", "erpnext")
	if compose := c.docker.UpsOf("erp-dev")[0].Compose; !strings.Contains(compose, "[ ! -e "+frappe.Paused+" ]") {
		t.Errorf("the frappe container does not hold off while paused:\n%s", compose)
	}
	if slices.Contains(c.docker.ExecLines(), strings.Join(frappe.StopProcesses(), " ")) {
		t.Error("the first build stopped processes, but none ran yet")
	}
	if err := os.RemoveAll(filepath.Join(dir, "apps")); err != nil {
		t.Fatal(err)
	}
	execs := len(c.docker.Execs)

	c.run(t, "start").assertCode(t, exitcode.CodeOK)

	assertPausedAround(t, c, c.docker.ExecLines()[execs:], strings.Join(frappe.NodePackages("frappe"), " "))
}

func TestAStartThatFailsWhilePausedLiftsThePauseWhenItResumes(t *testing.T) {
	c := sandbox(t)
	dir := c.newIn(t, "erp-dev", "--app", "hrms", "--app", "erpnext")
	if err := os.RemoveAll(filepath.Join(dir, "apps")); err != nil {
		t.Fatal(err)
	}
	c.docker.ExecFails = map[string]error{"yarn install": exitcode.New(exitcode.CodeFailed, "yarn failed", "fix what the output above names")}
	c.run(t, "start").assertCode(t, exitcode.CodeFailed)
	if _, ok := c.docker.File(frappeContainer, frappe.Paused); !ok {
		t.Fatal("the failed start left the processes running")
	}
	c.docker.ExecFails = nil

	c.run(t, "start").assertCode(t, exitcode.CodeOK)

	if _, ok := c.docker.File(frappeContainer, frappe.Paused); ok {
		t.Error("the bench is still paused after start resumed")
	}
}

func TestAppAddOfAListedAppAtAnotherBranchNamesWhereTheBranchChanges(t *testing.T) {
	c := sandbox(t)
	dir := c.newIn(t, "erp-dev", "--app", acmeURL)

	r := c.run(t, "app", "add", acmeURL+"#develop")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertOneLineError(t, "erp-dev already lists the app acme at main",
		"to use develop, set its branch in "+filepath.Join(dir, "tamp.toml")+" and check out develop in "+filepath.Join(dir, "apps", "acme")+", then run `tamp start`")
}

func TestAPauseLeftByAFailedStartIsLiftedEvenWhenNothingIsLeftToBuild(t *testing.T) {
	c := sandbox(t)
	dir := c.newIn(t, "erp-dev", "--app", "erpnext")
	if err := os.RemoveAll(filepath.Join(dir, "apps")); err != nil {
		t.Fatal(err)
	}
	lift := "rm -f " + frappe.Paused
	c.docker.ExecFails = map[string]error{lift: exitcode.New(exitcode.CodeFailed, "rm failed", "fix what the output above names")}
	c.run(t, "start").assertCode(t, exitcode.CodeFailed)
	c.docker.ExecFails = nil
	execs := len(c.docker.Execs)

	c.run(t, "start").assertCode(t, exitcode.CodeOK)

	again := c.docker.ExecLines()[execs:]
	if slices.Contains(again, strings.Join(frappe.StopProcesses(), " ")) || !slices.Contains(again, lift) {
		t.Errorf("execs = %q, want the pause lifted with nothing stopped or built", again)
	}
	if _, ok := c.docker.File(frappeContainer, frappe.Paused); ok {
		t.Error("the bench is still paused")
	}
}

func TestStartNamesEachRecipeAppItClones(t *testing.T) {
	c := sandbox(t)
	dir := c.newIn(t, "erp-dev", "--app", "erpnext")
	if err := os.RemoveAll(filepath.Join(dir, "apps", "erpnext")); err != nil {
		t.Fatal(err)
	}

	r := c.run(t, "start")

	r.assertCode(t, exitcode.CodeOK)
	if !strings.Contains(r.stderr, "cloning https://github.com/frappe/erpnext\n") {
		t.Errorf("stderr = %q, want a step naming the clone", r.stderr)
	}
}

// copyRemote clones a bare remote to a new bare repository at path.
func copyRemote(t *testing.T, from, path string) {
	t.Helper()
	if out, err := exec.Command("git", "clone", "--quiet", "--bare", from, path).CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v\n%s", err, out)
	}
}

func TestARelativeAppPathFromTheParentDirectoryIsClonedShallowAndRecordedAbsolute(t *testing.T) {
	c := sandbox(t)
	repo := filepath.Join(c.dir, "src", "acme-erp.git")
	copyRemote(t, filepath.Join(remotes, "acme", "acme-erp.git"), repo)
	specs := []string{"./src/acme-erp.git"}
	if runtime.GOOS == "windows" {
		specs = append(specs, `.\src\acme-erp.git`)
	}
	for i, spec := range specs {
		t.Run(spec, func(t *testing.T) {
			t.Chdir(c.dir)
			name := "erp" + strconv.Itoa(i)
			c.run(t, "new", name, "--frappe", "version-15", "--app", spec).assertCode(t, exitcode.CodeOK)

			dir := filepath.Join(c.dir, name)
			if got := readFile(t, filepath.Join(dir, "tamp.toml")); !strings.Contains(got, "url = "+strconv.Quote(repo)) {
				t.Errorf("tamp.toml = %q, want the url %q", got, repo)
			}
			if got := git(t, filepath.Join(dir, "apps", "acme"), "rev-parse", "--is-shallow-repository"); got != "true" {
				t.Errorf("is-shallow-repository = %s, want a shallow clone", got)
			}
		})
	}
}

func TestAppAddOfAParentRelativePathResolvesAgainstTheWorkingDirectory(t *testing.T) {
	c := sandbox(t)
	repo := filepath.Join(c.dir, "acme-erp.git")
	copyRemote(t, filepath.Join(remotes, "acme", "acme-erp.git"), repo)
	dir := c.newIn(t, "erp-dev")
	spec := "../acme-erp.git"
	if runtime.GOOS == "windows" {
		spec = `..\acme-erp.git`
	}

	c.run(t, "app", "add", spec).assertCode(t, exitcode.CodeOK)

	if got := readFile(t, filepath.Join(dir, "tamp.toml")); !strings.Contains(got, "url = "+strconv.Quote(repo)) {
		t.Errorf("tamp.toml = %q, want the url %q", got, repo)
	}
	if got := git(t, filepath.Join(dir, "apps", "acme"), "rev-parse", "--is-shallow-repository"); got != "true" {
		t.Errorf("is-shallow-repository = %s, want a shallow clone", got)
	}
}

func TestALocalPathHoldingPercentAndSpaceIsCloned(t *testing.T) {
	c := sandbox(t)
	repo := filepath.Join(c.dir, "my 100%20 repos", "acme-erp.git")
	copyRemote(t, filepath.Join(remotes, "acme", "acme-erp.git"), repo)
	dir := c.newIn(t, "erp-dev")

	c.run(t, "app", "add", repo).assertCode(t, exitcode.CodeOK)

	if got := git(t, filepath.Join(dir, "apps", "acme"), "config", "--get", "remote.upstream.url"); got != repo {
		t.Errorf("upstream = %q, want %q", got, repo)
	}
}
