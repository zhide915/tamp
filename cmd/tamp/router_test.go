package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/docker"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/router"
)

// newIn runs `tamp new <name>` from the sandbox's directory, expects it to
// succeed, and moves into the environment's directory.
func (c *cli) newIn(t *testing.T, name string, args ...string) string {
	t.Helper()
	t.Chdir(c.dir)
	c.run(t, append([]string{"new", name, "--frappe", "version-15"}, args...)...).assertCode(t, exitcode.CodeOK)
	dir := filepath.Join(c.dir, name)
	t.Chdir(dir)
	return dir
}

func (c *cli) caddyfile(t *testing.T) string {
	t.Helper()
	return readFile(t, filepath.Join(router.Dir(c.home), "Caddyfile"))
}

// siteBlocks splits a Caddyfile into each site's block, keyed by hostname.
func siteBlocks(caddyfile string) map[string]string {
	blocks := map[string]string{}
	for _, m := range regexp.MustCompile(`(?ms)^http://(\S+) \{\n(.*?)^\}$`).FindAllStringSubmatch(caddyfile, -1) {
		blocks[m[1]] = m[2]
	}
	return blocks
}

func assertRoutedTo(t *testing.T, caddyfile, host, upstream string) {
	t.Helper()
	block, ok := siteBlocks(caddyfile)[host]
	if !ok {
		t.Errorf("the Caddyfile does not route %s:\n%s", host, caddyfile)
		return
	}
	want := "\t@socketio path /socket.io*\n" +
		"\treverse_proxy @socketio " + upstream + ":9000 {\n" +
		"\t\theader_up Origin {scheme}://{host}\n" +
		"\t}\n" +
		"\treverse_proxy " + upstream + ":8000\n"
	if block != want {
		t.Errorf("the block for %s =\n%s\nwant\n%s", host, block, want)
	}
}

func writeRecipe(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "tamp.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func routerExecs(execs []docker.Exec) []string {
	var lines []string
	for _, e := range execs {
		if e.Container == router.Container {
			lines = append(lines, strings.Join(e.Cmd, " "))
		}
	}
	return lines
}

const reload = "caddy reload --config /etc/caddy/Caddyfile --adapter caddyfile"

func TestStartRoutesEverySiteOfEveryRunningEnvironment(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev", "--site", "shop.localhost", "--site", "erp.localhost")
	c.newIn(t, "other", "--site", "blog.localhost")

	caddyfile := c.caddyfile(t)

	assertRoutedTo(t, caddyfile, "shop.localhost", "erp-dev-frappe-1")
	assertRoutedTo(t, caddyfile, "erp.localhost", "erp-dev-frappe-1")
	assertRoutedTo(t, caddyfile, "blog.localhost", "other-frappe-1")
	if n := len(siteBlocks(caddyfile)); n != 3 {
		t.Errorf("the Caddyfile has %d site blocks, want 3:\n%s", n, caddyfile)
	}
}

func TestTheRouterIsCaddyOnPort80RestartingUnlessStoppedWithItsDirectoryMounted(t *testing.T) {
	c := sandbox(t)

	c.newIn(t, "erp-dev", "--site", "shop.localhost")

	ups := c.docker.UpsOf("tamp-router")
	if len(ups) != 1 {
		t.Fatalf("router compose ups = %d, want 1", len(ups))
	}
	up := ups[0]
	if want := filepath.Join(router.Dir(c.home), "compose.yaml"); up.Project.File != want {
		t.Errorf("router compose file = %s, want %s beside the Caddyfile", up.Project.File, want)
	}
	for _, line := range []string{
		"    image: caddy:",
		"    container_name: tamp-router\n",
		"    restart: unless-stopped\n",
		"      - 127.0.0.1:80:80\n",
		// The directory, so a renamed Caddyfile reaches the container.
		"      - .:/etc/caddy:ro\n",
	} {
		if !strings.Contains(up.Compose, line) {
			t.Errorf("the router's compose file lacks %q:\n%s", line, up.Compose)
		}
	}
	if strings.Contains(up.Compose, "Caddyfile:") {
		t.Errorf("the router mounts the Caddyfile itself, not its directory:\n%s", up.Compose)
	}
}

func TestTheCaddyfileIsReplacedByRenameNotRewrittenInPlace(t *testing.T) {
	c := sandbox(t)
	dir := c.newIn(t, "erp-dev", "--site", "shop.localhost")
	caddyfile := filepath.Join(router.Dir(c.home), "Caddyfile")
	before := readFile(t, caddyfile)
	// A hard link keeps the old file: writing in place would change it too.
	old := filepath.Join(t.TempDir(), "old")
	if err := os.Link(caddyfile, old); err != nil {
		t.Fatal(err)
	}
	writeRecipe(t, dir, "frappe = \"version-15\"\n\n[[sites]]\n  host = \"shop.localhost\"\n\n[[sites]]\n  host = \"erp.localhost\"\n")

	c.run(t, "start").assertCode(t, exitcode.CodeOK)

	if got := readFile(t, old); got != before {
		t.Errorf("the old Caddyfile changed in place:\n%s", got)
	}
	assertRoutedTo(t, readFile(t, caddyfile), "erp.localhost", "erp-dev-frappe-1")
	entries, err := os.ReadDir(router.Dir(c.home))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "Caddyfile" && e.Name() != "compose.yaml" {
			t.Errorf("the router's directory holds %s, want only Caddyfile and compose.yaml", e.Name())
		}
	}
}

func TestStartReloadsARunningRouterInsteadOfStartingIt(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev", "--site", "shop.localhost")
	execs := len(c.docker.Execs)

	c.run(t, "start").assertCode(t, exitcode.CodeOK)

	if ups := c.docker.UpsOf("tamp-router"); len(ups) != 1 {
		t.Errorf("router compose ups = %d, want only the first start's", len(ups))
	}
	if got := routerExecs(c.docker.Execs[execs:]); !slices.Equal(got, []string{reload}) {
		t.Errorf("router execs = %q, want one %q", got, reload)
	}
}

func TestTheRouterJoinsEachRunningEnvironmentsNetworkAnsweringToItsSites(t *testing.T) {
	c := sandbox(t)
	dir := c.newIn(t, "erp-dev", "--site", "shop.localhost")
	c.newIn(t, "other", "--site", "blog.localhost")

	want := []docker.Network{
		{Network: "erp-dev_default", Container: "tamp-router", Aliases: []string{"shop.localhost"}},
		{Network: "other_default", Container: "tamp-router", Aliases: []string{"blog.localhost"}},
	}
	assertNetworks(t, "connects", c.docker.Connects, want)
	if len(c.docker.Disconnects) > 0 {
		t.Errorf("disconnects = %v, want none", c.docker.Disconnects)
	}

	// A new site needs a new alias, and aliases change only on reconnect.
	writeRecipe(t, dir, "frappe = \"version-15\"\n\n[[sites]]\n  host = \"shop.localhost\"\n\n[[sites]]\n  host = \"erp.localhost\"\n")
	t.Chdir(dir)
	connects := len(c.docker.Connects)
	c.run(t, "start").assertCode(t, exitcode.CodeOK)

	assertNetworks(t, "disconnects", c.docker.Disconnects, []docker.Network{{Network: "erp-dev_default", Container: "tamp-router"}})
	assertNetworks(t, "new connects", c.docker.Connects[connects:], []docker.Network{
		{Network: "erp-dev_default", Container: "tamp-router", Aliases: []string{"shop.localhost", "erp.localhost"}},
	})
}

func assertNetworks(t *testing.T, what string, got, want []docker.Network) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s = %v, want %v", what, got, want)
		return
	}
	for i := range want {
		if got[i].Network != want[i].Network || got[i].Container != want[i].Container ||
			!slices.Equal(got[i].Aliases, want[i].Aliases) {
			t.Errorf("%s = %v, want %v", what, got, want)
			return
		}
	}
}

func TestAnUnreadableRecipeIsSkippedWithAWarningAndTheOthersStillReload(t *testing.T) {
	c := sandbox(t)
	other := c.newIn(t, "other", "--site", "blog.localhost")
	c.newIn(t, "erp-dev", "--site", "shop.localhost")
	writeRecipe(t, other, "frappe = [unclosed\n")
	execs := len(c.docker.Execs)

	r := c.run(t, "start", "--quiet")

	r.assertCode(t, exitcode.CodeOK)
	if !strings.Contains(r.stderr, "warning: the router skips other: ") ||
		!strings.Contains(r.stderr, "fix the TOML in "+filepath.Join(other, "tamp.toml")) {
		t.Errorf("stderr = %q, want a warning that the router skips other, naming its recipe and the fix", r.stderr)
	}
	caddyfile := c.caddyfile(t)
	if _, ok := siteBlocks(caddyfile)["blog.localhost"]; ok {
		t.Errorf("the Caddyfile still routes the skipped blog.localhost:\n%s", caddyfile)
	}
	assertRoutedTo(t, caddyfile, "shop.localhost", "erp-dev-frappe-1")
	if got := routerExecs(c.docker.Execs[execs:]); !slices.Equal(got, []string{reload}) {
		t.Errorf("router execs = %q, want one %q", got, reload)
	}
}

func TestAHostnameTwoEnvironmentsListGoesToTheFirstByNameWithAWarning(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "alpha", "--site", "shop.localhost")
	zeta := c.newIn(t, "zeta", "--site", "blog.localhost")
	// zeta is built last, so the last build does not decide the winner.
	writeRecipe(t, zeta, "frappe = \"version-15\"\n\n[[sites]]\n  host = \"blog.localhost\"\n\n[[sites]]\n  host = \"shop.localhost\"\n")
	connects := len(c.docker.Connects)

	r := c.run(t, "start", "--quiet")

	r.assertCode(t, exitcode.CodeOK)
	if want := "warning: alpha and zeta both list shop.localhost, so the router sends it to alpha\n"; r.stderr != want {
		t.Errorf("stderr = %q, want %q", r.stderr, want)
	}
	caddyfile := c.caddyfile(t)
	assertRoutedTo(t, caddyfile, "shop.localhost", "alpha-frappe-1")
	assertRoutedTo(t, caddyfile, "blog.localhost", "zeta-frappe-1")
	if n := strings.Count(caddyfile, "http://shop.localhost "); n != 1 {
		t.Errorf("shop.localhost has %d site blocks, want 1:\n%s", n, caddyfile)
	}
	// zeta's network keeps blog.localhost alone, so shop.localhost resolves
	// nowhere it is not routed.
	if got := c.docker.Connects[connects:]; len(got) > 0 {
		t.Errorf("connects = %v, want none: zeta's aliases are unchanged", got)
	}
}

func TestPort80TakenExits1WithAFixAndNoFallbackPort(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev", "--site", "shop.localhost")
	c.docker.Stop("tamp-router")
	c.docker.UpFails = map[string]error{"tamp-router": fmt.Errorf("docker compose failed: %w", docker.ErrPortTaken)}
	before := len(c.docker.UpsOf("tamp-router"))

	r := c.run(t, "start", "--quiet")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertOneLineError(t, "port 80 is taken", "stop what listens on port 80", "then run `tamp start`")
	ups := c.docker.UpsOf("tamp-router")[before:]
	if len(ups) != 1 {
		t.Fatalf("router compose ups = %d, want exactly 1", len(ups))
	}
	if ports := regexp.MustCompile(`(?m)^      - [\d.:]+$`).FindAllString(ups[0].Compose, -1); !slices.Equal(ports, []string{"      - 127.0.0.1:80:80"}) {
		t.Errorf("published ports = %q, want only port 80", ports)
	}
}

func TestTheWebServerListensOnEveryInterfaceSoTheRouterReachesItOnEveryBranch(t *testing.T) {
	c := sandbox(t)

	c.newIn(t, "erp-dev")

	if frappe := services(t, c.docker.UpsOf("erp-dev")[0].Compose)["frappe"]; !strings.Contains(frappe, "      FRAPPE_BIND_ADDR: 0.0.0.0\n") {
		t.Errorf("the frappe service does not set FRAPPE_BIND_ADDR to 0.0.0.0:\n%s", frappe)
	}
}

func TestOpenHandsTheOnlySitesURLToTheBrowser(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev", "--site", "shop.localhost")

	r := c.run(t, "open")

	r.assertCode(t, exitcode.CodeOK)
	if !slices.Equal(c.opened, []string{"http://shop.localhost"}) {
		t.Errorf("opened = %q, want http://shop.localhost", c.opened)
	}
	if !strings.Contains(r.stdout, "http://shop.localhost") {
		t.Errorf("stdout = %q, want the URL", r.stdout)
	}
}

func TestOpenWithSeveralSitesExits2ListingThemAndOpensNothing(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev", "--site", "shop.localhost", "--site", "erp.localhost")

	r := c.run(t, "open")

	r.assertCode(t, exitcode.CodeUsage)
	r.assertOneLineError(t, "shop.localhost", "erp.localhost", "`tamp open <host>`")
	if len(c.opened) > 0 {
		t.Errorf("opened = %q, want nothing", c.opened)
	}
}

func TestOpenWithAHostOpensThatSite(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev", "--site", "shop.localhost", "--site", "erp.localhost")

	c.run(t, "open", "erp.localhost").assertCode(t, exitcode.CodeOK)

	if !slices.Equal(c.opened, []string{"http://erp.localhost"}) {
		t.Errorf("opened = %q, want http://erp.localhost", c.opened)
	}
}

func TestOpenWithAHostTheRecipeDoesNotListExits3(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev", "--site", "shop.localhost")

	r := c.run(t, "open", "blog.localhost")

	r.assertCode(t, exitcode.CodeNotFound)
	r.assertOneLineError(t, "blog.localhost", "shop.localhost")
	if len(c.opened) > 0 {
		t.Errorf("opened = %q, want nothing", c.opened)
	}
}

func TestTheRouterSkipsAMovedEnvironmentWithAFixThatWorks(t *testing.T) {
	c := sandbox(t)
	other := c.newIn(t, "other", "--site", "blog.localhost")
	c.newIn(t, "erp-dev", "--site", "shop.localhost")
	// The directory moved away: its recipe is gone from where the labels point.
	if err := os.Remove(filepath.Join(other, "tamp.toml")); err != nil {
		t.Fatal(err)
	}

	r := c.run(t, "start", "--quiet")

	r.assertCode(t, exitcode.CodeOK)
	want := "warning: the router skips other: no recipe in " + other +
		": run `tamp start` in the directory the environment moved to, or remove it with `tamp remove -e other --yes`\n"
	if r.stderr != want {
		t.Errorf("stderr = %q, want %q", r.stderr, want)
	}
}

func TestStartsPortErrorNamesTheContainerThatPublishesPort80(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev", "--site", "shop.localhost")
	c.docker.Stop("tamp-router")
	c.docker.UpFails = map[string]error{"tamp-router": fmt.Errorf("docker compose failed: %w", docker.ErrPortTaken)}
	c.docker.Publishers = map[int][]string{80: {"web-1"}}

	r := c.run(t, "start", "--quiet")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertOneLineError(t, "port 80 is taken", "`docker stop web-1`", "then run `tamp start`")
}

func TestStartKeepsThePortMessageWhenDockerCannotListWhatPublishesPort80(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev", "--site", "shop.localhost")
	c.docker.Stop("tamp-router")
	c.docker.UpFails = map[string]error{"tamp-router": fmt.Errorf("docker compose failed: %w", docker.ErrPortTaken)}
	c.docker.PublishingErr = exitcode.New(exitcode.CodeDockerUnavailable, "cannot list Docker containers: EOF", "start Docker")

	r := c.run(t, "start", "--quiet")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertOneLineError(t, "port 80 is taken", "cannot list Docker containers: EOF", "stop what listens on port 80", "then run `tamp start`")
}

func TestAHostnameNoRunningEnvironmentServesGets404NamingTampList(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev", "--site", "shop.localhost")
	c.run(t, "remove", "--yes").assertCode(t, exitcode.CodeOK)

	caddyfile := c.caddyfile(t)

	want := "\nhttp:// {\n\trespond \"" + router.NotFoundBody + "\" 404\n}\n"
	if !strings.Contains(caddyfile, want) || !strings.Contains(router.NotFoundBody, "`tamp list`") {
		t.Errorf("the Caddyfile lacks the catch-all 404 block:\n%s", caddyfile)
	}
}
