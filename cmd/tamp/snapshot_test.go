package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/exitcode"
)

// A snapshot is protection: it has to hold every site with its files, say
// what it holds, and refuse to come back into a bench that cannot carry it.

func (c *cli) snapshotDir(name string) string {
	return c.path(name, env.StateDirName, env.SnapshotsDirName)
}

func (c *cli) snapshotNames(t *testing.T, name string) []string {
	t.Helper()
	entries, err := os.ReadDir(c.snapshotDir(name))
	if err != nil {
		t.Fatalf("cannot read %s: %v", c.snapshotDir(name), err)
	}
	var got []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") {
			got = append(got, strings.TrimSuffix(entry.Name(), ".json"))
		}
	}
	slices.Sort(got)
	return got
}

func (c *cli) manifest(t *testing.T, name, snapshot string) map[string]any {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(c.snapshotDir(name), snapshot+".json"))
	if err != nil {
		t.Fatalf("cannot read the manifest: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("the manifest is not JSON: %v", err)
	}
	return got
}

// rewriteManifest edits a snapshot in place — a snapshot is a user's file, so
// a manifest describing a bench this one is not is a real situation.
func (c *cli) rewriteManifest(t *testing.T, name, snapshot string, change func(map[string]any)) {
	t.Helper()
	got := c.manifest(t, name, snapshot)
	change(got)
	body, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.snapshotDir(name), snapshot+".json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
}

// ranSince reports whether anything after mark ran in a container — what
// pins that a refusal left the bench alone.
func (c *cli) ranSince(mark int, fragment string) bool {
	return execIndex(c.engine.Execs[mark:], fragment) >= 0
}

// withSite is an environment holding one site with one app on it.
func (c *cli) withSite(t *testing.T, name, host string) {
	t.Helper()
	c.create(t, name)
	c.engine.AddApp(c.container(t, name, "frappe"), "erpnext")
	c.siteNew(t, name, host, "--apps", "erpnext", "--admin-password", "secret")
}

// --- creating ---------------------------------------------------------------

func TestSnapshotCreateBundlesEverySiteWithItsFilesAndItsApps(t *testing.T) {
	c := sandbox(t)
	c.withSite(t, "demo", "shop.localhost")

	r := c.run(t, "snapshot", "create", "demo", "--name", "before-migrate")

	r.assertCode(t, exitcode.CodeOK)
	if !c.engine.Ran("backup --with-files") {
		t.Error("the snapshot did not back the site up with its files")
	}
	if _, err := os.Stat(filepath.Join(c.snapshotDir("demo"), "before-migrate.tar.gz")); err != nil {
		t.Errorf("no bundle beside the manifest: %v", err)
	}

	manifest := c.manifest(t, "demo", "before-migrate")
	sites, ok := manifest["sites"].([]any)
	if !ok || len(sites) != 1 {
		t.Fatalf("the manifest does not describe one site: %v", manifest["sites"])
	}
	site := sites[0].(map[string]any)
	if site["host"] != "shop.localhost" {
		t.Errorf("manifest host = %v, want shop.localhost", site["host"])
	}
	// The app list is what a restore's pre-flight is made of.
	apps, _ := json.Marshal(site["apps"])
	if !strings.Contains(string(apps), "erpnext") {
		t.Errorf("the manifest does not record the site's apps: %s", apps)
	}
}

// Bare 'tamp snapshot' is create: the subcommand words are reserved
// environment names, so the argument cannot be anything else.
func TestBareSnapshotTakesOne(t *testing.T) {
	c := sandbox(t)
	c.withSite(t, "demo", "shop.localhost")

	r := c.run(t, "snapshot", "demo")

	r.assertCode(t, exitcode.CodeOK)
	if got := c.snapshotNames(t, "demo"); len(got) != 1 {
		t.Errorf("snapshots on disk = %v, want one", got)
	}
}

// A snapshot protects the data layer, so an environment with nothing in it
// has nothing to protect — and says so rather than writing an empty bundle.
func TestSnapshotOfAnEnvironmentWithNoSitesIsRefused(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")

	r := c.run(t, "snapshot", "create", "demo")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, "nothing in its data layer", "tamp site new demo")
	if _, err := os.Stat(c.snapshotDir("demo")); err == nil {
		t.Error("tamp made a snapshots directory for a snapshot it refused to take")
	}
}

func TestASecondSnapshotOfTheSameNameIsRefused(t *testing.T) {
	c := sandbox(t)
	c.withSite(t, "demo", "shop.localhost")
	c.run(t, "snapshot", "create", "demo", "--name", "nightly").assertCode(t, exitcode.CodeOK)

	r := c.run(t, "snapshot", "create", "demo", "--name", "nightly")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, "already has a snapshot", "nightly")
}

// --- listing ----------------------------------------------------------------

func TestSnapshotListShowsNameCreatedSizeAndSiteCount(t *testing.T) {
	c := sandbox(t)
	c.withSite(t, "demo", "shop.localhost")
	c.run(t, "snapshot", "create", "demo", "--name", "nightly").assertCode(t, exitcode.CodeOK)

	r := c.run(t, "snapshot", "list", "demo")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "NAME", "CREATED", "SIZE", "SITES", "nightly")
}

func TestSnapshotListOnAnEnvironmentWithNoneSaysSoAndOffersTheCommand(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")

	r := c.run(t, "snapshot", "list", "demo")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "no snapshots yet", "tamp snapshot create demo")
}

// --- the cycle the data layer exists for ------------------------------------

// The acceptance the whole feature is for: wipe the data layer, restore, and
// the site is back with its apps and its route.
func TestCleanDataThenRestoreBringsTheSiteBack(t *testing.T) {
	c := sandbox(t)
	c.withSite(t, "demo", "shop.localhost")
	c.run(t, "snapshot", "create", "demo", "--name", "before").assertCode(t, exitcode.CodeOK)
	c.run(t, "clean", "demo", "--data", "--yes").assertCode(t, exitcode.CodeOK)
	if got := c.engine.Sites(); len(got) != 0 {
		t.Fatalf("clean --data left sites behind: %v", got)
	}

	// No --yes: there is no site data left for a restore to write over.
	r := c.run(t, "snapshot", "restore", "demo")

	r.assertCode(t, exitcode.CodeOK)
	if got := c.engine.Sites(); !slices.Equal(got, []string{"shop.localhost"}) {
		t.Errorf("bench sites = %v, want [shop.localhost]", got)
	}
	if got := c.engine.SiteApps("shop.localhost"); !slices.Contains(got, "erpnext") {
		t.Errorf("the restored site lost its apps: %v", got)
	}
	if !c.engine.Ran("bench --site \"$1\" migrate") {
		t.Error("the restore never migrated the site")
	}
	if !strings.Contains(c.caddyfile(t), "http://shop.localhost {") {
		t.Errorf("the restored site is not routed:\n%s", c.caddyfile(t))
	}
}

// Without --name the newest snapshot is the one that comes back.
func TestRestoreDefaultsToTheNewestSnapshot(t *testing.T) {
	c := sandbox(t)
	c.withSite(t, "demo", "shop.localhost")
	c.run(t, "snapshot", "create", "demo", "--name", "older").assertCode(t, exitcode.CodeOK)
	c.run(t, "snapshot", "create", "demo", "--name", "newer").assertCode(t, exitcode.CodeOK)

	r := c.run(t, "snapshot", "restore", "demo", "--yes")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "restored newer")
}

func TestRestoringASnapshotThatIsNotThereIsNotFound(t *testing.T) {
	c := sandbox(t)
	c.withSite(t, "demo", "shop.localhost")

	r := c.run(t, "snapshot", "restore", "demo", "--name", "nope")

	r.assertCode(t, exitcode.CodeNotFound)
	r.assertStderrContains(t, "no snapshot named", "tamp snapshot list demo")
}

// --- the pre-flight ---------------------------------------------------------

// A restore that died half-way is the failure mode snapshots exist to
// prevent, so every refusal happens before the bench is touched.
func TestRestoreOntoABenchMissingAnAppExitsOneAndTouchesNothing(t *testing.T) {
	c := sandbox(t)
	c.withSite(t, "demo", "shop.localhost")
	c.run(t, "snapshot", "create", "demo", "--name", "nightly").assertCode(t, exitcode.CodeOK)
	c.rewriteManifest(t, "demo", "nightly", func(m map[string]any) {
		m["sites"] = []any{map[string]any{
			"host": "shop.localhost",
			"apps": []any{"frappe", "hrms", "payments"},
		}}
	})
	mark := c.mark()

	r := c.run(t, "snapshot", "restore", "demo", "--name", "nightly", "--yes")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStdoutContains(t,
		"hrms",
		"payments",
		"tamp exec demo -- bench get-app hrms --branch <branch>")
	r.assertStderrContains(t, "nothing was restored")
	if c.ranSince(mark, "restore") {
		t.Error("the pre-flight ran a restore anyway")
	}
	if c.ranSince(mark, "-xzf -") {
		t.Error("the pre-flight unpacked the bundle before refusing")
	}
}

// Hostnames are unique across the machine: one address twice in the
// Caddyfile takes every site on the machine down.
func TestRestoreOfAHostnameAnotherEnvironmentNowHasFailsPreFlight(t *testing.T) {
	c := sandbox(t)
	c.withSite(t, "one", "shop.localhost")
	c.create(t, "two")
	c.run(t, "snapshot", "create", "one", "--name", "nightly").assertCode(t, exitcode.CodeOK)

	c.run(t, "site", "rm", "one", "shop.localhost", "--yes").assertCode(t, exitcode.CodeOK)
	c.siteNew(t, "two", "shop.localhost", "--admin-password", "secret")
	mark := c.mark()

	r := c.run(t, "snapshot", "restore", "one", "--name", "nightly")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStdoutContains(t, "shop.localhost", `"two"`)
	r.assertStderrContains(t, "nothing was restored")
	if c.ranSince(mark, "bench new-site") {
		t.Error("the pre-flight recreated a site whose hostname is taken")
	}
}

// --- writing over live data -------------------------------------------------

func TestRestoreOverLiveSiteDataWithoutYesExitsFiveNamingWhatWouldGo(t *testing.T) {
	c := sandbox(t)
	c.withSite(t, "demo", "shop.localhost")
	c.siteNew(t, "demo", "other.localhost", "--admin-password", "secret")
	c.run(t, "snapshot", "create", "demo", "--name", "nightly").assertCode(t, exitcode.CodeOK)
	c.rewriteManifest(t, "demo", "nightly", func(m map[string]any) {
		m["sites"] = []any{map[string]any{"host": "shop.localhost", "apps": []any{"frappe"}}}
	})
	mark := c.mark()

	r := c.run(t, "snapshot", "restore", "demo", "--name", "nightly")

	r.assertCode(t, exitcode.CodeConfirmationRequired)
	r.assertStdoutContains(t,
		"restoring nightly would replace, in demo:",
		"shop.localhost",
		"it would keep:",
		"other.localhost",
		"tamp snapshot restore demo --name nightly --yes")
	if c.ranSince(mark, "-xzf -") {
		t.Error("the preview unpacked the bundle")
	}
}

func TestRestoreOverLiveSiteDataWithYesProceeds(t *testing.T) {
	c := sandbox(t)
	c.withSite(t, "demo", "shop.localhost")
	c.run(t, "snapshot", "create", "demo", "--name", "nightly").assertCode(t, exitcode.CodeOK)
	mark := c.mark()

	r := c.run(t, "snapshot", "restore", "demo", "--name", "nightly", "--yes")

	r.assertCode(t, exitcode.CodeOK)
	if !c.ranSince(mark, `"$1" restore`) {
		t.Error("the restore never ran bench restore")
	}
	// The site was already there, so it must not be created a second time.
	if c.ranSince(mark, "bench new-site") {
		t.Error("the restore recreated a site that was already on the bench")
	}
}

// --- the command surface ----------------------------------------------------

func TestSnapshotNeedsARunningEnvironment(t *testing.T) {
	c := sandbox(t)
	c.withSite(t, "demo", "shop.localhost")
	c.run(t, "stop", "demo").assertCode(t, exitcode.CodeOK)

	r := c.run(t, "snapshot", "create", "demo")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, "demo is not running", "tamp start demo")
}

func TestAnUnusableSnapshotNameIsRefusedBeforeAnythingRuns(t *testing.T) {
	c := sandbox(t)
	c.withSite(t, "demo", "shop.localhost")
	mark := c.mark()

	r := c.run(t, "snapshot", "create", "demo", "--name", "Nightly Backup")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, "not a usable snapshot name")
	if c.ranSince(mark, "backup --with-files") {
		t.Error("tamp backed a site up for a snapshot it could not name")
	}
}

// A restored site outside .localhost is routed but still resolves nowhere,
// which is the same pending state 'site new' reports.
func TestRestoringACustomDomainSaysItsHostsEntryIsPending(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.siteNew(t, "demo", "abc.xyz.com", "--admin-password", "secret")
	c.run(t, "snapshot", "create", "demo", "--name", "nightly").assertCode(t, exitcode.CodeOK)
	c.run(t, "site", "rm", "demo", "abc.xyz.com", "--yes").assertCode(t, exitcode.CodeOK)

	r := c.run(t, "snapshot", "restore", "demo", "--name", "nightly")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "pending", "tamp hosts sync")
}

// The same restore into a hosts file that already names it says nothing.
func TestRestoringACustomDomainThatIsAlreadyInTheBlockSaysNothing(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.siteNew(t, "demo", "abc.xyz.com", "--admin-password", "secret")
	c.run(t, "snapshot", "create", "demo", "--name", "nightly").assertCode(t, exitcode.CodeOK)
	c.run(t, "hosts", "sync").assertCode(t, exitcode.CodeOK)

	r := c.run(t, "snapshot", "restore", "demo", "--name", "nightly", "--yes")

	r.assertCode(t, exitcode.CodeOK)
	if strings.Contains(r.stdout, "pending") {
		t.Errorf("tamp asked for a hosts entry it already has:\n%s", r.stdout)
	}
}
