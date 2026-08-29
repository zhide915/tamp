package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/router"
)

// tamp open resolves an address and hands it over. What it must never do is
// open one that would not answer.

// assertOpened names hostnames, not URLs: the router takes port 8080 when
// something else already has 80, and every URL it hands out says so.
func (c *cli) assertOpened(t *testing.T, hosts ...string) {
	t.Helper()
	want := make([]string, 0, len(hosts))
	for _, host := range hosts {
		want = append(want, router.Status{Port: c.routerPort(t)}.URL(host))
	}
	if len(c.opened) != len(want) {
		t.Fatalf("tamp opened %v, want %v", c.opened, want)
	}
	for i, url := range want {
		if c.opened[i] != url {
			t.Errorf("opened[%d] = %q, want %q", i, c.opened[i], url)
		}
	}
}

// routerPort is the port the router settled on, which is the default until
// it has started once.
func (c *cli) routerPort(t *testing.T) int {
	t.Helper()
	path := filepath.Join(c.home, env.HomeDirName, router.DirName, router.StateFileName)
	body, err := os.ReadFile(path)
	if err != nil {
		return router.DefaultPort
	}
	var state struct {
		Port int `json:"port"`
	}
	if err := json.Unmarshal(body, &state); err != nil {
		t.Fatalf("%s is not JSON tamp could have written: %v", path, err)
	}
	return state.Port
}

func TestOpenWithNoTargetOpensTheEnvironmentsFirstSite(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.siteNew(t, "demo", "shop.localhost")

	r := c.run(t, "open", "demo")

	r.assertCode(t, exitcode.CodeOK)
	c.assertOpened(t, "shop.localhost")
}

func TestOpenTakesTheSiteToOpen(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.siteNew(t, "demo", "shop.localhost")
	c.siteNew(t, "demo", "other.localhost")

	r := c.run(t, "open", "demo", "other.localhost")

	r.assertCode(t, exitcode.CodeOK)
	c.assertOpened(t, "other.localhost")
}

// A hostname carries a dot and an environment name never does, which is what
// lets one lone argument mean either — here, the site inside the environment
// the user is standing in.
func TestOpenReadsALoneArgumentAsAHostnameWhenItHasADot(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.siteNew(t, "demo", "shop.localhost")
	c.siteNew(t, "demo", "other.localhost")

	r := c.inside(t, c.path("demo"), "open", "shop.localhost")

	r.assertCode(t, exitcode.CodeOK)
	c.assertOpened(t, "shop.localhost")
}

func TestOpenMailOpensTheEnvironmentsMailUI(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")

	r := c.run(t, "open", "demo", "mail")

	r.assertCode(t, exitcode.CodeOK)
	c.assertOpened(t, "mail.demo.localhost")
}

// --- refusing ---------------------------------------------------------------

func TestOpenRefusesAStoppedEnvironmentAndOpensNothing(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.siteNew(t, "demo", "shop.localhost")
	c.run(t, "stop", "demo").assertCode(t, exitcode.CodeOK)

	r := c.run(t, "open", "demo")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, "tamp start demo")
	c.assertOpened(t)
}

// A custom domain with no hosts entry resolves nowhere, so opening it would
// land the user on a browser error page rather than the site.
func TestOpenRefusesASiteWhoseHostsEntryIsPending(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.siteNew(t, "demo", "abc.xyz.com")

	r := c.run(t, "open", "demo", "abc.xyz.com")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, "tamp hosts sync")
	c.assertOpened(t)
}

func TestOpenOpensACustomDomainOnceItsHostsEntryIsWritten(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.siteNew(t, "demo", "abc.xyz.com")
	c.run(t, "hosts", "sync").assertCode(t, exitcode.CodeOK)

	r := c.run(t, "open", "demo", "abc.xyz.com")

	r.assertCode(t, exitcode.CodeOK)
	c.assertOpened(t, "abc.xyz.com")
}

// A line the user keeps outside tamp's block resolves the name just as well;
// refusing to open it would tell them to fix what is not broken.
func TestOpenHonoursAHostsEntryOutsideTampsBlock(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.siteNew(t, "demo", "abc.xyz.com")
	if err := os.WriteFile(filepath.Join(c.home, "hosts"), []byte("127.0.0.1  abc.xyz.com\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := c.run(t, "open", "demo", "abc.xyz.com")

	r.assertCode(t, exitcode.CodeOK)
	c.assertOpened(t, "abc.xyz.com")
}

func TestOpenRefusesAHostnameTheEnvironmentDoesNotHave(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.siteNew(t, "demo", "shop.localhost")

	r := c.run(t, "open", "demo", "other.localhost")

	r.assertCode(t, exitcode.CodeNotFound)
	r.assertStderrContains(t, "tamp site list demo")
	c.assertOpened(t)
}

// The router is the last thing between the URL and an answer, and no
// container in tamp restarts itself.
func TestOpenRefusesWhenTheRouterIsNotRunning(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.siteNew(t, "demo", "shop.localhost")
	c.engine.Down(router.Project)

	r := c.run(t, "open", "demo")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, "router is not running")
	c.assertOpened(t)
}

// "opening ..." is a claim about what happened, so it must not be printed
// when the machine had no browser to hand it to.
func TestOpenReportsABrowserThatWouldNotOpen(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")
	c.siteNew(t, "demo", "shop.localhost")
	c.openErr = exitcode.New(exitcode.CodeFailed,
		"cannot open http://shop.localhost: exec: \"xdg-open\": executable file not found in $PATH",
		"open the URL above in your browser yourself")

	r := c.run(t, "open", "demo")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, "cannot open http://shop.localhost")
	if strings.Contains(r.stdout, "opening") {
		t.Errorf("tamp said it opened a URL the browser refused:\n%s", r.stdout)
	}
}

func TestOpenRefusesAnEnvironmentWithNoSites(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")

	r := c.run(t, "open", "demo")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, "tamp site new demo")
	c.assertOpened(t)
}
