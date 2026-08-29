package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/frappe"
)

// A seed is caching, not protection: it must be filled by an ordinary site
// creation, restore only onto the version and app set it came from, and cost
// nothing but time when it is gone.

// seededSite is an environment whose first site filled the seed store.
func (c *cli) seededSite(t *testing.T, name, host string, apps ...string) {
	t.Helper()
	c.create(t, name)
	for _, app := range apps {
		c.engine.AddApp(c.container(t, name, "frappe"), app)
	}
	c.siteNew(t, name, host, "--apps", strings.Join(apps, ","))
}

// storedSeeds names the seeds in the machine's store, sorted.
func (c *cli) storedSeeds() []string {
	var keys []string
	for path := range c.engine.Files {
		if after, ok := strings.CutPrefix(path, frappe.SeedDir+"/"); ok {
			if key, found := strings.CutSuffix(after, ".tar.gz"); found {
				keys = append(keys, key)
			}
		}
	}
	slices.Sort(keys)
	return keys
}

// seedManifest is what tamp recorded beside the one stored seed.
func (c *cli) seedManifest(t *testing.T) map[string]any {
	t.Helper()
	keys := c.storedSeeds()
	if len(keys) != 1 {
		t.Fatalf("the store holds %d seeds, want 1: %v", len(keys), keys)
	}
	body, ok := c.engine.Wrote(frappe.SeedManifestPath(keys[0]))
	if !ok {
		t.Fatalf("the %s seed has no manifest beside it", keys[0])
	}
	manifest := map[string]any{}
	if err := json.Unmarshal([]byte(body), &manifest); err != nil {
		t.Fatalf("the %s manifest is not valid JSON: %v\n%s", keys[0], err, body)
	}
	return manifest
}

// wipeSeeds empties the store, which is always allowed to happen.
func (c *cli) wipeSeeds() {
	for path := range c.engine.Files {
		if strings.HasPrefix(path, frappe.SeedDir+"/") {
			delete(c.engine.Files, path)
		}
	}
}

// --- filling the store ------------------------------------------------------

func TestTheFirstSiteOfAnAppSetCachesItsBackupAsASeed(t *testing.T) {
	c := sandbox(t)

	c.seededSite(t, "demo", "shop.localhost", "erpnext")

	if !c.engine.Ran("backup --with-files") {
		t.Error("tamp did not back the new site up, so it cached nothing")
	}
	manifest := c.seedManifest(t)
	if manifest["frappe"] != "version-15" {
		t.Errorf("seed manifest frappe = %v, want version-15", manifest["frappe"])
	}
	apps, _ := json.Marshal(manifest["apps"])
	if string(apps) != `["erpnext"]` {
		t.Errorf("seed manifest apps = %s, want the app set the site was created with", apps)
	}
}

// Docker creates a newly mounted volume root-owned, and only a create runs
// the provisioning that chowns the rest — so an environment made before this
// store existed could never write to it.
func TestTheSeedStoreIsHandedToTheBenchUserBeforeASave(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.engine.AddApp(c.container(t, "demo", "frappe"), "erpnext")

	// Marked past the create's own provisioning: the repair that matters is
	// the one the save itself makes, on an environment that predates the store.
	mark := c.mark()
	c.siteNew(t, "demo", "shop.localhost", "--apps", "erpnext")

	at := execIndex(c.engine.Execs[mark:], "chown")
	if at < 0 {
		t.Fatal("tamp saved a seed without first making the store writable by the bench user")
	}
	prepare := c.engine.Execs[mark+at]
	if prepare.User != "root" {
		t.Errorf("tamp prepared the seed store as %q, and only root may chown it", prepare.User)
	}
	if !strings.Contains(prepare.Line(), frappe.SeedDir) {
		t.Errorf("the chown was not aimed at the seed store: %s", prepare.Line())
	}
}

// The second site of an app set has nothing to add to the store.
func TestASecondSiteOfTheSameAppSetIsNotBackedUpAgain(t *testing.T) {
	c := sandbox(t)
	c.seededSite(t, "demo", "shop.localhost", "erpnext")

	mark := c.mark()
	c.siteNew(t, "demo", "second.localhost", "--apps", "erpnext")

	if c.ranSince(mark, "backup --with-files") {
		t.Error("tamp backed a second site up over a seed it already had")
	}
}

// --- restoring from the store -----------------------------------------------

func TestSeedRestoresTheCachedBackupInsteadOfInstallingTheApps(t *testing.T) {
	c := sandbox(t)
	c.seededSite(t, "demo", "shop.localhost", "erpnext")

	mark := c.mark()
	r := c.run(t, "site", "new", "demo", "second.localhost", "--apps", "erpnext", "--seed")

	r.assertCode(t, exitcode.CodeOK)
	if c.ranSince(mark, "install-app") {
		t.Error("tamp installed the apps despite restoring a seed")
	}
	if !c.ranSince(mark, `"$1" restore`) {
		t.Error("tamp did not restore the seed")
	}
	// A seed is a backup of an older schema by the time it is used, so the
	// migrate is what makes it usable at all.
	if !c.ranSince(mark, "bench --site \"$1\" migrate") {
		t.Error("tamp did not migrate the restored site")
	}
	if apps := c.engine.SiteApps("second.localhost"); !slices.Contains(apps, "erpnext") {
		t.Errorf("second.localhost has %v installed, want the seed's erpnext", apps)
	}
}

// The seed carries the Administrator password of the site it was taken from,
// which is not the one this creation was asked for.
func TestASeededSiteGetsTheAdministratorPasswordThisCreationAskedFor(t *testing.T) {
	c := sandbox(t)
	c.seededSite(t, "demo", "shop.localhost", "erpnext")

	c.siteNew(t, "demo", "second.localhost", "--apps", "erpnext", "--seed",
		"--admin-password", "chosen-here")

	reset := c.benchRan(t, "set-admin-password")
	if !slices.Contains(reset.Cmd, "chosen-here") {
		t.Errorf("tamp reset the password to %v, want the one it was given", reset.Cmd)
	}
}

// --- refusing ---------------------------------------------------------------

func TestSeedWithNoMatchingSeedRefusesAndCreatesNothing(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.engine.AddApp(c.container(t, "demo", "frappe"), "erpnext")

	mark := c.mark()
	r := c.run(t, "site", "new", "demo", "shop.localhost", "--apps", "erpnext", "--seed")

	r.assertCode(t, exitcode.CodeNotFound)
	// The error has to name the combination, or it cannot be acted on.
	r.assertStderrContains(t, "version-15", "erpnext", "without --seed")
	if c.ranSince(mark, "bench new-site") {
		t.Error("tamp created a site with no seed to restore into it")
	}
	if got := c.registered(t, "demo"); len(got) != 0 {
		t.Errorf("demo has sites %v registered, want none", got)
	}
}

// A seed stands in for the app installs, so an app set of none has nothing
// to save — and the refusal has to say that rather than blame the cache.
func TestSeedWithoutAppsRefusesAndNamesWhy(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")

	r := c.run(t, "site", "new", "demo", "shop.localhost", "--seed")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, "--apps")
}

func TestASiteWithNoAppsIsNeverCached(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")

	c.siteNew(t, "demo", "shop.localhost")

	if c.engine.Ran("backup --with-files") {
		t.Error("tamp backed up a site with no apps, which no seed could save time on")
	}
	if got := c.storedSeeds(); len(got) != 0 {
		t.Errorf("the store holds %v, want nothing for an empty app set", got)
	}
}

// A container tamp cannot reach is not an empty cache: saying "no seed here"
// would be a sentence about the store that tamp never checked.
func TestAFailedSeedProbeIsNotReportedAsAMissingSeed(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.engine.AddApp(c.container(t, "demo", "frappe"), "erpnext")
	c.engine.ExecFails = map[string]error{
		frappe.SeedDir: exitcode.New(exitcode.CodeEngineUnavailable,
			"the container is gone", "start Docker Desktop"),
	}

	r := c.run(t, "site", "new", "demo", "shop.localhost", "--apps", "erpnext", "--seed")

	r.assertCode(t, exitcode.CodeEngineUnavailable)
	if strings.Contains(r.stderr, "has no") {
		t.Errorf("tamp blamed the seed store for a container it could not reach:\n%s", r.stderr)
	}
}

func TestASeedNeverCrossesAppSets(t *testing.T) {
	c := sandbox(t)
	c.seededSite(t, "demo", "shop.localhost", "erpnext")
	c.engine.AddApp(c.container(t, "demo", "frappe"), "hrms")

	r := c.run(t, "site", "new", "demo", "second.localhost", "--apps", "erpnext,hrms", "--seed")

	r.assertCode(t, exitcode.CodeNotFound)
}

// A same-named app from another repository or branch is another app; the
// seed of one must never restore a site of the other.
func TestASeedNeverCrossesAppSources(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo", "--apps", "https://github.com/one/custom")
	c.siteNew(t, "demo", "shop.localhost", "--apps", "custom")
	c.create(t, "other", "--apps", "https://github.com/two/custom")

	r := c.run(t, "site", "new", "other", "second.localhost", "--apps", "custom", "--seed")

	r.assertCode(t, exitcode.CodeNotFound)
}

func TestASeedNeverCrossesFrappeVersions(t *testing.T) {
	c := sandbox(t)
	c.seededSite(t, "demo", "shop.localhost", "erpnext")
	c.create(t, "next", "--frappe", "version-16")
	c.engine.AddApp(c.container(t, "next", "frappe"), "erpnext")

	r := c.run(t, "site", "new", "next", "other.localhost", "--apps", "erpnext", "--seed")

	r.assertCode(t, exitcode.CodeNotFound)
	r.assertStderrContains(t, "version-16")
}

// --- losing the store -------------------------------------------------------

func TestWipingTheSeedStoreOnlyCostsTheNextSiteItsInstall(t *testing.T) {
	c := sandbox(t)
	c.seededSite(t, "demo", "shop.localhost", "erpnext")
	c.wipeSeeds()

	mark := c.mark()
	c.siteNew(t, "demo", "second.localhost", "--apps", "erpnext")

	if !c.ranSince(mark, "install-app") {
		t.Error("tamp skipped the install with no seed left to restore")
	}
	if len(c.storedSeeds()) != 1 {
		t.Errorf("the store holds %v, want the seed put back", c.storedSeeds())
	}
}

// A seed tamp cannot vouch for is an empty store, not a broken one.
func TestASeedWithAnUnreadableManifestIsTakenAgain(t *testing.T) {
	c := sandbox(t)
	c.seededSite(t, "demo", "shop.localhost", "erpnext")
	key := c.storedSeeds()[0]
	c.engine.Files[frappe.SeedManifestPath(key)] = "{{{ not json"

	mark := c.mark()
	c.siteNew(t, "demo", "second.localhost", "--apps", "erpnext")

	if !c.ranSince(mark, "backup --with-files") {
		t.Error("tamp left a seed it cannot read in the store")
	}
	if _, ok := c.seedManifest(t)["created"]; !ok {
		t.Error("the manifest tamp wrote back does not record when it was taken")
	}
}
