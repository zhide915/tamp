package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/frappe"
)

// The template cache is a promise about time, so these tests reach into the
// store directly: they plant a template of a chosen age rather than wait for
// one to grow old.

// ranCount is how many container commands contained fragment — what tells a
// second bench init from none at all.
func (c *cli) ranCount(fragment string) int {
	n := 0
	for _, e := range c.engine.Execs {
		if strings.Contains(e.Line(), fragment) {
			n++
		}
	}
	return n
}

// storedTemplate is the manifest tamp recorded for a version, or nil.
func (c *cli) storedTemplate(t *testing.T, version string) map[string]any {
	t.Helper()
	body, ok := c.engine.Wrote(frappe.TemplateManifestPath(version))
	if !ok {
		return nil
	}
	manifest := map[string]any{}
	if err := json.Unmarshal([]byte(body), &manifest); err != nil {
		t.Fatalf("the %s manifest is not valid JSON: %v\n%s", version, err, body)
	}
	return manifest
}

// plantTemplate puts a template of a given age in the store, as a create that
// ran that long ago would have left it.
func (c *cli) plantTemplate(t *testing.T, version string, age time.Duration, python, node string) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"schema":      1,
		"frappe":      version,
		"bench_image": env.BenchImage,
		"python":      python,
		"node":        node,
		"created":     time.Now().Add(-age).Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("cannot render a manifest: %v", err)
	}
	c.engine.Files[frappe.TemplatePath(version)] = "a tarred bench"
	c.engine.Files[frappe.TemplateManifestPath(version)] = string(body)
}

// wipeTemplates empties the store, which is always allowed to happen.
func (c *cli) wipeTemplates() {
	for path := range c.engine.Files {
		if strings.HasPrefix(path, frappe.TemplateDir+"/") {
			delete(c.engine.Files, path)
		}
	}
}

// globalConfig writes the machine's settings file.
func (c *cli) globalConfig(t *testing.T, body string) {
	t.Helper()
	path := filepath.Join(c.home, env.HomeDirName, env.GlobalConfigFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("cannot make %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("cannot write %s: %v", path, err)
	}
}

// --- filling the store ----------------------------------------------------

func TestTheFirstCreateOfAVersionCachesItsInitializedBench(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "create", "demo", "--frappe", "version-15")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "template cache missed for version-15")
	if !c.engine.Ran("bench init") {
		t.Error("a cold cache did not initialize a bench")
	}
	if _, ok := c.engine.Wrote(frappe.TemplatePath("version-15")); !ok {
		t.Errorf("create stored no template; the container holds %v", c.engine.Written())
	}
	manifest := c.storedTemplate(t, "version-15")
	if manifest == nil {
		t.Fatal("create stored a template with no manifest")
	}
	if manifest["python"] != "3.11" || manifest["node"] != "18" {
		t.Errorf("the manifest records python %v on node %v, want 3.11 on 18",
			manifest["python"], manifest["node"])
	}
}

// The environment is what the user asked for; the template is only speed.
func TestAStoreThatRefusesToWriteStillLeavesAWorkingEnvironment(t *testing.T) {
	c := sandbox(t)
	c.engine.ExecFails = map[string]error{
		"gzip -1": errors.New("no space left on device"),
	}

	r := c.run(t, "create", "demo", "--frappe", "version-15")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStderrContains(t, "could not store the version-15 template")
	r.assertStdoutContains(t, "demo ready")
}

// --- using the store ------------------------------------------------------

func TestASecondCreateOfTheSameVersionUnpacksTheTemplate(t *testing.T) {
	c := sandbox(t)
	c.create(t, "first")
	mark := c.mark()

	r := c.run(t, "create", "second", "--frappe", "version-15")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "template cache hit for version-15")
	if got := c.ranCount("bench init"); got != 1 {
		t.Errorf("bench init ran %d times across two creates, want 1", got)
	}
	if !ranAny(c.engine.Execs[mark:], frappe.TemplatePath("version-15")) {
		t.Error("the second create never reached for the stored template")
	}
}

// One template per Frappe version: a version-16 bench is not a version-15 one.
func TestATemplateIsNeverUsedForAnotherFrappeVersion(t *testing.T) {
	c := sandbox(t)
	c.create(t, "fifteen")

	r := c.run(t, "create", "sixteen", "--frappe", "version-16")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "template cache missed for version-16")
	if got := c.ranCount("bench init"); got != 2 {
		t.Errorf("bench init ran %d times, want 2 — one per version", got)
	}
	if _, ok := c.engine.Wrote(frappe.TemplatePath("version-16")); !ok {
		t.Error("the version-16 create stored no template of its own")
	}
}

// The template's virtualenv was built against the versions in its manifest; a
// matrix that moved since costs a requirements pass, not a bench init.
func TestAToolchainThatMovedSinceTheTemplateReinstallsTheRequirements(t *testing.T) {
	c := sandbox(t)
	c.plantTemplate(t, "version-15", 24*time.Hour, "3.10", "18")

	r := c.run(t, "create", "demo", "--frappe", "version-15")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "template cache hit", "python 3.10")
	if !c.engine.Ran("bench setup requirements") {
		t.Error("a drifted template was unpacked without reinstalling requirements")
	}
	if c.engine.Ran("bench init") {
		t.Error("a drifted template was rebuilt from scratch rather than reused")
	}
}

// The re-store is what stops the repair repeating: a second create of the
// same version finds a template that matches the matrix.
func TestARepairedTemplateIsNotRepairedAgainOnTheNextCreate(t *testing.T) {
	c := sandbox(t)
	c.plantTemplate(t, "version-15", 24*time.Hour, "3.10", "18")
	c.create(t, "first")
	mark := c.mark()

	r := c.run(t, "create", "second", "--frappe", "version-15")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "template cache hit for version-15")
	if ranAny(c.engine.Execs[mark:], "bench setup requirements") {
		t.Error("the second create repaired a template the first one already repaired")
	}
}

func TestATemplateMatchingTheMatrixSkipsTheRequirementsPass(t *testing.T) {
	c := sandbox(t)
	c.plantTemplate(t, "version-15", 24*time.Hour, "3.11", "18")

	r := c.run(t, "create", "demo", "--frappe", "version-15")

	r.assertCode(t, exitcode.CodeOK)
	if c.engine.Ran("bench setup requirements") {
		t.Error("an up-to-date template still reinstalled its requirements")
	}
}

// A repair reinstalls requirements; it does not re-clone frappe. Dating the
// re-stored template today would hand every later create a checkout older than
// the expiry promises.
func TestRepairingATemplateDoesNotRestartItsExpiryClock(t *testing.T) {
	c := sandbox(t)
	c.plantTemplate(t, "version-15", 13*24*time.Hour, "3.10", "18")

	c.run(t, "create", "demo", "--frappe", "version-15").assertCode(t, exitcode.CodeOK)

	manifest := c.storedTemplate(t, "version-15")
	if manifest == nil {
		t.Fatal("the repaired bench was not stored")
	}
	created, err := time.Parse(time.RFC3339Nano, manifest["created"].(string))
	if err != nil {
		t.Fatalf("the re-stored manifest has no readable timestamp: %v", err)
	}
	if age := time.Since(created); age < 12*24*time.Hour {
		t.Errorf("the re-stored template is dated %v old, want the 13 days it was — the repair bought it a fresh TTL", age)
	}
}

// --- the TTL --------------------------------------------------------------

// Release branches move under a template, so an old one is a lie about the
// branch the user asked for.
func TestATemplatePastItsExpiryIsRebuiltAndReCached(t *testing.T) {
	c := sandbox(t)
	c.plantTemplate(t, "version-15", 15*24*time.Hour, "3.11", "18")

	r := c.run(t, "create", "demo", "--frappe", "version-15")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "the stored version-15 template is past its expiry")
	if !c.engine.Ran("bench init") {
		t.Error("an expired template was reused instead of rebuilt")
	}
	manifest := c.storedTemplate(t, "version-15")
	if manifest == nil {
		t.Fatal("the expired template was not re-cached")
	}
	created, err := time.Parse(time.RFC3339Nano, manifest["created"].(string))
	if err != nil {
		t.Fatalf("the re-cached manifest has no readable timestamp: %v", err)
	}
	if time.Since(created) > time.Hour {
		t.Errorf("the expired template was not re-cached: its manifest still reads %s", created)
	}
}

func TestTheTemplateTTLIsConfigurableInTheGlobalConfig(t *testing.T) {
	c := sandbox(t)
	c.globalConfig(t, "[cache]\ntemplate_ttl_days = 30\n")
	c.plantTemplate(t, "version-15", 20*24*time.Hour, "3.11", "18")

	r := c.run(t, "create", "demo", "--frappe", "version-15")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "template cache hit for version-15")
}

// Zero days is how the cache is turned off for good.
func TestATemplateTTLOfZeroExpiresEveryTemplate(t *testing.T) {
	c := sandbox(t)
	c.globalConfig(t, "[cache]\ntemplate_ttl_days = 0\n")
	c.plantTemplate(t, "version-15", time.Minute, "3.11", "18")

	r := c.run(t, "create", "demo", "--frappe", "version-15")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "past its expiry")
	if !c.engine.Ran("bench init") {
		t.Error("a zero TTL still reused a stored template")
	}
}

// --- --no-cache -----------------------------------------------------------

func TestNoCacheInitializesAFreshBenchAndLeavesTheStoredTemplateAlone(t *testing.T) {
	c := sandbox(t)
	c.create(t, "first")
	stored, _ := c.engine.Wrote(frappe.TemplateManifestPath("version-15"))
	mark := c.mark()

	r := c.run(t, "create", "second", "--frappe", "version-15", "--no-cache")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "template cache skipped")
	if got := c.ranCount("bench init"); got != 2 {
		t.Errorf("bench init ran %d times, want 2 — --no-cache means a fresh one", got)
	}
	// The directory itself is chowned on every create; the tarball is only
	// ever named by a probe, a restore or a save.
	if ranAny(c.engine.Execs[mark:], frappe.TemplatePath("version-15")) {
		t.Error("--no-cache still went to the stored template")
	}
	// The manifest carries the moment it was written, so a re-store would
	// change it.
	if now, _ := c.engine.Wrote(frappe.TemplateManifestPath("version-15")); now != stored {
		t.Error("--no-cache rewrote the stored template's manifest")
	}
}

// --- wiping the store -----------------------------------------------------

func TestAnEmptiedTemplateStoreOnlyCostsTheNextCreateItsFullPrice(t *testing.T) {
	c := sandbox(t)
	c.create(t, "first")
	c.wipeTemplates()

	r := c.run(t, "create", "second", "--frappe", "version-15")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "template cache missed for version-15", "second ready")
	if got := c.ranCount("bench init"); got != 2 {
		t.Errorf("bench init ran %d times, want 2 — the store was emptied between them", got)
	}
}

// A tarball with no manifest is a template tamp cannot vouch for.
func TestATemplateWithNoManifestIsTreatedAsNoTemplate(t *testing.T) {
	c := sandbox(t)
	c.create(t, "first")
	delete(c.engine.Files, frappe.TemplateManifestPath("version-15"))

	r := c.run(t, "create", "second", "--frappe", "version-15")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "template cache missed")
	if got := c.ranCount("bench init"); got != 2 {
		t.Errorf("bench init ran %d times, want 2", got)
	}
}

// --- the store belongs to the machine ------------------------------------

func TestTheTemplateStoreIsAVolumeNoEnvironmentOwns(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")

	compose := c.read(t, "demo", "compose.yaml")
	if !strings.Contains(compose, frappe.TemplateVolume) {
		t.Errorf("the compose file does not mount the template store:\n%s", compose)
	}
	if !strings.Contains(compose, frappe.TemplateDir) {
		t.Errorf("the template store is not mounted at %s:\n%s", frappe.TemplateDir, compose)
	}

	// External: 'tamp rm --volumes' on one environment must not empty the
	// machine's template store for the rest.
	c.run(t, "rm", "demo", "--volumes", "--yes").assertCode(t, exitcode.CodeOK)
	for _, removed := range c.engine.Removed {
		if removed == frappe.TemplateVolume {
			t.Error("tamp rm --volumes destroyed the machine's template store")
		}
	}
}
