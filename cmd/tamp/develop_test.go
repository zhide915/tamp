package main

import (
	"strings"
	"testing"
	"time"

	"github.com/zhide915/tamp/internal/exitcode"
)

// develop is tamp's third preset, for people working on Frappe itself. It
// tracks a branch that moves under it, which is what these tests are about.

func TestCreateWithDevelopProvisionsTheDevelopToolchain(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "create", "dev", "--frappe", "develop")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "python 3.14 · node 24 · mariadb 11.8", "dev ready")

	// The branch bench init clones is the version tamp was asked for.
	if got := c.benchRan(t, "bench init"); got.Cmd[len(got.Cmd)-2] != "develop" {
		t.Errorf("bench init cloned %q, want develop", got.Cmd[len(got.Cmd)-2])
	}
	if compose := c.read(t, "dev", "compose.yaml"); !strings.Contains(compose, "mariadb:11.8") {
		t.Errorf("the develop environment does not run MariaDB 11.8:\n%s", compose)
	}
}

// A framework contributor keeps a stable environment beside the one they are
// breaking.
func TestADevelopEnvironmentAndAStableOneRunSideBySide(t *testing.T) {
	c := sandbox(t)
	c.create(t, "fifteen")

	c.run(t, "create", "dev", "--frappe", "develop").assertCode(t, exitcode.CodeOK)

	r := c.run(t, "list")
	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t,
		"fifteen", "version-15", "3.11", "10.11",
		"dev", "develop", "3.14", "11.8")

	// Both are up, on their own networks, with their own mail UIs.
	for _, name := range []string{"fifteen", "dev"} {
		if !c.routerIsOn(t, name) {
			t.Errorf("the router is not on %s's network, so nothing routes to it", name)
		}
	}
	if caddy := c.caddyfile(t); !strings.Contains(caddy, "mail.fifteen.localhost") || !strings.Contains(caddy, "mail.dev.localhost") {
		t.Errorf("the router does not carry both environments:\n%s", caddy)
	}
}

// develop's template is the one most exposed to branch movement, so the
// expiry has to bite for it exactly as it does for a release.
func TestADevelopTemplatePastItsExpiryIsRebuilt(t *testing.T) {
	c := sandbox(t)
	c.plantTemplate(t, "develop", 15*24*time.Hour, "3.14", "24")

	r := c.run(t, "create", "dev", "--frappe", "develop")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "the stored develop template is past its expiry")
	if !c.engine.Ran("bench init") {
		t.Error("an expired develop template was reused instead of rebuilt")
	}
}
