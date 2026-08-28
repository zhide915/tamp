package main

import (
	"strconv"
	"testing"

	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/exitcode"
)

// db is the one exception to hostname-only access: a MySQL client needs a
// loopback port, a credential, and the database name Frappe never repeats.

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

func TestDBSaysWhenThereIsNothingInTheDatabaseYet(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")

	r := c.run(t, "db", "demo")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "no databases yet", "tamp site new demo")
}

// The details come from tamp's own record, so a stopped environment answers.
func TestDBAnswersWithTheEnvironmentStopped(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.siteNew(t, "demo", "shop.localhost")
	c.run(t, "stop", "demo").assertCode(t, exitcode.CodeOK)

	r := c.run(t, "db", "demo")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, strconv.Itoa(c.dbPort(t, "demo")), "shop.localhost")
}

// dbPort reads the allocated port back rather than assuming it: allocation
// skips ports already in use on this machine.
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

func (c *cli) dbPassword(t *testing.T, name string) string {
	t.Helper()
	password, err := env.ReadDBRootPassword(c.path(name))
	if err != nil {
		t.Fatalf("cannot read the database credential: %v", err)
	}
	return password
}
