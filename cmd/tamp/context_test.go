package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/exitcode"
)

// versionOutputs are what the containers print for the version reads.
var versionOutputs = map[string]string{
	"import platform, frappe": "3.11.15\n15.124.0\n",
	"node --version":          "v18.20.8\n",
	"mariadb --version":       "mariadb  Ver 15.1 Distrib 10.11.19-MariaDB, for debian-linux-gnu (x86_64) using  EditLine wrapper\n",
}

// examples are the lines of the fenced code blocks in a context block.
func examples(block string) []string {
	var out []string
	fenced := false
	for _, line := range strings.Split(block, "\n") {
		switch {
		case strings.HasPrefix(line, "```"):
			fenced = !fenced
		case fenced:
			out = append(out, line)
		}
	}
	return out
}

func (r result) assertContains(t *testing.T, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(r.stdout, w) {
			t.Errorf("stdout lacks %q:\n%s", w, r.stdout)
		}
	}
}

func (r result) assertLacks(t *testing.T, unwanted ...string) {
	t.Helper()
	for _, u := range unwanted {
		if strings.Contains(r.stdout, u) {
			t.Errorf("stdout holds %q:\n%s", u, r.stdout)
		}
	}
}

func assertExample(t *testing.T, lines []string, want string) {
	t.Helper()
	if !slices.Contains(lines, want) {
		t.Errorf("examples = %q, want %q", lines, want)
	}
}

func TestOnARunningEnvironmentContextNamesTheVersionsFromTheContainersTheAppsPathEverySiteAndTheLogin(t *testing.T) {
	c := sandbox(t)
	dir := c.newIn(t, "erp-dev", "--app", acmeURL, "--site", "shop.localhost", "--site", "blog.localhost")
	c.docker.ExecStdout = versionOutputs
	t.Chdir(c.dir)

	r := c.run(t, "context", "-e", "erp-dev")

	r.assertCode(t, exitcode.CodeOK)
	if r.stderr != "" {
		t.Errorf("stderr = %q, want empty", r.stderr)
	}
	apps := filepath.Join(dir, "apps")
	r.assertContains(t, "# tamp environment erp-dev\n", "The environment is running.",
		"- Frappe 15.124.0, branch version-15, read from the frappe container\n",
		"- Python 3.11.15, read from the frappe container\n",
		"- Node 18.20.8, read from the frappe container\n",
		"- MariaDB 10.11.19, read from the mariadb container\n",
		"Edit app code on the host, in "+apps+":",
		"- acme: "+filepath.Join(apps, "acme")+", branch main\n",
		"- http://shop.localhost\n", "- http://blog.localhost\n",
		"`Administrator`, with the password `password`")
}

func TestEveryExampleNamesTheEnvironmentAndWorksAsPrintedWithoutATerminalFromAnyDirectory(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev", "--app", acmeURL, "--site", "shop.localhost")
	c.docker.ExecStdout = versionOutputs
	t.Chdir(c.dir)

	r := c.run(t, "context", "-e", "erp-dev")

	r.assertCode(t, exitcode.CodeOK)
	lines := examples(r.stdout)
	for _, want := range []string{
		"tamp shell -e erp-dev -- bench --site shop.localhost migrate",
		"tamp shell -e erp-dev -- bench build --app acme",
		"tamp shell -e erp-dev -- bench --site shop.localhost execute frappe.ping",
		"tamp site add -e erp-dev erp-dev-test.localhost",
		"tamp shell -e erp-dev -- env CI=1 bench --site erp-dev-test.localhost run-tests --module frappe.tests.test_formatter",
		"tamp status -e erp-dev",
		"tamp logs -e erp-dev --tail 100",
	} {
		assertExample(t, lines, want)
	}
	curl := regexp.MustCompile(`^curl(\.exe)? -sS --resolve shop\.localhost:80:127\.0\.0\.1 .*http://shop\.localhost/`)
	for _, line := range lines {
		if strings.Contains(line, "console") {
			t.Errorf("example %q runs console, which needs a terminal", line)
		}
		if curl.MatchString(line) {
			continue
		}
		args := strings.Fields(line)
		if i := slices.Index(args, "-e"); len(args) < 4 || args[0] != "tamp" || i < 0 || i+1 == len(args) || args[i+1] != "erp-dev" {
			t.Errorf("example %q is neither curl nor a tamp command with -e erp-dev", line)
			continue
		}
		if got := c.run(t, args[1:]...); got.code != exitcode.CodeOK {
			t.Errorf("example %q exits %d: %s", line, got.code, got.stderr)
		}
	}
	if !slices.ContainsFunc(lines, curl.MatchString) {
		t.Errorf("examples = %q, want the curl --resolve line", lines)
	}
	login := regexp.MustCompile(`-d usr=Administrator -d pwd=password http://shop\.localhost/api/method/login --next .*-b \S+ http://shop\.localhost/api/method/`)
	if !slices.ContainsFunc(lines, login.MatchString) {
		t.Errorf("examples = %q, want a curl that logs in, then calls a method", lines)
	}
}

func TestForAnERPNextRecipeTestsRunOneModuleOnlyOnTheTestSite(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev", "--app", "erpnext", "--site", "shop.localhost")

	r := c.run(t, "context")

	r.assertCode(t, exitcode.CodeOK)
	lines := examples(r.stdout)
	assertExample(t, lines, "tamp site add -e erp-dev erp-dev-test.localhost")
	for _, line := range lines {
		if (strings.Contains(line, "run-tests") || strings.Contains(line, "allow_tests")) && !strings.Contains(line, "--site erp-dev-test.localhost ") {
			t.Errorf("example %q runs tests, or allows them, on a site other than erp-dev-test.localhost", line)
		}
	}
	// Prose too: an agent copies a command from a sentence as readily.
	for _, line := range strings.Split(r.stdout, "\n") {
		if strings.Contains(line, "run-tests --app") {
			t.Errorf("line %q runs a whole app's suite", line)
		}
		if (strings.Contains(line, "run-tests") || strings.Contains(line, "allow_tests") || strings.Contains(line, "CI=1")) && !strings.Contains(line, "erp-dev-test.localhost") {
			t.Errorf("line %q mentions tests without naming the test site", line)
		}
	}
	if !slices.ContainsFunc(lines, func(l string) bool { return strings.Contains(l, "run-tests --module ") }) {
		t.Errorf("examples = %q, want run-tests of one module", lines)
	}
	if regexp.MustCompile(`shop\.localhost\S* .*allow_tests|allow_tests.*shop\.localhost`).MatchString(r.stdout) {
		t.Errorf("stdout suggests allow_tests on shop.localhost:\n%s", r.stdout)
	}
	r.assertContains(t, "Tests write to the site they run on", "never on a site with real work")
}

func TestARecipeThatListsTheTestSiteGetsNoSiteAddAndItsOtherSiteForTheWork(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev", "--site", "erp-dev-test.localhost", "--site", "shop.localhost")

	r := c.run(t, "context")

	lines := examples(r.stdout)
	assertExample(t, lines, "tamp shell -e erp-dev -- bench --site shop.localhost migrate")
	if slices.ContainsFunc(lines, func(l string) bool { return strings.HasPrefix(l, "tamp site add") }) {
		t.Errorf("examples = %q, want no site add: the recipe lists erp-dev-test.localhost", lines)
	}
}

func TestTheTestExampleRunsTheAppsFirstTestModuleInAModuleFolder(t *testing.T) {
	c := sandbox(t)
	dir := c.newIn(t, "erp-dev", "--app", acmeURL, "--site", "shop.localhost")
	pkg := filepath.Join(dir, "apps", "acme", "acme")
	// The root file sorts before zeta/, so only the root rule skips it.
	for _, f := range []string{filepath.Join(pkg, "test_root.py"), filepath.Join(pkg, "zeta", "test_api.py")} {
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte("import unittest\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	r := c.run(t, "context")

	assertExample(t, examples(r.stdout), "tamp shell -e erp-dev -- env CI=1 bench --site erp-dev-test.localhost run-tests --module acme.zeta.test_api")
}

func TestOnVersion16TheTestLineNeedsNoCI(t *testing.T) {
	c := sandbox(t)
	c.newEnvironment(t, "--frappe", "version-16", "--site", "shop.localhost")

	r := c.run(t, "context")

	assertExample(t, examples(r.stdout), "tamp shell -e erp-dev -- bench --site erp-dev-test.localhost run-tests --module frappe.tests.test_formatter")
	r.assertLacks(t, "CI=1")
}

func TestContextSaysNeverToRunBenchStartOrBenchUpdate(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev", "--site", "shop.localhost")

	r := c.run(t, "context")

	r.assertCode(t, exitcode.CodeOK)
	r.assertContains(t, "Never run `bench start`", "Never run `bench update`")
}

func TestOnAStoppedEnvironmentContextPrintsFromTheRecipeSaysItIsStoppedAndReadsNoContainer(t *testing.T) {
	c := sandbox(t)
	c.mutagen()
	c.newIn(t, "erp-dev", "--site", "shop.localhost")
	c.run(t, "stop").assertCode(t, exitcode.CodeOK)
	c.docker.ExecStdout = versionOutputs
	execs := len(c.docker.Execs)

	r := c.run(t, "context")

	r.assertCode(t, exitcode.CodeOK)
	if r.stderr != "" {
		t.Errorf("stderr = %q, want empty", r.stderr)
	}
	r.assertContains(t, "The environment is stopped", "`tamp start -e erp-dev`",
		"The `tamp shell` and `curl` lines need the environment running",
		"- Frappe branch version-15, from the recipe, because the frappe container is not running\n",
		"- Python 3.11, from the recipe", "- Node 18, from the recipe", "- MariaDB 10.11, from the recipe",
		"- http://shop.localhost\n")
	r.assertLacks(t, "within seconds")
	if got := c.docker.ExecLines()[execs:]; len(got) > 0 {
		t.Errorf("execs = %q, want none", got)
	}
}

func TestOnAPartlyRunningEnvironmentContextNamesTheStoppedContainers(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev", "--site", "shop.localhost")
	if err := c.docker.StopContainer(context.Background(), "erp-dev-mariadb-1"); err != nil {
		t.Fatal(err)
	}

	r := c.run(t, "context")

	r.assertCode(t, exitcode.CodeOK)
	r.assertContains(t, "The environment is partly running: erp-dev-mariadb-1 is stopped. Its sites can fail until all of it runs, so start the rest with `tamp start -e erp-dev`.")
	r.assertLacks(t, "sites do not answer")
}

func TestOnAnEnvironmentNeverBuiltContextPrintsFromTheRecipeAndSaysHowToBuildIt(t *testing.T) {
	c := sandbox(t)
	writeRecipe(t, c.dir, "frappe = \"version-16\"\n\n[[sites]]\n  host = \"shop.localhost\"\n")

	r := c.run(t, "context")

	r.assertCode(t, exitcode.CodeOK)
	r.assertContains(t, "The environment is not built", "`tamp start` in "+c.dir,
		"- Python 3.14, from the recipe", "- MariaDB 11.8, from the recipe")
	r.assertLacks(t, "works from any directory")
}

func TestInAMovedDirectoryContextSaysEFindsNoRecipeUntilStartRecreatesTheContainers(t *testing.T) {
	c := sandbox(t)
	old := c.newIn(t, filepath.Join("a", "erp-dev"), "--site", "shop.localhost")
	moved := filepath.Join(c.dir, "b", "erp-dev")
	// t.Chdir would hold the directory open, and Windows renames no open one.
	if err := os.Chdir(c.dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(c.dir, "a"), filepath.Join(c.dir, "b")); err != nil {
		t.Fatal(err)
	}
	t.Chdir(moved)

	r := c.run(t, "context")

	r.assertCode(t, exitcode.CodeOK)
	if r.stderr != "" {
		t.Errorf("stderr = %q, want empty", r.stderr)
	}
	r.assertContains(t, "The environment moved here from "+old, "`-e erp-dev` cannot find its recipe", "Run `tamp start` in "+moved)
	r.assertLacks(t, "works from any directory")
}

func TestAVersionTheBenchCannotReportComesFromTheRecipeAndSaysWhy(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev", "--site", "shop.localhost")
	c.docker.ExecStdout = versionOutputs
	c.docker.ExecFails = map[string]error{"import platform, frappe": errors.New("no module named frappe")}

	r := c.run(t, "context")

	r.assertCode(t, exitcode.CodeOK)
	r.assertContains(t, "- Frappe branch version-15, from the recipe, because the bench cannot report it yet\n",
		"- Python 3.11, from the recipe, because the bench cannot report it yet\n",
		"- Node 18.20.8, read from the frappe container\n")
}

func TestAContainerThatNeverAnswersAVersionReadTimesOutToTheRecipe(t *testing.T) {
	saved := env.ReadTimeout
	t.Cleanup(func() { env.ReadTimeout = saved })
	env.ReadTimeout = 50 * time.Millisecond
	c := sandbox(t)
	c.newIn(t, "erp-dev", "--site", "shop.localhost")
	c.docker.ExecStdout = versionOutputs
	c.docker.ExecHangs = []string{"mariadb --version"}

	r := c.run(t, "context")

	r.assertCode(t, exitcode.CodeOK)
	r.assertContains(t, "- MariaDB 10.11, from the recipe, because the mariadb container did not answer within 50ms\n",
		"- Node 18.20.8, read from the frappe container\n")
}

func TestContextChangesNothing(t *testing.T) {
	c := sandbox(t)
	sc := c.mutagen()
	dir := c.newIn(t, "erp-dev", "--site", "shop.localhost")
	c.docker.ExecStdout = versionOutputs
	recipe := readFile(t, filepath.Join(dir, "tamp.toml"))
	calls, execs, syncs := len(c.docker.Calls), len(c.docker.Execs), len(sc.Calls)

	c.run(t, "context").assertCode(t, exitcode.CodeOK)

	for _, call := range c.callsSince(calls) {
		if call != "Exec" {
			assertOnlyReads(t, []string{call})
		}
	}
	want := []string{"/home/frappe/frappe-bench/env/bin/python -c import platform, frappe; print(platform.python_version()); print(frappe.__version__)",
		"node --version", "mariadb --version"}
	got := slices.Sorted(slices.Values(c.docker.ExecLines()[execs:]))
	if slices.Sort(want); !slices.Equal(got, want) {
		t.Errorf("execs = %q, want only the version reads %q", got, want)
	}
	if got := sc.Calls[syncs:]; len(got) > 0 {
		t.Errorf("Mutagen calls = %q, want none", got)
	}
	if got := readFile(t, filepath.Join(dir, "tamp.toml")); got != recipe {
		t.Errorf("the recipe changed:\n%s", got)
	}
}

func TestContextHelpRecommendsOneLineInTheAgentsInstructionFile(t *testing.T) {
	c := sandbox(t)

	r := c.run(t, "context", "--help")

	r.assertCode(t, exitcode.CodeOK)
	r.assertContains(t, "Run `tamp context` at the start of each session")
}

func TestEachEnvironmentGetsATestSiteOfItsOwnSoTheSecondsTestLinesWork(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev", "--site", "shop.localhost")
	for _, line := range examples(c.run(t, "context").stdout) {
		if strings.HasPrefix(line, "tamp site add ") {
			c.run(t, strings.Fields(line)[1:]...).assertCode(t, exitcode.CodeOK)
		}
	}
	c.newIn(t, "crm-dev", "--site", "crm.localhost")
	t.Chdir(c.dir)

	r := c.run(t, "context", "-e", "crm-dev")

	r.assertCode(t, exitcode.CodeOK)
	tests := 0
	for _, line := range examples(r.stdout) {
		if !strings.HasPrefix(line, "tamp site add ") && !strings.Contains(line, "allow_tests") && !strings.Contains(line, "run-tests") {
			continue
		}
		tests++
		if got := c.run(t, strings.Fields(line)[1:]...); got.code != exitcode.CodeOK {
			t.Errorf("example %q exits %d: %s", line, got.code, got.stderr)
		}
	}
	if tests != 3 {
		t.Errorf("examples = %q, want the site add, allow_tests and run-tests lines", examples(r.stdout))
	}
}

func TestTheTestSiteIsAValidHostnameForAnEnvironmentNameWithUnderscoresAndATrailingHyphen(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp_dev-", "--site", "shop.localhost")

	r := c.run(t, "context")

	add := ""
	for _, line := range examples(r.stdout) {
		if strings.HasPrefix(line, "tamp site add ") {
			add = line
		}
	}
	if add != "tamp site add -e erp_dev- erp-dev--test.localhost" {
		t.Fatalf("examples = %q, want the site add of erp-dev--test.localhost", examples(r.stdout))
	}
	c.run(t, strings.Fields(add)[1:]...).assertCode(t, exitcode.CodeOK)
}

func TestWhenTheTestSiteIsTheOnlySiteContextAsksForASiteForTheWork(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev", "--site", "erp-dev-test.localhost")

	r := c.run(t, "context")

	r.assertContains(t, "the only site of the recipe, erp-dev-test.localhost, is for tests: add one with `tamp site add -e erp-dev <host>`")
	r.assertLacks(t, "The site commands need a site: add one")
}

func TestHungContainersCostOneReadTimeoutNotOneEachRead(t *testing.T) {
	saved := env.ReadTimeout
	t.Cleanup(func() { env.ReadTimeout = saved })
	env.ReadTimeout = 400 * time.Millisecond
	c := sandbox(t)
	c.newIn(t, "erp-dev", "--site", "shop.localhost")
	c.docker.ExecHangs = []string{"import platform, frappe", "node --version", "mariadb --version"}

	start := time.Now()
	r := c.run(t, "context")

	r.assertCode(t, exitcode.CodeOK)
	if took := time.Since(start); took > 2*env.ReadTimeout {
		t.Errorf("context took %s with three hung reads, want about one ReadTimeout of %s", took, env.ReadTimeout)
	}
}

func TestALongNamesTestSiteLabelFitsIn63(t *testing.T) {
	c := sandbox(t)
	name := strings.Repeat("a", 59)
	c.newIn(t, name, "--site", "shop.localhost")

	r := c.run(t, "context")

	add := ""
	for _, line := range examples(r.stdout) {
		if strings.HasPrefix(line, "tamp site add ") {
			add = line
		}
	}
	host := strings.Repeat("a", 58) + "-test.localhost"
	if add != "tamp site add -e "+name+" "+host {
		t.Fatalf("examples = %q, want the site add of %s", examples(r.stdout), host)
	}
	c.run(t, strings.Fields(add)[1:]...).assertCode(t, exitcode.CodeOK)
}

func TestWithNoAppTheTestsSectionSaysToAddAnAppAndNeverToEditFrappe(t *testing.T) {
	c := sandbox(t)
	dir := c.newIn(t, "erp-dev", "--site", "shop.localhost")

	r := c.run(t, "context")

	r.assertContains(t, "add an app with `tamp app add -e erp-dev <url|name>[#branch]`")
	r.assertLacks(t, filepath.Join(dir, "apps", "frappe", "frappe", "<module>"), "`frappe.<module>", "A whole-app run")
}

func TestTheLoggedInCurlNamesACookieFileThatIsNotMeantToExistAndShowsQueryArguments(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev", "--site", "shop.localhost")

	r := c.run(t, "context")

	r.assertContains(t, "-b no-such-cookie-file ",
		"'http://shop.localhost/api/method/frappe.client.get_count?doctype=User'", "Quote each URL that holds one")
}

func TestWithTheRecipesAppsNotClonedTheTestsSectionSaysToStartAndNeverToAddThem(t *testing.T) {
	c := sandbox(t)
	dir := c.newIn(t, "erp-dev", "--site", "shop.localhost")
	recipe := filepath.Join(dir, "tamp.toml")
	body := readFile(t, recipe) + "\n[[apps]]\n  url = \"" + acmeURL + "\"\n"
	if err := os.WriteFile(recipe, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	r := c.run(t, "context")

	r.assertCode(t, exitcode.CodeOK)
	r.assertContains(t, "The recipe's apps are not cloned yet", "run `tamp start -e erp-dev`, which clones them")
	r.assertLacks(t, "tamp app add", "The recipe lists no app", "`frappe.<module>", "A whole-app run",
		filepath.Join(dir, "apps", "frappe", "frappe", "<module>"))
}

func TestATeammatesFreshCloneOfARecipeWithANamedAppSaysToStartNotThatTheAppHasNoTests(t *testing.T) {
	c := sandbox(t)
	dir := c.newIn(t, "erp-dev", "--app", acmeURL, "--site", "shop.localhost")
	mate := filepath.Join(c.dir, "crm-dev")
	if err := os.MkdirAll(mate, 0o755); err != nil {
		t.Fatal(err)
	}
	recipe := readFile(t, filepath.Join(dir, "tamp.toml"))
	if !strings.Contains(recipe, `name = "acme"`) {
		t.Fatalf("recipe = %q, want it to name acme", recipe)
	}
	if err := os.WriteFile(filepath.Join(mate, "tamp.toml"), []byte(recipe), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(mate)

	r := c.run(t, "context")

	r.assertCode(t, exitcode.CodeOK)
	r.assertContains(t, "- acme: "+filepath.Join(mate, "apps", "acme")+", not cloned yet\n",
		"The recipe's apps are not cloned yet", "which clones them")
	r.assertLacks(t, "acme has no test module yet", "bench build --app acme", "`acme.<module>")
}

func TestContextOfAPausedEnvironmentOpensWithTheUnpauseFix(t *testing.T) {
	c := sandbox(t)
	c.newEnvironment(t, "--frappe", "version-15", "--site", "shop.localhost")
	c.docker.Pause(frappeContainer)

	r := c.run(t, "context")

	r.assertCode(t, exitcode.CodeOK)
	_, rest, _ := strings.Cut(r.stdout, "\n\n")
	first, _, _ := strings.Cut(rest, "\n")
	if !strings.Contains(first, "erp-dev-frappe-1 is paused") || !strings.Contains(first, "`docker unpause erp-dev-frappe-1`") {
		t.Errorf("the line under the heading = %q, want the paused container and its unpause fix", first)
	}
	r.assertLacks(t, "The environment is running.")
}

func TestTheQueryArgumentExampleLogsInFirstInTheSameCurl(t *testing.T) {
	c := sandbox(t)
	c.newIn(t, "erp-dev", "--site", "shop.localhost")

	r := c.run(t, "context")

	for _, line := range examples(r.stdout) {
		if strings.Contains(line, "get_count") {
			if !strings.Contains(line, "/api/method/login --next ") {
				t.Errorf("the get_count example %q does not log in first", line)
			}
			return
		}
	}
	t.Errorf("no example calls get_count:\n%s", r.stdout)
}

func TestContextOfAPausedEnvironmentSaysTheVersionsComeFromTheRecipeBecauseItIsPaused(t *testing.T) {
	c := sandbox(t)
	c.newEnvironment(t, "--frappe", "version-15", "--site", "shop.localhost")
	c.docker.Pause(frappeContainer)

	r := c.run(t, "context")

	r.assertContains(t, "- Python 3.11, from the recipe, because the frappe container is paused\n")
}

func TestTheTestLineSkipsBeforeTestsOnlyWhenAHookCallsAnAppTheRecipeLacks(t *testing.T) {
	for _, tc := range []struct {
		hook string
		skip bool
	}{
		{`before_tests = "erpnext.setup.utils.before_tests"`, true},
		{`before_tests = ["acme.setup.before_tests", "erpnext.setup.utils.before_tests"]`, true},
		{"before_tests = [\n\t\"acme.setup.before_tests\",\n\t\"erpnext.setup.utils.before_tests\",\n]", true},
		{`before_tests = "acme.setup.before_tests"`, false},
		{`before_tests = "frappe.utils.install.before_tests"`, false},
		{``, false},
	} {
		t.Run(tc.hook, func(t *testing.T) {
			c := sandbox(t)
			dir := c.newIn(t, "erp-dev", "--app", acmeURL, "--site", "shop.localhost")
			hooks := filepath.Join(dir, "apps", "acme", "acme", "hooks.py")
			if err := os.WriteFile(hooks, []byte("app_name = \"acme\"\n"+tc.hook+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			r := c.run(t, "context")

			var line string
			for _, l := range examples(r.stdout) {
				if strings.Contains(l, "run-tests") {
					line = l
				}
			}
			if got := strings.Contains(line, " run-tests --skip-before-tests --module "); got != tc.skip {
				t.Errorf("test line = %q, want --skip-before-tests %v", line, tc.skip)
			}
			if tc.skip {
				r.assertContains(t, "acme's `before_tests` hook calls `erpnext.setup.utils.before_tests` of erpnext, an app that the recipe does not list")
			}
		})
	}
}

// testLine is the run-tests example of a context block.
func testLine(stdout string) string {
	for _, l := range examples(stdout) {
		if strings.Contains(l, "run-tests") {
			return l
		}
	}
	return ""
}

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOnVersion16AndDevelopTheTestLineSkipsBeforeTestsOnlyForTheHooksOfTheModulesApp(t *testing.T) {
	const missing = "app_name = \"acme\"\nbefore_tests = \"erpnext.setup.utils.before_tests\"\n"
	const test = "import unittest\n\n\nclass TestX(unittest.TestCase):\n\tdef test_x(self):\n\t\tpass\n"
	for _, branch := range []string{"version-16", "develop"} {
		for _, tc := range []struct {
			name   string
			files  map[string]string
			module string
			skip   bool
		}{
			{"a Frappe module runs only Frappe's hooks", map[string]string{"hooks.py": missing}, "frappe.tests.test_formatter", false},
			{"an acme module runs acme's hooks", map[string]string{"hooks.py": missing, "core/test_x.py": test}, "acme.core.test_x", true},
			{"acme's own hook passes", map[string]string{"hooks.py": "before_tests = \"acme.setup.before_tests\"\n", "core/test_x.py": test}, "acme.core.test_x", false},
		} {
			t.Run(branch+": "+tc.name, func(t *testing.T) {
				c := sandbox(t)
				c.run(t, "new", "erp-dev", "--frappe", branch, "--app", acmeURL, "--site", "shop.localhost").assertCode(t, exitcode.CodeOK)
				dir := filepath.Join(c.dir, "erp-dev")
				t.Chdir(dir)
				writeFiles(t, filepath.Join(dir, "apps", "acme", "acme"), tc.files)

				r := c.run(t, "context")

				line := testLine(r.stdout)
				if !strings.HasSuffix(line, " --module "+tc.module) {
					t.Errorf("test line = %q, want --module %s", line, tc.module)
				}
				if got := strings.Contains(line, " --skip-before-tests "); got != tc.skip {
					t.Errorf("test line = %q, want --skip-before-tests %v", line, tc.skip)
				}
				if tc.skip {
					r.assertContains(t, "skips every `before_tests` hook, Frappe's included")
				}
			})
		}
	}
}

func TestTheExampleModuleIsTheFirstThatDefinesATest(t *testing.T) {
	c := sandbox(t)
	dir := c.newIn(t, "erp-dev", "--app", acmeURL, "--site", "shop.localhost")
	pkg := filepath.Join(dir, "apps", "acme", "acme")
	writeFiles(t, pkg, map[string]string{
		"a_mod/test_empty.py": "import unittest\n\n\nclass TestEmpty(unittest.TestCase):\n\tpass\n",
		"b_mod/test_real.py":  "import unittest\n\n\nclass TestReal(unittest.TestCase):\n\tdef test_one(self):\n\t\tpass\n",
	})

	r := c.run(t, "context")

	if line := testLine(r.stdout); !strings.HasSuffix(line, " --module acme.b_mod.test_real") {
		t.Errorf("test line = %q, want the module that defines a test", line)
	}
	r.assertLacks(t, "runs 0 tests")

	if err := os.Remove(filepath.Join(pkg, "b_mod", "test_real.py")); err != nil {
		t.Fatal(err)
	}
	r = c.run(t, "context")

	if line := testLine(r.stdout); !strings.HasSuffix(line, " --module acme.a_mod.test_empty") {
		t.Errorf("test line = %q, want the only test module", line)
	}
	r.assertContains(t, "None of acme's test modules that load in this bench defines a test yet, so the example runs 0 tests until you add one.")
}

func TestTheExampleModuleSkipsATestModuleThatImportsAnAppTheBenchLacks(t *testing.T) {
	c := sandbox(t)
	dir := c.newIn(t, "erp-dev", "--app", acmeURL, "--site", "shop.localhost")
	pkg := filepath.Join(dir, "apps", "acme", "acme")
	writeFiles(t, pkg, map[string]string{
		"a_mod/test_erp.py":  "import unittest\n\nfrom erpnext.accounts import x\n\n\nclass TestErp(unittest.TestCase):\n\tdef test_one(self):\n\t\tpass\n",
		"b_mod/test_real.py": "import json, unittest\n\nimport frappe\nfrom acme.b_mod import y\n\n\nclass TestReal(unittest.TestCase):\n\tdef test_one(self):\n\t\tpass\n",
	})

	r := c.run(t, "context")

	if line := testLine(r.stdout); !strings.HasSuffix(line, " --module acme.b_mod.test_real") {
		t.Errorf("test line = %q, want the module that loads in the bench", line)
	}

	if err := os.Remove(filepath.Join(pkg, "b_mod", "test_real.py")); err != nil {
		t.Fatal(err)
	}
	r = c.run(t, "context")

	if line := testLine(r.stdout); !strings.HasSuffix(line, " --module frappe.tests.test_formatter") {
		t.Errorf("test line = %q, want Frappe's", line)
	}
	r.assertContains(t, "Each test module of acme imports a package beyond Frappe, the recipe's apps, and the standard library")
}
