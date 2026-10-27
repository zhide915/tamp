package main

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/frappe"
	"github.com/zhide915/tamp/internal/router"
)

func execsStartingWith(lines []string, prefix string) []string {
	var out []string
	for _, l := range lines {
		if strings.HasPrefix(l, prefix) {
			out = append(out, l)
		}
	}
	return out
}

func TestSiteAddWritesTheHostIntoTheRecipeCreatesTheSiteAndRoutesIt(t *testing.T) {
	c := sandbox(t)
	dir := c.newIn(t, "erp-dev", "--site", "shop.localhost")
	execs := len(c.docker.Execs)

	r := c.run(t, "site", "add", "blog.localhost")

	r.assertCode(t, exitcode.CodeOK)
	want := "frappe = \"version-15\"\n\n[[sites]]\n  host = \"shop.localhost\"\n\n[[sites]]\n  host = \"blog.localhost\"\n"
	if got := readFile(t, filepath.Join(dir, "tamp.toml")); got != want {
		t.Errorf("tamp.toml = %q, want %q", got, want)
	}
	again := c.docker.ExecLines()[execs:]
	if got := execsStartingWith(again, "bench new-site "); len(got) != 1 || !strings.HasPrefix(got[0], "bench new-site blog.localhost ") {
		t.Errorf("new-site execs = %q, want one for blog.localhost", got)
	}
	if _, ok := c.docker.File(frappeContainer, frappe.SiteInstalled("blog.localhost")); !ok {
		t.Error("blog.localhost was not finished")
	}
	caddyfile := c.caddyfile(t)
	assertRoutedTo(t, caddyfile, "blog.localhost", frappeContainer)
	assertRoutedTo(t, caddyfile, "shop.localhost", frappeContainer)
	if got := routerExecs(c.docker.Execs[execs:]); !slices.Equal(got, []string{reload}) {
		t.Errorf("router execs = %q, want one %q", got, reload)
	}
}

func TestSiteAddWithAHostThatIsNotLocalhostExits2AndChangesNothing(t *testing.T) {
	for _, host := range []string{"shop.example.com", "localhost", "Shop.localhost"} {
		t.Run(host, func(t *testing.T) {
			c := sandbox(t)
			dir := c.newIn(t, "erp-dev")
			before := readFile(t, filepath.Join(dir, "tamp.toml"))
			calls := len(c.docker.Calls)

			r := c.run(t, "site", "add", host)

			r.assertCode(t, exitcode.CodeUsage)
			r.assertOneLineError(t, host, "`.localhost`")
			if got := readFile(t, filepath.Join(dir, "tamp.toml")); got != before {
				t.Errorf("tamp.toml = %q, want it unchanged", got)
			}
			if got := c.callsSince(calls); len(got) > 0 {
				t.Errorf("Docker calls = %v, want none", got)
			}
		})
	}
}

func TestSiteAddWithAHostAStoppedEnvironmentsRecipeListsExits1AndChangesNothing(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "other", "--site", "shop.localhost")
	c.run(t, "stop").assertCode(t, exitcode.CodeOK)
	dir := c.newIn(t, "erp-dev")
	before := readFile(t, filepath.Join(dir, "tamp.toml"))
	caddyfile := c.caddyfile(t)
	calls := len(c.docker.Calls)

	r := c.run(t, "site", "add", "shop.localhost")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertOneLineError(t, "other", "shop.localhost")
	if got := readFile(t, filepath.Join(dir, "tamp.toml")); got != before {
		t.Errorf("tamp.toml = %q, want it unchanged", got)
	}
	if got := c.caddyfile(t); got != caddyfile {
		t.Errorf("the Caddyfile changed:\n%s", got)
	}
	assertOnlyReads(t, c.callsSince(calls))
}

func TestSiteAddWithAHostTheRecipeAlreadyListsExits1(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev", "--site", "shop.localhost")
	calls := len(c.docker.Calls)

	r := c.run(t, "site", "add", "shop.localhost")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertOneLineError(t, "erp-dev already lists the site shop.localhost")
	assertOnlyReads(t, c.callsSince(calls))
}

func TestSiteAddWithEAddsTheSiteFromAnotherDirectory(t *testing.T) {
	c := sandbox(t)
	dir := c.newIn(t, "erp-dev")
	t.Chdir(c.dir)

	c.run(t, "site", "add", "blog.localhost", "-e", "erp-dev").assertCode(t, exitcode.CodeOK)

	if got := readFile(t, filepath.Join(dir, "tamp.toml")); !strings.Contains(got, `host = "blog.localhost"`) {
		t.Errorf("tamp.toml = %q, want it to list blog.localhost", got)
	}
	assertRoutedTo(t, c.caddyfile(t), "blog.localhost", frappeContainer)
}

func TestSiteRemoveWithoutYesExits5AndChangesNothing(t *testing.T) {
	c := sandbox(t)
	dir := c.newIn(t, "erp-dev", "--site", "shop.localhost")
	before := readFile(t, filepath.Join(dir, "tamp.toml"))
	caddyfile := c.caddyfile(t)
	calls := len(c.docker.Calls)

	r := c.run(t, "site", "remove", "shop.localhost")

	r.assertCode(t, exitcode.CodeNeedsYes)
	r.assertOneLineError(t, "shop.localhost", "`tamp site remove shop.localhost --yes`")
	assertOnlyReads(t, c.callsSince(calls))
	if got := readFile(t, filepath.Join(dir, "tamp.toml")); got != before {
		t.Errorf("tamp.toml = %q, want it unchanged", got)
	}
	if got := c.caddyfile(t); got != caddyfile {
		t.Errorf("the Caddyfile changed:\n%s", got)
	}
}

func TestSiteRemoveWithYesDropsTheSiteEditsTheRecipeAndTheRouteIsGone(t *testing.T) {
	c := sandbox(t)
	dir := c.newIn(t, "erp-dev", "--site", "shop.localhost", "--site", "blog.localhost")
	execs := len(c.docker.Execs)

	r := c.run(t, "site", "remove", "shop.localhost", "--yes")

	r.assertCode(t, exitcode.CodeOK)
	again := c.docker.ExecLines()[execs:]
	want := "bench drop-site shop.localhost --db-root-password password --no-backup --force"
	if got := execsStartingWith(again, "bench "); !slices.Equal(got, []string{want}) {
		t.Errorf("bench execs = %q, want only %q", got, want)
	}
	if _, ok := c.docker.File(frappeContainer, frappe.SiteConfig("shop.localhost")); ok {
		t.Error("shop.localhost is still in the bench")
	}
	if got, want := readFile(t, filepath.Join(dir, "tamp.toml")), "frappe = \"version-15\"\n\n[[sites]]\n  host = \"blog.localhost\"\n"; got != want {
		t.Errorf("tamp.toml = %q, want %q", got, want)
	}
	caddyfile := c.caddyfile(t)
	if _, ok := siteBlocks(caddyfile)["shop.localhost"]; ok {
		t.Errorf("the Caddyfile still routes shop.localhost:\n%s", caddyfile)
	}
	assertRoutedTo(t, caddyfile, "blog.localhost", frappeContainer)
	if got := routerExecs(c.docker.Execs[execs:]); !slices.Equal(got, []string{reload}) {
		t.Errorf("router execs = %q, want one %q", got, reload)
	}
}

func TestSiteRemoveOfASiteNeitherTheRecipeNorTheBenchHoldsExits3(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev", "--site", "shop.localhost")
	calls := len(c.docker.Calls)

	r := c.run(t, "site", "remove", "blog.localhost", "--yes")

	r.assertCode(t, exitcode.CodeNotFound)
	r.assertOneLineError(t, "erp-dev has no site blog.localhost", "`tamp site list`")
	assertOnlyReads(t, c.callsSince(calls))
}

func TestSiteRemoveInAStoppedEnvironmentExits1WithTheFixToStartItAndChangesNothing(t *testing.T) {
	c := sandbox(t)
	dir := c.newIn(t, "erp-dev", "--site", "shop.localhost")
	c.run(t, "stop").assertCode(t, exitcode.CodeOK)
	before := readFile(t, filepath.Join(dir, "tamp.toml"))
	calls := len(c.docker.Calls)

	r := c.run(t, "site", "remove", "shop.localhost", "--yes")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertOneLineError(t, "erp-dev is stopped", "run `tamp start`, then `tamp site remove shop.localhost --yes`")
	assertOnlyReads(t, c.callsSince(calls))
	if got := readFile(t, filepath.Join(dir, "tamp.toml")); got != before {
		t.Errorf("tamp.toml = %q, want it unchanged", got)
	}
}

// A site the bench holds but the recipe does not list is what start warns
// about, and remove is the fix that warning names.
func TestSiteRemoveDropsASiteOnlyTheBenchHolds(t *testing.T) {
	c := sandbox(t)
	dir := c.newIn(t, "erp-dev", "--site", "shop.localhost")
	writeRecipe(t, dir, "frappe = \"version-15\"\n")
	execs := len(c.docker.Execs)

	c.run(t, "site", "remove", "shop.localhost", "--yes").assertCode(t, exitcode.CodeOK)

	if got := execsStartingWith(c.docker.ExecLines()[execs:], "bench drop-site shop.localhost "); len(got) != 1 {
		t.Errorf("drop-site execs = %q, want one", got)
	}
	if got := readFile(t, filepath.Join(dir, "tamp.toml")); got != "frappe = \"version-15\"\n" {
		t.Errorf("tamp.toml = %q, want it unchanged", got)
	}
}

// A site the recipe lists that was never built has nothing to drop.
func TestSiteRemoveOfASiteNeverBuiltOnlyEditsTheRecipe(t *testing.T) {
	c := sandbox(t)
	dir := c.newIn(t, "erp-dev")
	writeRecipe(t, dir, "frappe = \"version-15\"\n\n[[sites]]\n  host = \"shop.localhost\"\n")
	execs := len(c.docker.Execs)

	c.run(t, "site", "remove", "shop.localhost", "--yes").assertCode(t, exitcode.CodeOK)

	if got := execsStartingWith(c.docker.ExecLines()[execs:], "bench "); len(got) > 0 {
		t.Errorf("bench execs = %q, want none", got)
	}
	if got := readFile(t, filepath.Join(dir, "tamp.toml")); got != "frappe = \"version-15\"\n" {
		t.Errorf("tamp.toml = %q, want the site gone", got)
	}
}

func TestABareSiteCommandPrintsHelpAndExits2(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "site")

	r.assertCode(t, exitcode.CodeUsage)
	for _, sub := range []string{"add", "list", "remove"} {
		if !strings.Contains(r.stderr, "  "+sub+" ") {
			t.Errorf("stderr = %q, want help listing %s", r.stderr, sub)
		}
	}
	if line := lastLine(r.stderr); !strings.HasPrefix(line, "error: ") {
		t.Errorf("last stderr line = %q, want an error", line)
	}
}

func TestAnUnknownSiteCommandExits2(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "site", "drop", "shop.localhost")

	r.assertCode(t, exitcode.CodeUsage)
	r.assertOneLineError(t, `"drop"`)
}

func TestSiteListPrintsEachSiteWithItsURL(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev", "--site", "shop.localhost", "--site", "erp.localhost")

	r := c.run(t, "site", "list")

	r.assertCode(t, exitcode.CodeOK)
	if want := "shop.localhost  http://shop.localhost\nerp.localhost   http://erp.localhost\n"; r.stdout != want {
		t.Errorf("stdout = %q, want %q", r.stdout, want)
	}
}

func TestFollowingThePartlyCreatedSiteFixRebuildsTheSite(t *testing.T) {
	c := sandbox(t)
	c.docker.ExecFails = map[string]error{"new-site": exitcode.New(exitcode.CodeFailed,
		"bench new-site shop.localhost exited 1 in erp-dev-frappe-1", "fix what the output above names")}
	c.run(t, "new", "erp-dev", "--frappe", "version-15", "--site", "shop.localhost").assertCode(t, exitcode.CodeFailed)
	c.docker.ExecFails = nil
	dir := filepath.Join(c.dir, "erp-dev")
	t.Chdir(dir)
	r := c.run(t, "start", "--quiet")
	r.assertCode(t, exitcode.CodeFailed)

	// The user runs each command the fix names, as printed.
	commands := regexp.MustCompile("`tamp ([^`]*)`").FindAllStringSubmatch(r.stderr, -1)
	if len(commands) != 2 {
		t.Fatalf("stderr = %q, want a fix of two commands", r.stderr)
	}
	execs := len(c.docker.Execs)
	for _, cmd := range commands {
		c.run(t, strings.Fields(cmd[1])...).assertCode(t, exitcode.CodeOK)
	}

	again := c.docker.ExecLines()[execs:]
	if got := execsStartingWith(again, "bench drop-site shop.localhost "); len(got) != 1 {
		t.Errorf("drop-site execs = %q, want one for shop.localhost", got)
	}
	if got := execsStartingWith(again, "bench new-site shop.localhost "); len(got) != 1 {
		t.Errorf("new-site execs = %q, want one for shop.localhost", got)
	}
	if _, ok := c.docker.File(frappeContainer, frappe.SiteInstalled("shop.localhost")); !ok {
		t.Error("shop.localhost is not marked installed")
	}
	if got := readFile(t, filepath.Join(dir, "tamp.toml")); !strings.Contains(got, `host = "shop.localhost"`) {
		t.Errorf("tamp.toml = %q, want it to list shop.localhost again", got)
	}
	c.run(t, "start").assertCode(t, exitcode.CodeOK)
}

func TestWithEThePartlyCreatedSiteFixCarriesE(t *testing.T) {
	c := sandbox(t)
	c.docker.ExecFails = map[string]error{"new-site": exitcode.New(exitcode.CodeFailed,
		"bench new-site shop.localhost exited 1 in erp-dev-frappe-1", "fix what the output above names")}
	c.run(t, "new", "erp-dev", "--frappe", "version-15", "--site", "shop.localhost").assertCode(t, exitcode.CodeFailed)
	c.docker.ExecFails = nil

	r := c.run(t, "start", "-e", "erp-dev", "--quiet")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertOneLineError(t, "run `tamp site remove shop.localhost --yes -e erp-dev`, then `tamp site add shop.localhost -e erp-dev`")
}

func TestStartWarnsAboutASiteTheRecipeDoesNotListAndDeletesNothing(t *testing.T) {
	c := sandbox(t)
	dir := c.newIn(t, "erp-dev", "--site", "shop.localhost", "--site", "blog.localhost")
	writeRecipe(t, dir, "frappe = \"version-15\"\n\n[[sites]]\n  host = \"blog.localhost\"\n")
	execs := len(c.docker.Execs)

	r := c.run(t, "start", "--quiet")

	r.assertCode(t, exitcode.CodeOK)
	want := "warning: the bench holds the site shop.localhost, which the recipe does not list: " +
		"add it with `tamp site add shop.localhost`, or drop it with `tamp site remove shop.localhost --yes`\n"
	if r.stderr != want {
		t.Errorf("stderr = %q, want %q", r.stderr, want)
	}
	if got := execsStartingWith(c.docker.ExecLines()[execs:], "bench drop-site"); len(got) > 0 {
		t.Errorf("start ran %q, want nothing dropped", got)
	}
	if _, ok := c.docker.File(frappeContainer, frappe.SiteConfig("shop.localhost")); !ok {
		t.Error("start deleted shop.localhost")
	}
}

func TestAReloadRightAfterTheRouterIsRecreatedIsRetriedUntilCaddyAnswers(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev", "--site", "shop.localhost")
	file := filepath.Join(router.Dir(c.home), "compose.yaml")
	if err := os.WriteFile(file, []byte("# an older template\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.docker.ExecFailsFirst = map[string]int{"caddy reload": 2}
	execs := len(c.docker.Execs)

	r := c.run(t, "start")

	r.assertCode(t, exitcode.CodeOK)
	if got := routerExecs(c.docker.Execs[execs:]); !slices.Equal(got, []string{reload, reload, reload}) {
		t.Errorf("router execs = %q, want the reload tried three times", got)
	}
}

func TestAReloadOfARouterThatWasNotRecreatedIsNotRetried(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev", "--site", "shop.localhost")
	c.docker.ExecFailsFirst = map[string]int{"caddy reload": 1}
	execs := len(c.docker.Execs)

	r := c.run(t, "start")

	r.assertCode(t, exitcode.CodeFailed)
	if got := routerExecs(c.docker.Execs[execs:]); !slices.Equal(got, []string{reload}) {
		t.Errorf("router execs = %q, want one reload", got)
	}
}

func TestSiteRemoveOfANameThatCouldLeaveTheSitesFolderExits2BeforeAnyDockerCall(t *testing.T) {
	for _, host := range []string{"../x", "..", "a/b", `a\b`, "-x"} {
		t.Run(host, func(t *testing.T) {
			c := sandbox(t)
			c.newIn(t, "erp-dev", "--site", "shop.localhost")
			calls := len(c.docker.Calls)

			// After --, a leading - reaches tamp rather than the flag parser.
			r := c.run(t, "site", "remove", "--yes", "--", host)

			r.assertCode(t, exitcode.CodeUsage)
			r.assertOneLineError(t, "is not a site name")
			if got := c.callsSince(calls); len(got) > 0 {
				t.Errorf("Docker calls = %v, want none", got)
			}
		})
	}
}

func TestSiteRemoveOfANonLocalhostNameTheBenchDoesNotHoldExits2(t *testing.T) {
	for _, host := range []string{"shop.example.com", "mysite"} {
		t.Run(host, func(t *testing.T) {
			c := sandbox(t)
			c.newIn(t, "erp-dev", "--site", "shop.localhost")
			calls := len(c.docker.Calls)

			r := c.run(t, "site", "remove", host, "--yes")

			r.assertCode(t, exitcode.CodeUsage)
			r.assertOneLineError(t, host, "`.localhost`")
			assertOnlyReads(t, c.callsSince(calls))
		})
	}
}

// bench new-site takes any name, so the fix start prints must work for one
// that is not *.localhost.
func TestTheUnlistedSiteFixDropsAHandMadeSiteWithAnyName(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev")
	c.docker.SetFile(frappeContainer, frappe.SiteConfig("mysite"), `{"db_name": "_mysite"}`)
	warned := c.run(t, "start", "--quiet")
	warned.assertCode(t, exitcode.CodeOK)
	if !strings.Contains(warned.stderr, "drop it with `tamp site remove mysite --yes`") {
		t.Fatalf("stderr = %q, want the fix `tamp site remove mysite --yes`", warned.stderr)
	}
	execs := len(c.docker.Execs)

	r := c.run(t, "site", "remove", "mysite", "--yes")

	r.assertCode(t, exitcode.CodeOK)
	want := "bench drop-site mysite --db-root-password password --no-backup --force"
	if got := execsStartingWith(c.docker.ExecLines()[execs:], "bench "); !slices.Equal(got, []string{want}) {
		t.Errorf("bench execs = %q, want only %q", got, want)
	}
	if _, ok := c.docker.File(frappeContainer, frappe.SiteConfig("mysite")); ok {
		t.Error("mysite is still in the bench")
	}
}

// inheritOldVolumes builds a/erp-dev with shop.localhost, then deletes its
// directory, so a new b/erp-dev looks like the same environment moved.
func (c *cli) inheritOldVolumes(t *testing.T) {
	t.Helper()
	c.run(t, "new", filepath.Join("a", "erp-dev"), "--frappe", "version-15", "--site", "shop.localhost").assertCode(t, exitcode.CodeOK)
	if err := os.RemoveAll(filepath.Join(c.dir, "a", "erp-dev")); err != nil {
		t.Fatal(err)
	}
}

func TestDuringNewTheUnlistedSiteWarningNamesTheDirectory(t *testing.T) {
	c := sandbox(t)
	c.inheritOldVolumes(t)
	where := filepath.Join("b", "erp-dev")

	r := c.run(t, "new", where, "--frappe", "version-15", "--quiet")

	r.assertCode(t, exitcode.CodeOK)
	if want := "drop it with `tamp site remove shop.localhost --yes` in " + where + "\n"; !strings.HasSuffix(r.stderr, want) {
		t.Errorf("stderr = %q, want it to end in %q", r.stderr, want)
	}
}

func TestDuringNewThePartlyCreatedSiteFixNamesTheDirectory(t *testing.T) {
	c := sandbox(t)
	c.docker.ExecFails = map[string]error{"new-site": exitcode.New(exitcode.CodeFailed,
		"bench new-site shop.localhost exited 1 in erp-dev-frappe-1", "fix what the output above names")}
	c.run(t, "new", filepath.Join("a", "erp-dev"), "--frappe", "version-15", "--site", "shop.localhost").assertCode(t, exitcode.CodeFailed)
	c.docker.ExecFails = nil
	if err := os.RemoveAll(filepath.Join(c.dir, "a", "erp-dev")); err != nil {
		t.Fatal(err)
	}
	where := filepath.Join("b", "erp-dev")

	r := c.run(t, "new", where, "--frappe", "version-15", "--site", "shop.localhost", "--quiet")

	r.assertCode(t, exitcode.CodeFailed)
	if want := "run `tamp site remove shop.localhost --yes` in " + where + ", then `tamp site add shop.localhost` in " + where; !strings.HasSuffix(lastLine(r.stderr), want) {
		t.Errorf("stderr = %q, want its last line to end in %q", r.stderr, want)
	}
}

func TestSiteAddWithALabelOver63CharactersExits2WithTheLimitInTheFix(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev")
	host := strings.Repeat("a", 64) + ".localhost"

	r := c.run(t, "site", "add", host)

	r.assertCode(t, exitcode.CodeUsage)
	r.assertOneLineError(t, host, "63 characters")
	c.run(t, "site", "add", strings.Repeat("a", 63)+".localhost").assertCode(t, exitcode.CodeOK)
}

func TestSiteAddAndAppAddEndWithALineThatNamesWhatTheyAdded(t *testing.T) {
	c := sandbox(t)
	c.newEnvironment(t, "--frappe", "version-15", "--site", "shop.localhost")

	site := c.run(t, "site", "add", "blog.localhost")
	app := c.run(t, "app", "add", acmeURL)

	site.assertCode(t, exitcode.CodeOK)
	app.assertCode(t, exitcode.CodeOK)
	if got := lastLine(site.stdout); got != "✓ created the site blog.localhost in erp-dev, with every app, and routed it" {
		t.Errorf("site add ends with %q", got)
	}
	if got := lastLine(app.stdout); got != "✓ installed the app acme on every site in erp-dev" {
		t.Errorf("app add ends with %q", got)
	}
}
