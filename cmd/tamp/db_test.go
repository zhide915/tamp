package main

import (
	"strconv"
	"testing"

	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/exitcode"
)

// tamp's one exception to hostname-only access. What has to be right is that
// everything a database client needs is on screen and nothing else is needed:
// the port tamp allocated, the credential tamp generated, and the database
// name Frappe invented and never says again.

func TestDBPrintsEverythingAClientNeeds(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.siteNew(t, "demo", "shop.localhost")

	r := c.run(t, "db", "demo")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t,
		env.DBHost,
		strconv.Itoa(c.dbPort(t, "demo")),
		env.DBUser,
		c.dbPassword(t, "demo"),
		"shop.localhost",
		"_shop_localhost",
	)
}

// A bench with no sites has no databases, and saying so beats an empty table.
func TestDBSaysWhenThereIsNothingInTheDatabaseYet(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")

	r := c.run(t, "db", "demo")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "no databases yet", "tamp site new demo")
}

// The connection details are tamp's own record, so they survive a stopped
// environment — which is exactly when someone reaches for a GUI client.
func TestDBAnswersWithTheEnvironmentStopped(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.siteNew(t, "demo", "shop.localhost")
	c.run(t, "stop", "demo").assertCode(t, exitcode.CodeOK)

	r := c.run(t, "db", "demo")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, strconv.Itoa(c.dbPort(t, "demo")), "shop.localhost")
}

// dbPort is the host port tamp allocated to an environment.
//
// Read back rather than assumed: allocation skips ports something on this
// machine is already listening on, so a developer who happens to have one of
// them in use gets a different number and the same correct answer.
func (c *cli) dbPort(t *testing.T, name string) int {
	t.Helper()
	cfg, _, err := env.LoadConfig(env.ConfigPath(c.path(name)))
	if err != nil {
		t.Fatalf("cannot read %s's config: %v", name, err)
	}
	if cfg.Ports.DB < env.FirstDBPort {
		t.Errorf("tamp allocated port %d, below the first it may use (%d)", cfg.Ports.DB, env.FirstDBPort)
	}
	return cfg.Ports.DB
}

// dbPassword is the credential tamp generated for an environment.
func (c *cli) dbPassword(t *testing.T, name string) string {
	t.Helper()
	password, err := env.ReadDBRootPassword(c.path(name))
	if err != nil {
		t.Fatalf("cannot read the database credential: %v", err)
	}
	return password
}
