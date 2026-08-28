package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/exitcode"
)

// An app reaches a site by two deliberate steps: fetched onto the bench at
// create, installed onto a site at 'site new'. What these tests hold in place
// is the seam between them — tamp never fetches an app a site asks for, and
// never guesses the branch of one it does fetch.

func TestCreateFetchesEachAppOntoTheBenchAtTheBranchAsked(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "create", "demo", "--frappe", "version-15", "--apps", "erpnext:version-15,hrms:version-15")

	r.assertCode(t, exitcode.CodeOK)
	fetched := c.benchRan(t, "bench get-app")
	if !slices.Contains(fetched.Cmd, "version-15") {
		t.Errorf("tamp fetched erpnext without the branch asked for: %v", fetched.Cmd)
	}
	// frappe comes from bench init; the other two are what tamp fetched.
	if got := c.engine.Apps(); !slices.Equal(got, []string{"erpnext", "frappe", "hrms"}) {
		t.Errorf("bench apps = %v, want [erpnext frappe hrms]", got)
	}

	// The environment records where each app came from, so a later tamp can
	// say what this bench is made of without asking the bench.
	config := c.read(t, "demo", "tamp.toml")
	for _, want := range []string{`name = "erpnext"`, `source = "https://github.com/frappe/erpnext"`, `branch = "version-15"`} {
		if !strings.Contains(config, want) {
			t.Errorf("tamp.toml does not record %s:\n%s", want, config)
		}
	}
}

// The one predictable way an unpinned app goes wrong: most Frappe apps default
// to develop, which does not run on a version-15 bench. tamp fetches it
// anyway — that is the rule — and says what it just did.
func TestCreateWarnsThatAnUnpinnedAppTakesTheRepositoryDefaultBranch(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "create", "demo", "--frappe", "version-15", "--apps", "erpnext")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStderrContains(t, "default branch of erpnext", "erpnext:version-15")
	if c.benchRan(t, "bench get-app").Line() == "" {
		t.Error("tamp did not fetch the app it warned about")
	}
}

// The pin hint for a URL-sourced app has to repeat the URL: following a
// bare-name hint would fetch from the frappe organisation, not the user's
// repository.
func TestThePinHintForAURLAppRepeatsTheURL(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "create", "demo", "--frappe", "version-15",
		"--apps", "https://github.com/myorg/custom_app")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStderrContains(t, "https://github.com/myorg/custom_app:version-15")
}

// A repository's name and the app it declares can differ — frappe/health
// clones as healthcare. The bench is the authority, and tamp.toml has to
// record the name the bench will answer with, or re-adoption re-fetches an
// app that is already there and fails on it.
func TestCreateRecordsTheNameTheAppDeclaresNotTheRepositorys(t *testing.T) {
	c := sandbox(t)
	c.engine.AppAliases = map[string]string{"health": "healthcare"}

	r := c.run(t, "create", "demo", "--frappe", "version-15",
		"--apps", "https://github.com/frappe/health:version-15")

	r.assertCode(t, exitcode.CodeOK)
	config := c.read(t, "demo", "tamp.toml")
	if !strings.Contains(config, `name = "healthcare"`) {
		t.Errorf("tamp.toml records the repository's name, not the app's:\n%s", config)
	}
}

func TestSiteNewInstallsTheAppsTheBenchAlreadyHas(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo", "--apps", "erpnext:version-15")

	r := c.run(t, "site", "new", "demo", "shop.localhost", "--apps", "erpnext")

	r.assertCode(t, exitcode.CodeOK)
	if got := c.engine.SiteApps("shop.localhost"); !slices.Equal(got, []string{"erpnext"}) {
		t.Errorf("shop.localhost has %v installed, want [erpnext]", got)
	}
	c.run(t, "site", "list", "demo").assertStdoutContains(t, "erpnext")
}

// tamp cannot know which branch of a missing app this bench wants, so it
// refuses — and refuses before the site exists, rather than leaving a site
// with some of the apps that were asked for.
func TestSiteNewRefusesAnAppTheBenchDoesNotHaveBeforeAnythingRuns(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")

	r := c.run(t, "site", "new", "demo", "shop.localhost", "--apps", "erpnext")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStdoutContains(t, "erpnext", "bench get-app erpnext --branch")
	if c.engine.Ran("bench new-site") {
		t.Error("tamp created the site before checking its apps")
	}
	if sites := c.registered(t, "demo"); len(sites) != 0 {
		t.Errorf("tamp claimed %v for a site it refused to create", sites)
	}
}

// Installing fetches nothing, so a branch here would be a pin tamp silently
// dropped.
func TestSiteNewRefusesAPinnedAppBecauseInstallingFetchesNothing(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo", "--apps", "erpnext:version-15")

	r := c.run(t, "site", "new", "demo", "shop.localhost", "--apps", "erpnext:version-15")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, "--apps erpnext")
}

// An app fetched behind tamp's back through the exec bridge is as present as
// one tamp fetched itself: the bench is the authority, not tamp.toml.
func TestSiteNewAcceptsAnAppFetchedThroughTheExecBridge(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.engine.AddApp("erpnext")

	r := c.run(t, "site", "new", "demo", "shop.localhost", "--apps", "erpnext")

	r.assertCode(t, exitcode.CodeOK)
	if got := c.engine.SiteApps("shop.localhost"); !slices.Equal(got, []string{"erpnext"}) {
		t.Errorf("shop.localhost has %v installed, want [erpnext]", got)
	}
}
