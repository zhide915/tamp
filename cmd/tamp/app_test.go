package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/exitcode"
)

// Apps move in two explicit steps — fetched onto the bench, installed onto a
// site — and tamp never bridges the two or guesses a branch.

func TestCreateFetchesEachAppOntoTheBenchAtTheBranchAsked(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "create", "demo", "--frappe", "version-15", "--apps", "erpnext:version-15,hrms:version-15")

	r.assertCode(t, exitcode.CodeOK)
	fetched := c.benchRan(t, "bench get-app")
	if !slices.Contains(fetched.Cmd, "version-15") {
		t.Errorf("tamp fetched erpnext without the branch asked for: %v", fetched.Cmd)
	}
	// frappe itself arrives via bench init.
	if got := c.engine.Apps(); !slices.Equal(got, []string{"erpnext", "frappe", "hrms"}) {
		t.Errorf("bench apps = %v, want [erpnext frappe hrms]", got)
	}

	// tamp.toml records provenance so later commands need not ask the bench.
	config := c.read(t, "demo", "tamp.toml")
	for _, want := range []string{`name = "erpnext"`, `source = "https://github.com/frappe/erpnext"`, `branch = "version-15"`} {
		if !strings.Contains(config, want) {
			t.Errorf("tamp.toml does not record %s:\n%s", want, config)
		}
	}
}

// Unpinned apps usually default to develop, which a version-15 bench cannot run.
func TestCreateWarnsThatAnUnpinnedAppTakesTheRepositoryDefaultBranch(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "create", "demo", "--frappe", "version-15", "--apps", "erpnext")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStderrContains(t, "default branch of erpnext", "erpnext:version-15")
	if c.benchRan(t, "bench get-app").Line() == "" {
		t.Error("tamp did not fetch the app it warned about")
	}
}

// A bare-name hint would point at the frappe organisation, not the user's repo.
func TestThePinHintForAURLAppRepeatsTheURL(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "create", "demo", "--frappe", "version-15",
		"--apps", "https://github.com/myorg/custom_app")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStderrContains(t, "https://github.com/myorg/custom_app:version-15")
}

// A repo and its app can be named differently (frappe/health -> healthcare);
// recording the repo's name would make re-adoption re-fetch and fail.
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

// The branch of a missing app is unknowable, so refuse before the site exists.
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

func TestSiteNewRefusesAPinnedAppBecauseInstallingFetchesNothing(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo", "--apps", "erpnext:version-15")

	r := c.run(t, "site", "new", "demo", "shop.localhost", "--apps", "erpnext:version-15")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, "--apps erpnext")
}

// The bench, not tamp.toml, is the authority on what is present.
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
