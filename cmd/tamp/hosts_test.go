package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/hosts"
)

// tamp owns one marked block in the hosts file and nothing else in it. Every
// test here is a way of asking whether that still holds.

// theirHosts is a hosts file as the machine's owner left it — what must come
// back byte for byte from every sync.
const theirHosts = "127.0.0.1\tlocalhost\n" +
	"::1\tlocalhost\n" +
	"# the VPN wrote this\n" +
	"10.1.2.3\tintranet.corp\n"

func (c *cli) hostsPath() string { return filepath.Join(c.home, "hosts") }

func (c *cli) writeHosts(t *testing.T, body string) {
	t.Helper()
	if err := os.WriteFile(c.hostsPath(), []byte(body), 0o644); err != nil {
		t.Fatalf("cannot plant a hosts file: %v", err)
	}
}

func (c *cli) hosts(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(c.hostsPath())
	if err != nil {
		t.Fatalf("cannot read the hosts file: %v", err)
	}
	return string(body)
}

// outsideBlock is everything the file holds that is not tamp's.
func outsideBlock(t *testing.T, file string) string {
	t.Helper()
	begin := strings.Index(file, hosts.BeginMarker)
	if begin < 0 {
		return file
	}
	end := strings.Index(file, hosts.EndMarker)
	if end < 0 {
		t.Fatalf("a begin marker with no end:\n%s", file)
	}
	return file[:begin] + file[end+len(hosts.EndMarker)+1:]
}

// --- the custom-domain flow ------------------------------------------------

func TestHostsSyncWritesACustomDomainInsideTheBlockOnly(t *testing.T) {
	c := sandbox(t)
	c.writeHosts(t, theirHosts)
	c.create(t, "demo")
	c.siteNew(t, "demo", "abc.xyz.com")

	r := c.run(t, "hosts", "sync")

	r.assertCode(t, exitcode.CodeOK)
	file := c.hosts(t)
	if !strings.Contains(file, "127.0.0.1  abc.xyz.com") {
		t.Errorf("the custom domain never reached the hosts file:\n%s", file)
	}
	if got := outsideBlock(t, file); got != theirHosts {
		t.Errorf("tamp changed something outside its block:\n%q\nwant:\n%q", got, theirHosts)
	}
}

// The two halves of the flow are one promise: site new says what is missing,
// and the command it names supplies it.
func TestSiteNewMarksTheEntryPendingAndSyncMakesItPresent(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo")

	created := c.run(t, "site", "new", "demo", "abc.xyz.com")
	created.assertCode(t, exitcode.CodeOK)
	created.assertStdoutContains(t, "pending", "tamp hosts sync")

	before := c.run(t, "site", "list", "demo")
	before.assertStdoutContains(t, "HOSTS ENTRY", "pending")

	c.run(t, "hosts", "sync").assertCode(t, exitcode.CodeOK)

	after := c.run(t, "site", "list", "demo")
	after.assertStdoutContains(t, "abc.xyz.com", "ok")
	if strings.Contains(after.stdout, "pending") {
		t.Errorf("the entry is still pending after a sync:\n%s", after.stdout)
	}
}

// A *.localhost site needs no line, so it must never get one.
func TestHostsSyncLeavesLocalhostSitesOutOfTheBlock(t *testing.T) {
	c := sandbox(t)
	c.writeHosts(t, theirHosts)
	c.create(t, "demo")
	c.siteNew(t, "demo", "shop.localhost")

	c.run(t, "hosts", "sync").assertCode(t, exitcode.CodeOK)

	if got := c.hosts(t); got != theirHosts {
		t.Errorf("a .localhost site changed the hosts file:\n%q", got)
	}
}

func TestHostsSyncCollectsCustomDomainsFromEveryEnvironment(t *testing.T) {
	c := sandbox(t)
	c.create(t, "one")
	c.create(t, "two")
	c.siteNew(t, "one", "one.example.test")
	c.siteNew(t, "two", "two.example.test")

	c.run(t, "hosts", "sync").assertCode(t, exitcode.CodeOK)

	file := c.hosts(t)
	for _, host := range []string{"one.example.test", "two.example.test"} {
		if !strings.Contains(file, "127.0.0.1  "+host) {
			t.Errorf("%s is missing from the block:\n%s", host, file)
		}
	}
}

// --- removal ---------------------------------------------------------------

func TestSiteRemovalTakesItsLineOutOnTheNextSync(t *testing.T) {
	c := sandbox(t)
	c.writeHosts(t, theirHosts)
	c.create(t, "demo")
	c.siteNew(t, "demo", "abc.xyz.com")
	c.run(t, "hosts", "sync").assertCode(t, exitcode.CodeOK)

	c.run(t, "site", "rm", "demo", "abc.xyz.com", "--yes").assertCode(t, exitcode.CodeOK)
	r := c.run(t, "hosts", "sync")

	r.assertCode(t, exitcode.CodeOK)
	if got := c.hosts(t); got != theirHosts {
		t.Errorf("the hosts file did not come back byte for byte:\n%q\nwant:\n%q", got, theirHosts)
	}
}

// --- the registry is the authority -----------------------------------------

// The cached site lists are exactly why a sync needs no containers: a machine
// whose environments are all stopped still knows what it answers to.
func TestHostsSyncWorksWithEveryEnvironmentStopped(t *testing.T) {
	c := sandbox(t)
	c.writeHosts(t, theirHosts)
	c.create(t, "demo")
	c.siteNew(t, "demo", "abc.xyz.com")
	c.run(t, "stop", "demo").assertCode(t, exitcode.CodeOK)
	before := c.mark()

	r := c.run(t, "hosts", "sync")

	r.assertCode(t, exitcode.CodeOK)
	if !strings.Contains(c.hosts(t), "abc.xyz.com") {
		t.Errorf("a stopped environment lost its custom domain:\n%s", c.hosts(t))
	}
	if len(c.engine.Execs) != before {
		t.Errorf("hosts sync ran %d container commands; it must need none",
			len(c.engine.Execs)-before)
	}
}

// --- idempotence -----------------------------------------------------------

func TestASecondSyncChangesNothingAndSaysSo(t *testing.T) {
	c := sandbox(t)
	c.writeHosts(t, theirHosts)
	c.create(t, "demo")
	c.siteNew(t, "demo", "abc.xyz.com")
	c.run(t, "hosts", "sync").assertCode(t, exitcode.CodeOK)
	first := c.hosts(t)

	r := c.run(t, "hosts", "sync")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "already in sync")
	if got := c.hosts(t); got != first {
		t.Errorf("the second sync rewrote the file:\n%q\nwant:\n%q", got, first)
	}
}

// A machine with nothing but .localhost sites has nothing to write, and must
// not create a hosts file to say so.
func TestHostsSyncOnAMachineWithNoCustomDomainsWritesNothing(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "hosts", "sync")

	r.assertCode(t, exitcode.CodeOK)
	r.assertStdoutContains(t, "already in sync")
	if _, err := os.Stat(c.hostsPath()); err == nil {
		t.Error("tamp created a hosts file to hold an empty block")
	}
}

// --- when the file will not be written -------------------------------------

// The redirect exists for tests, so tamp must never aim an elevation at it: a
// refused write on a redirected file is an error, not a UAC prompt.
func TestASyncOfARedirectedFileNeverElevates(t *testing.T) {
	c := sandbox(t)
	c.writeHosts(t, theirHosts)
	c.create(t, "demo")
	c.siteNew(t, "demo", "abc.xyz.com")
	if err := os.Chmod(c.hostsPath(), 0o444); err != nil {
		t.Fatal(err)
	}
	// Running as root (or anything else that can write it anyway) has no
	// refusal to observe.
	if f, err := os.OpenFile(c.hostsPath(), os.O_WRONLY, 0o444); err == nil {
		_ = f.Close()
		t.Skip("this user can write a read-only file, so there is no refusal to test")
	}

	r := c.run(t, "hosts", "sync")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, "cannot write")
	if got := c.hosts(t); got != theirHosts {
		t.Errorf("the refused write reached the file anyway:\n%q", got)
	}
}
