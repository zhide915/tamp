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

// A site is tamp's whole promise made visible: a hostname a browser opens.
// These tests are about the three things that decide it — the bench command
// tamp ran, the routes it assembled afterwards, and what it refuses to
// accept in the first place.

// siteNew makes a site and fails the test if it did not work, so that tests
// about listing and removal are not also tests about creation.
func (c *cli) siteNew(t *testing.T, args ...string) {
	t.Helper()
	c.run(t, append([]string{"site", "new"}, args...)...).assertCode(t, exitcode.CodeOK)
}

// benchRan is the command tamp sent to the bench containing fragment.
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

// registered is the site list tamp recorded for an environment. It is what
// the router's routes are assembled from, so it is what has to be right for a
// stopped environment to keep its sites.
func (c *cli) registered(t *testing.T, name string) []string {
	t.Helper()
	reg, err := env.LoadRegistry(filepath.Join(c.home, env.HomeDirName))
	if err != nil {
		t.Fatalf("cannot read the registry: %v", err)
	}
	return reg[name].Sites
}

// --- creating a site -------------------------------------------------------

// The point of the whole command: after it, a browser pointed at the hostname
// reaches this bench, and the desk's live updates reach it too.
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
	// socket.io is a second server on a second port; without its own route the
	// desk stops updating in real time.
	if !strings.Contains(caddyfile, "/socket.io/*") {
		t.Errorf("shop.localhost has no socket.io route:\n%s", caddyfile)
	}
	if got := c.registered(t, "demo"); !slices.Equal(got, []string{"shop.localhost"}) {
		t.Errorf("tamp recorded %v as demo's sites, want [shop.localhost]", got)
	}
}

// The three flags that make a site work in a container: the login scope,
// without which the new site cannot reach the database it was just given, and
// the two passwords, without which bench stops to prompt a user who may be an
// agent.
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

	// The environment's own stored credential, not something tamp asked for:
	// there is no user-facing database password flag anywhere on this command.
	password := c.read(t, "demo", env.StateDirName, env.SecretsDirName, env.DBRootPasswordFile)
	if !slices.Contains(newSite.Cmd, strings.TrimSpace(password)) {
		t.Errorf("tamp did not pass demo's own database credential:\n%s", newSite.Line())
	}
}

// Frappe resolves the site from the Host header, which only works while no
// default site is forced. Writing currentsite.txt would make one bench answer
// for one site whatever hostname was asked for.
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

// Developer mode is what makes Frappe reload changed Python and rebuild
// changed assets — the reason anyone runs a bench locally at all.
func TestSiteNewTurnsOnDeveloperModeForTheSite(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")

	c.siteNew(t, "demo", "shop.localhost")

	setConfig := c.benchRan(t, "set-config")
	if !slices.Contains(setConfig.Cmd, "developer_mode") {
		t.Errorf("tamp never set developer_mode on the site:\n%s", setConfig.Line())
	}
	// -p parses the value, so the key lands as the number 1 rather than the
	// string "1" — the shape the bench-wide config already has.
	if !strings.Contains(setConfig.Line(), "set-config -p") {
		t.Errorf("developer_mode was set as a string:\n%s", setConfig.Line())
	}
}

var adminPasswordLine = regexp.MustCompile(`Administrator password: (\S+)`)

// A generated credential the user never sees again is a site they cannot log
// into, and one printed twice is one more place it outlives the terminal.
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
	// It is not written down anywhere: what tamp generates and prints once is
	// the user's to keep, and a file tamp never reads back is a file that
	// only ever leaks.
	if c.exists("demo", env.StateDirName, env.SecretsDirName, "admin_password") {
		t.Error("tamp stored the Administrator password")
	}
}

// After `bench new-site` succeeds the site and its database exist, whatever
// happens next. A failure past that point must not take with it the one thing
// only this run knows — the generated password — and must still route the
// site, or the claimed hostname points at nothing until an unrelated command
// happens to reassemble the routes.
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
	// A password the user chose is not news, and printing it back only puts it
	// in one more place.
	if strings.Contains(r.stdout+r.stderr, "hunter2") {
		t.Errorf("tamp echoed back the password it was given:\n%s%s", r.stdout, r.stderr)
	}
}

// The router matches one Host header against every environment's routes at
// once, so a hostname is a machine-wide claim rather than a per-bench one.
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

// An environment's mail UI is as much a claim on a hostname as a site is, and
// a Caddyfile holding one address twice is one the router will not load — which
// would take every other site on the machine down with it.
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

// The same rule from the other side: a new environment's name decides a
// hostname too.
func TestCreateRefusesANameWhoseMailHostnameIsAlreadyASite(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.siteNew(t, "demo", "mail.next.localhost")

	r := c.run(t, "create", "next", "--frappe", "version-15")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, "mail.next.localhost", "demo")
}

// Creating a site is minutes of work inside a container, and tamp never
// starts an environment on the user's behalf — that would hide the wait inside
// a command that promised something else.
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

// *.localhost resolves everywhere with no configuration; anything else
// resolves to whatever the internet says, which is not this machine. Until
// tamp manages the hosts file itself, the user needs the exact line.
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

// One bench, two sites, two databases — and stopping short of that would make
// tamp one environment per project rather than one environment per stack.
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

// The environment tamp acts on is the one the user is standing in, the same
// way it is for every other command.
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
	// Every Frappe site has frappe installed, so the column is never empty on
	// a bench tamp can reach.
	r.assertStdoutContains(t, "frappe")
}

func TestSiteListOnAnEnvironmentWithNoSites(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")

	r := c.run(t, "site", "list", "demo")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "no sites yet", "tamp site new demo")
}

// The registry caches the site list precisely so that a stopped environment
// still knows what it has — the same cache the router's routes are assembled
// from.
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

// A site made behind tamp's back — through 'tamp exec -- bench new-site' —
// is still a site on this bench, and the bench is the authority whenever it is
// running.
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

// tamp did not create every site it finds, and a bench can hold a hostname
// the machine has already given to something else. Adopting that one would put
// the address in the assembled Caddyfile twice, which is a router that will not
// reload, so the site is reported and left unrouted.
func TestSiteListRefusesToAdoptAHostnameAnotherEnvironmentOwns(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.create(t, "other")
	c.siteNew(t, "other", "shop.localhost")
	// The same hostname turns up on demo's bench, behind tamp's back.
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

// One site is one database, and this is where that has to be true: dropping
// one must leave every other site on the bench working.
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

// The exit-5 contract: what --yes would destroy is on screen before the user
// retypes the command, and nothing has happened yet.
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

// The bench is the authority whenever it is up, so a site tamp did not create
// is still one tamp can drop by name.
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

// A hostname tamp gave up is a hostname another environment may take.
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
