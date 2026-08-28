package main

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/engine/enginetest"
	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/exitcode"
)

// Three things decide a site: the bench command run, the routes assembled
// afterwards, and what tamp refuses up front.

// siteNew fails the test on error, so callers are not also testing creation.
func (c *cli) siteNew(t *testing.T, args ...string) {
	t.Helper()
	c.run(t, append([]string{"site", "new"}, args...)...).assertCode(t, exitcode.CodeOK)
}

func (c *cli) benchRan(t *testing.T, fragment string) enginetest.Exec {
	t.Helper()
	for _, e := range c.engine.Execs {
		if strings.Contains(e.Line(), fragment) {
			return e
		}
	}
	t.Fatalf("tamp never ran anything containing %q in a container", fragment)
	return enginetest.Exec{}
}

// registered is the recorded site list — what the router's routes are
// assembled from.
func (c *cli) registered(t *testing.T, name string) []string {
	t.Helper()
	reg, err := env.LoadRegistry(filepath.Join(c.home, env.HomeDirName))
	if err != nil {
		t.Fatalf("cannot read the registry: %v", err)
	}
	return reg[name].Sites
}

// --- creating a site -------------------------------------------------------

func TestSiteNewRoutesTheHostnameToItsBench(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")

	r := c.run(t, "site", "new", "demo", "shop.localhost")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "http://shop.localhost")

	caddyfile := c.caddyfile(t)
	if !strings.Contains(caddyfile, "http://shop.localhost {") {
		t.Errorf("shop.localhost is not routed:\n%s", caddyfile)
	}
	// Without its own socket.io route the desk stops updating live.
	if !strings.Contains(caddyfile, "/socket.io/*") {
		t.Errorf("shop.localhost has no socket.io route:\n%s", caddyfile)
	}
	if got := c.registered(t, "demo"); !slices.Equal(got, []string{"shop.localhost"}) {
		t.Errorf("tamp recorded %v as demo's sites, want [shop.localhost]", got)
	}
}

// The login scope lets the site reach its database across containers; the
// passwords keep bench from stopping to prompt.
func TestSiteNewCreatesTheSiteNonInteractivelyInAContainer(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")

	c.siteNew(t, "demo", "shop.localhost")

	newSite := c.benchRan(t, "bench new-site")
	for _, want := range []string{
		"--mariadb-user-host-login-scope=%",
		"--db-root-password",
		"--admin-password",
	} {
		if !strings.Contains(newSite.Line(), want) {
			t.Errorf("bench new-site was run without %s:\n%s", want, newSite.Line())
		}
	}
	if got := newSite.Container; got != c.container(t, "demo", env.FrappeService) {
		t.Errorf("tamp created the site in %s, not on demo's bench", got)
	}

	// The stored credential: no user-facing db password flag exists.
	password := c.read(t, "demo", env.StateDirName, env.SecretsDirName, env.DBRootPasswordFile)
	if !slices.Contains(newSite.Cmd, strings.TrimSpace(password)) {
		t.Errorf("tamp did not pass demo's own database credential:\n%s", newSite.Line())
	}
}

// Host-header resolution only works while no default site is forced;
// currentsite.txt would pin the bench to one site.
func TestSiteNewNeverForcesADefaultSite(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")

	c.siteNew(t, "demo", "shop.localhost")

	for _, path := range c.engine.Written() {
		if strings.Contains(path, "currentsite.txt") {
			t.Errorf("tamp wrote %s, which pins the bench to one site", path)
		}
	}
	if c.engine.Ran("currentsite") || c.engine.Ran("use shop.localhost") {
		t.Error("tamp set a default site on the bench")
	}
}

// Developer mode is what reloads changed Python and rebuilds changed assets.
func TestSiteNewTurnsOnDeveloperModeForTheSite(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")

	c.siteNew(t, "demo", "shop.localhost")

	setConfig := c.benchRan(t, "set-config")
	if !slices.Contains(setConfig.Cmd, "developer_mode") {
		t.Errorf("tamp never set developer_mode on the site:\n%s", setConfig.Line())
	}
	// -p parses the value: the number 1, not the string "1", matching the
	// bench-wide config's shape.
	if !strings.Contains(setConfig.Line(), "set-config -p") {
		t.Errorf("developer_mode was set as a string:\n%s", setConfig.Line())
	}
}

var adminPasswordLine = regexp.MustCompile(`Administrator password: (\S+)`)

// Unseen means a site nobody can log into; printed twice outlives the terminal.
func TestSiteNewPrintsAGeneratedAdminPasswordExactlyOnce(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")

	r := c.run(t, "site", "new", "demo", "shop.localhost")

	r.assertCode(t, exitcode.CodeOK)
	match := adminPasswordLine.FindStringSubmatch(r.stdout)
	if match == nil {
		t.Fatalf("tamp never printed the Administrator password:\n%s", r.stdout)
	}
	password := match[1]

	if got := strings.Count(r.stdout+r.stderr, password); got != 1 {
		t.Errorf("tamp printed the Administrator password %d times, want 1", got)
	}
	if !slices.Contains(c.benchRan(t, "bench new-site").Cmd, password) {
		t.Error("tamp printed one password and gave the site another")
	}
	// Never stored: a file tamp never reads back can only leak.
	if c.exists("demo", env.StateDirName, env.SecretsDirName, "admin_password") {
		t.Error("tamp stored the Administrator password")
	}
}

// Once bench new-site succeeds the site exists regardless; the generated
// password and the route must survive a later failure.
func TestAFailureAfterTheSiteExistsStillPrintsThePasswordAndRoutesIt(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo", "--apps", "erpnext:version-15")
	c.engine.ExecFails = map[string]error{
		"install-app": exitcode.New(exitcode.CodeFailed, "migration failed", ""),
	}

	r := c.run(t, "site", "new", "demo", "shop.localhost", "--apps", "erpnext")

	r.assertCode(t, exitcode.CodeFailed)
	if adminPasswordLine.FindStringSubmatch(r.stdout) == nil {
		t.Errorf("the generated Administrator password was lost with the failure:\n%s", r.stdout)
	}
	if !strings.Contains(c.caddyfile(t), "http://shop.localhost {") {
		t.Errorf("the site exists but is not routed:\n%s", c.caddyfile(t))
	}
}

func TestSiteNewTakesTheAdminPasswordFromTheFlag(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")

	r := c.run(t, "site", "new", "demo", "shop.localhost", "--admin-password", "hunter2")

	r.assertCode(t, exitcode.CodeOK)
	if !slices.Contains(c.benchRan(t, "bench new-site").Cmd, "hunter2") {
		t.Error("tamp generated a password over the one it was given")
	}
	// Echoing a user-chosen password only spreads it.
	if strings.Contains(r.stdout+r.stderr, "hunter2") {
		t.Errorf("tamp echoed back the password it was given:\n%s%s", r.stdout, r.stderr)
	}
}

// The router matches Host against every environment at once, so a hostname
// is a machine-wide claim.
func TestSiteNewRefusesAHostnameAnotherEnvironmentAlreadyHas(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.create(t, "other")
	c.siteNew(t, "demo", "shop.localhost")

	r := c.run(t, "site", "new", "other", "shop.localhost")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, "shop.localhost", "demo")
	if got := c.registered(t, "other"); len(got) != 0 {
		t.Errorf("the refused hostname was recorded against other anyway: %v", got)
	}
}

// A duplicate address makes a Caddyfile the router will not load, taking
// every site on the machine down.
func TestSiteNewRefusesAnEnvironmentsMailHostname(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")

	r := c.run(t, "site", "new", "demo", "mail.demo.localhost")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, "mail.demo.localhost", "mail UI")
	if c.engine.Ran("bench new-site") {
		t.Error("tamp created the site it had just refused")
	}
}

// The same claim from the other side: a new name decides a hostname too.
func TestCreateRefusesANameWhoseMailHostnameIsAlreadyASite(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.siteNew(t, "demo", "mail.next.localhost")

	r := c.run(t, "create", "next", "--frappe", "version-15")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, "mail.next.localhost", "demo")
}

// No auto-start: minutes of hidden work inside a command that promised
// something else.
func TestSiteNewNeedsARunningEnvironment(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.run(t, "stop", "demo").assertCode(t, exitcode.CodeOK)
	before := len(c.engine.Execs)

	r := c.run(t, "site", "new", "demo", "shop.localhost")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, "not running", "tamp start demo")
	if len(c.engine.Execs) != before {
		t.Error("tamp ran something on a bench that is not up")
	}
	if got := c.registered(t, "demo"); len(got) != 0 {
		t.Errorf("the site tamp refused to create was recorded anyway: %v", got)
	}
}

// Only *.localhost resolves without configuration; other names need the
// exact hosts-file line.
func TestSiteNewSaysWhatANonLocalhostNameStillNeeds(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")

	r := c.run(t, "site", "new", "demo", "shop.example.com")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStderrContains(t, "shop.example.com", "resolve")
	r.assertStdoutContains(t, "127.0.0.1  shop.example.com")
}

func TestSiteNewSaysNothingAboutHostsFilesForALocalhostName(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")

	r := c.run(t, "site", "new", "demo", "shop.localhost")

	if strings.Contains(r.stdout+r.stderr, "127.0.0.1") {
		t.Errorf("tamp asked for a hosts entry a browser does not need:\n%s%s", r.stdout, r.stderr)
	}
}

func TestASecondSiteOnOneBenchGetsItsOwnRoute(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")

	c.siteNew(t, "demo", "shop.localhost")
	c.siteNew(t, "demo", "books.localhost")

	for _, host := range []string{"shop.localhost", "books.localhost"} {
		if !strings.Contains(c.caddyfile(t), "http://"+host+" {") {
			t.Errorf("%s is not routed:\n%s", host, c.caddyfile(t))
		}
	}
	if got := c.registered(t, "demo"); !slices.Equal(got, []string{"books.localhost", "shop.localhost"}) {
		t.Errorf("tamp recorded %v as demo's sites, want both", got)
	}
}

func TestSiteNewTakesTheEnvironmentFromTheWorkingDirectory(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	t.Chdir(c.path("demo"))

	r := c.run(t, "site", "new", "shop.localhost")

	r.assertCode(t, exitcode.CodeOK)
	if got := c.registered(t, "demo"); !slices.Equal(got, []string{"shop.localhost"}) {
		t.Errorf("tamp recorded %v as demo's sites, want [shop.localhost]", got)
	}
}

// --- listing sites ---------------------------------------------------------

func TestSiteListShowsEveryHostWithItsURL(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.siteNew(t, "demo", "shop.localhost")
	c.siteNew(t, "demo", "books.localhost")

	r := c.run(t, "site", "list", "demo")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "HOST", "URL", "APPS",
		"shop.localhost", "http://shop.localhost",
		"books.localhost", "http://books.localhost")
	// frappe is on every site, so the column is never empty on a reachable bench.
	r.assertStdoutContains(t, "frappe")
}

func TestSiteListOnAnEnvironmentWithNoSites(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")

	r := c.run(t, "site", "list", "demo")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "no sites yet", "tamp site new demo")
}

// The cached list is the same one routes are assembled from.
func TestSiteListOnAStoppedEnvironmentListsWhatTampLastSaw(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.siteNew(t, "demo", "shop.localhost")
	c.run(t, "stop", "demo").assertCode(t, exitcode.CodeOK)

	r := c.run(t, "site", "list", "demo")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "shop.localhost")
	r.assertStderrContains(t, "not running")
}

// The running bench is the authority, even for sites made behind tamp's back.
func TestSiteListAdoptsASiteTampDidNotCreate(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.run(t, "exec", "demo", "--", "bench", "new-site", "smuggled.localhost").
		assertCode(t, exitcode.CodeOK)

	r := c.run(t, "site", "list", "demo")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "smuggled.localhost")
	if got := c.registered(t, "demo"); !slices.Equal(got, []string{"smuggled.localhost"}) {
		t.Errorf("tamp recorded %v, so the site it found would never be routed", got)
	}
}

// The refresh on start is what routes a site made through the exec bridge
// without waiting for a site command.
func TestStartRoutesASiteTampDidNotCreate(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.run(t, "exec", "demo", "--", "bench", "new-site", "smuggled.localhost").
		assertCode(t, exitcode.CodeOK)

	c.run(t, "start", "demo").assertCode(t, exitcode.CodeOK)

	if !strings.Contains(c.caddyfile(t), "http://smuggled.localhost {") {
		t.Errorf("the site found on the bench is not routed:\n%s", c.caddyfile(t))
	}
}

// One malformed address in the Caddyfile stops the router loading it, taking
// every site on the machine down with it.
func TestStartDoesNotRouteABenchSiteThatIsNotAHostname(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.run(t, "exec", "demo", "--", "bench", "new-site", "SHOUTY.localhost").
		assertCode(t, exitcode.CodeOK)

	r := c.run(t, "start", "demo")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStderrContains(t, "SHOUTY.localhost", "not routing it")
	if strings.Contains(c.caddyfile(t), "SHOUTY.localhost") {
		t.Errorf("the malformed hostname reached the Caddyfile:\n%s", c.caddyfile(t))
	}
}

// Adopting a hostname another environment owns would duplicate it in the
// Caddyfile, which the router refuses to load; report it, leave it unrouted.
func TestSiteListRefusesToAdoptAHostnameAnotherEnvironmentOwns(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.create(t, "other")
	c.siteNew(t, "other", "shop.localhost")
	// The taken hostname appears on demo's bench behind tamp's back.
	c.run(t, "exec", "demo", "--", "bench", "new-site", "shop.localhost").
		assertCode(t, exitcode.CodeOK)

	r := c.run(t, "site", "list", "demo")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStderrContains(t, "shop.localhost", "other", "not routing it")
	if got := c.registered(t, "demo"); len(got) != 0 {
		t.Errorf("tamp took %v from other by listing demo", got)
	}
	if got := c.registered(t, "other"); !slices.Equal(got, []string{"shop.localhost"}) {
		t.Errorf("other lost its site to demo's bench: %v", got)
	}
	if strings.Count(c.caddyfile(t), "http://shop.localhost {") != 1 {
		t.Errorf("shop.localhost is routed twice, so the router will not reload:\n%s", c.caddyfile(t))
	}
}

// --- removing a site -------------------------------------------------------

func TestSiteRmDropsOnlyThatSite(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.siteNew(t, "demo", "shop.localhost")
	c.siteNew(t, "demo", "books.localhost")

	r := c.run(t, "site", "rm", "demo", "shop.localhost", "--yes")

	r.assertCode(t, exitcode.CodeOK)
	drop := c.benchRan(t, "bench drop-site")
	if !slices.Contains(drop.Cmd, "shop.localhost") {
		t.Errorf("tamp dropped the wrong site:\n%s", drop.Line())
	}
	if got := c.registered(t, "demo"); !slices.Equal(got, []string{"books.localhost"}) {
		t.Errorf("tamp recorded %v as demo's sites, want [books.localhost]", got)
	}

	caddyfile := c.caddyfile(t)
	if strings.Contains(caddyfile, "http://shop.localhost {") {
		t.Errorf("the dropped site is still routed:\n%s", caddyfile)
	}
	if !strings.Contains(caddyfile, "http://books.localhost {") {
		t.Errorf("dropping one site took the other's route with it:\n%s", caddyfile)
	}
}

// Exit 5: show what --yes would destroy, do nothing.
func TestSiteRmWithoutYesDestroysNothing(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.siteNew(t, "demo", "shop.localhost")

	r := c.run(t, "site", "rm", "demo", "shop.localhost")

	r.assertCode(t, exitcode.CodeConfirmationRequired)
	r.assertStdoutContains(t, "would destroy", "shop.localhost")
	r.assertStderrContains(t, "--yes")
	if c.engine.Ran("bench drop-site") {
		t.Error("tamp dropped the site it was asking about")
	}
	if got := c.registered(t, "demo"); !slices.Equal(got, []string{"shop.localhost"}) {
		t.Errorf("tamp recorded %v after destroying nothing", got)
	}
}

func TestSiteRmOnASiteTheEnvironmentDoesNotHave(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")

	r := c.run(t, "site", "rm", "demo", "shop.localhost", "--yes")

	r.assertCode(t, exitcode.CodeNotFound)
	r.assertStderrContains(t, "shop.localhost", "tamp site list demo")
}

func TestSiteRmDropsASiteTampDidNotCreate(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.run(t, "exec", "demo", "--", "bench", "new-site", "smuggled.localhost").
		assertCode(t, exitcode.CodeOK)

	r := c.run(t, "site", "rm", "demo", "smuggled.localhost", "--yes")

	r.assertCode(t, exitcode.CodeOK)
	if !slices.Contains(c.benchRan(t, "bench drop-site").Cmd, "smuggled.localhost") {
		t.Error("tamp did not drop the site it found on the bench")
	}
}

func TestSiteRmNeedsARunningEnvironment(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.siteNew(t, "demo", "shop.localhost")
	c.run(t, "stop", "demo").assertCode(t, exitcode.CodeOK)

	r := c.run(t, "site", "rm", "demo", "shop.localhost", "--yes")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, "not running", "tamp start demo")
	if got := c.registered(t, "demo"); !slices.Equal(got, []string{"shop.localhost"}) {
		t.Errorf("tamp forgot a site it never dropped: %v", got)
	}
}

func TestAHostnameIsFreeAgainOnceItsSiteIsDropped(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.create(t, "other")
	c.siteNew(t, "demo", "shop.localhost")
	c.run(t, "site", "rm", "demo", "shop.localhost", "--yes").assertCode(t, exitcode.CodeOK)

	c.run(t, "site", "new", "other", "shop.localhost").assertCode(t, exitcode.CodeOK)
}

// --- the command surface ---------------------------------------------------

func TestSiteUsageErrors(t *testing.T) {
	cases := map[string][]string{
		"no subcommand at all":      {"site", "bogus"},
		"site new with no host":     {"site", "new"},
		"site rm with no host":      {"site", "rm"},
		"site new with a third arg": {"site", "new", "demo", "shop.localhost", "extra"},
	}
	for why, args := range cases {
		t.Run(why, func(t *testing.T) {
			c := sandbox(t)
			c.run(t, args...).assertCode(t, exitcode.CodeUsage)
		})
	}
}
