package env

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/zhide915/tamp/internal/docker"
	"github.com/zhide915/tamp/internal/frappe"
)

// testSite is where the block runs tests, one per environment. A hostname
// label holds no `_` and at most 63 characters.
func testSite(name string) string {
	label := strings.ReplaceAll(name, "_", "-")
	return label[:min(len(label), 58)] + "-test.localhost"
}

// ReadTimeout bounds each version read, so a hung container never hangs
// `tamp context`. Tests shorten it.
var ReadTimeout = 5 * time.Second

// Context prints, on stdout, a markdown block that tells an AI coding agent
// how to work in the environment. It reads the recipe and the containers,
// and changes nothing: a stopped environment is described from its recipe.
func (m *Manager) Context(envFlag string) error {
	ctx := context.Background()
	t, err := m.find(ctx, envFlag)
	if err != nil {
		return err
	}
	// As locate, but a move is told on stdout, in the block.
	moved := ""
	if t.flag == "" {
		if moved, err = m.owner(ctx, t); err != nil {
			return err
		}
	}
	r, err := readRecipe(t.dir)
	if err != nil {
		return err
	}
	containers, err := m.envContainers(ctx, t.name)
	if err != nil {
		return err
	}
	up, frozen := map[string]bool{}, map[string]bool{}
	var down []string
	for _, c := range containers {
		up[c.Name], frozen[c.Name] = c.Running, c.Paused
		if !c.Running {
			down = append(down, c.Name)
		}
	}
	slices.Sort(down)
	st := state(t.name, containers)
	byE := target{name: t.name, dir: t.dir, flag: t.name}
	here := target{name: t.name, dir: t.dir}
	e := "-e " + t.name
	var b strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }

	line("# tamp environment %s", t.name)
	line("")
	reachable := moved == "" && st != stateNotBuilt
	switch {
	case moved != "":
		line("The environment moved here from %s. Its containers still name that directory, so `%s` cannot find its recipe. Run %s in %s, which recreates them for this directory.", moved, e, here.command("start"), t.dir)
	case st == stateNotBuilt:
		line("The environment is not built, so nothing below works yet, and `%s` cannot find it. Build it with %s in %s.", e, here.command("start"), t.dir)
	case st == stateRunning:
		line("The environment is running.")
	case st == statePaused:
		msg, fix := pausedFix(containers)
		line("The environment is paused: %s. Before anything below, %s.", msg, fix)
	case st == stateStopped:
		line("The environment is stopped, so its sites do not answer. Start it with %s.", byE.command("start"))
	default:
		var parts []string
		for _, p := range []struct {
			names []string
			what  string
		}{{down, "stopped"}, {missingContainers(t.name, containers), "missing"}} {
			switch len(p.names) {
			case 0:
			case 1:
				parts = append(parts, p.names[0]+" is "+p.what)
			default:
				parts = append(parts, strings.Join(p.names, ", ")+" are "+p.what)
			}
		}
		line("The environment is partly running: %s. Its sites can fail until all of it runs, so start the rest with %s.", strings.Join(parts, ", and "), byE.command("start"))
	}
	recipe := "Its recipe is " + filepath.Join(t.dir, RecipeFile) + "."
	switch {
	case reachable && st == stateRunning:
		recipe += " Every command below works from any directory."
	case reachable:
		recipe += " Every command below works from any directory. The `tamp shell` and `curl` lines need the environment running."
	}
	line("%s", recipe)

	line("")
	line("## Versions")
	line("")
	for _, v := range m.versions(ctx, t.name, r, up, frozen) {
		line("- %s", v)
	}

	apps := filepath.Join(t.dir, AppsDir)
	line("")
	line("## Code")
	line("")
	line("Edit app code on the host, in %s:", apps)
	line("")
	line("- frappe: %s, branch %s", filepath.Join(apps, "frappe"), r.Frappe)
	for _, a := range r.Apps {
		switch {
		case a.Name == "":
			line("- %s: not cloned yet", a.URL)
		case !isDir(filepath.Join(apps, a.Name)):
			line("- %s: %s, not cloned yet", a.Name, filepath.Join(apps, a.Name))
		case a.Branch == "":
			line("- %s: %s", a.Name, filepath.Join(apps, a.Name))
		default:
			line("- %s: %s, branch %s", a.Name, filepath.Join(apps, a.Name), a.Branch)
		}
	}
	line("")
	if m.sync != nil {
		line("While the environment runs, Mutagen syncs %s with the frappe container both ways, and the host wins each conflict.", apps)
		line("Leave `node_modules/` to yarn, which writes it inside the container, where it does not sync. Never edit it.")
	} else {
		line("%s is bind-mounted into the frappe container.", apps)
		line("Leave `node_modules/` to yarn, which writes it inside the container. Never edit it.")
	}
	line("The web server reloads each Python edit by itself.")
	line("Developer mode is on for every site, so a DocType that you change in the desk is written into its app's folder.")
	line("Run git on the host, in the app's folder: the user's git credentials are on the host, and git in the container has none of them.")

	testHost := testSite(t.name)
	var work string
	hasTestSite := false
	for _, s := range r.Sites {
		if s.Host == testHost {
			hasTestSite = true
		} else if work == "" {
			work = s.Host
		}
	}
	app := "frappe"
	// A teammate's fresh clone of the recipe names apps that apps/ does not hold yet.
	for _, a := range slices.Backward(r.Apps) {
		if a.Name != "" && isDir(filepath.Join(apps, a.Name)) {
			app = a.Name
			break
		}
	}
	shell := "tamp shell " + e + " -- "
	line("")
	line("## Bench commands")
	line("")
	line("Run bench through `tamp shell`, with the command after `--`. It runs as the bench user in the bench directory, needs no terminal, and exits with the exit code of the command:")
	line("")
	line("```sh")
	if work != "" {
		line("%sbench --site %s migrate", shell, work)
	}
	line("%sbench build --app %s", shell, app)
	if work != "" {
		line("%sbench --site %s execute frappe.ping", shell, work)
		line("%sbench --site %s execute frappe.utils.add_days --kwargs \"{'date':'2026-01-01','days':1}\"", shell, work)
	}
	line("```")
	line("")
	if work != "" {
		line("- `execute` calls a dotted path, takes its arguments as Python literals in `--args` and `--kwargs`, and commits what the call writes.")
	} else if hasTestSite {
		line("- The site commands need a site for your work, and the only site of the recipe, %s, is for tests: add one with `tamp site add %s <host>`.", testHost, e)
	} else {
		line("- The site commands need a site: add one with `tamp site add %s <host>`.", e)
	}
	line("- `bench console`, and `tamp shell` without a command, need a terminal: use `execute` instead.")
	line("- Read the bench processes' output with `tamp logs %s --tail 100`. The frappe container already runs the web server, socket.io, the watcher, the scheduler, and a worker. Never run `bench start`, which starts a second copy.", e)
	line("- To update an app, pull with git on the host, then run `migrate`. Never run `bench update`, which pulls inside the container, where git has no credentials.")

	module, mod := m.testModule(apps, app, r)
	// A before_tests hook that calls an app the environment lacks fails the
	// run. version-15 runs the hooks of every installed app, and version-16 and
	// develop only those of the module's app, for its integration tests.
	hooksOf := r.names()
	if r.Frappe != "version-15" {
		hooksOf = []string{strings.Split(module, ".")[0]}
	}
	skips := missingBeforeTests(apps, r, hooksOf)
	runTests := "bench --site " + testHost + " run-tests"
	if len(skips) > 0 {
		runTests += " --skip-before-tests"
	}
	runTests += " --module " + module
	if r.Frappe == "version-15" {
		runTests = "env CI=1 " + runTests
	}
	line("")
	line("## Tests")
	line("")
	line("Tests write to the site they run on: they make test records, and an app's `before_tests` hook can change the site, as ERPNext's completes the setup wizard. Run tests only on %s, the site for tests, and never on a site with real work:", testHost)
	line("")
	line("```sh")
	if !hasTestSite {
		line("tamp site add %s %s", e, testHost)
	}
	line("%sbench --site %s set-config allow_tests true", shell, testHost)
	line("%s%s", shell, runTests)
	line("```")
	line("")
	line("- Set `allow_tests` only on %s: it is the guard that keeps `run-tests` off every other site.", testHost)
	if r.Frappe == "version-15" {
		line("- On version-15, `run-tests` exits non-zero on a failed test only with `CI=1`. `CI=1` also skips the `allow_tests` check, so give it only %s.", testHost)
	}
	line("- Run one module, never a whole app: an app's suite is long.")
	if len(skips) > 0 {
		line("- `--skip-before-tests` skips every `before_tests` hook, Frappe's included. A test that needs the setup of a hook fails without it.")
	}
	for _, s := range skips {
		line("- %s's `before_tests` hook calls `%s` of %s, an app that the recipe does not list, so a run that calls the hook fails without `--skip-before-tests`.", s.app, s.hook, s.needs)
	}
	// Frappe's own code is never the agent's to change, so no line points into it.
	switch {
	case len(r.Apps) == 0:
		line("- The recipe lists no app, so the example runs one of Frappe's tests. Never change Frappe's own code: before you write a test, add an app with `tamp app add %s <url|name>[#branch]`, and put the test in its package.", e)
	case app == "frappe":
		start := byE.command("start")
		if !reachable {
			start = here.command("start") + " in " + t.dir
		}
		line("- The recipe's apps are not cloned yet, so the example runs one of Frappe's tests. Never change Frappe's own code: run %s, which clones them, and put the test in an app's package.", start)
	default:
		line("- The example names a test module that tamp found. Put the module you work on in its place: `--module` takes its dotted path, such as `%s.<module>.test_<name>`.", app)
		switch mod {
		case moduleNone:
			line("- %s has no test module yet, so the example runs one of Frappe's tests.", app)
		case moduleUnsafe:
			line("- Each test module of %s imports a package beyond Frappe, the recipe's apps, and the standard library, which the bench may lack, so the example runs one of Frappe's tests.", app)
		case moduleEmpty:
			line("- None of %s's test modules that load in this bench defines a test yet, so the example runs 0 tests until you add one.", app)
		}
		line("- Put an app's tests in a module folder of its package, such as %s, not at the package's root.", filepath.Join(apps, app, app, "<module>", "test_<name>.py"))
		if r.Frappe == "version-15" {
			line("  A run of the whole app imports a `test_*.py` at the root as the package itself, and runs none of its tests.")
		}
	}

	line("")
	line("## Sites")
	line("")
	if len(r.Sites) == 0 {
		line("The recipe lists no site. Add one with `tamp site add %s <host>`.", e)
	} else {
		line("Log in to each as `Administrator`, with the password `%s`:", frappe.AdminPassword)
		line("")
		for _, s := range r.Sites {
			line("- http://%s", s.Host)
		}
		curl := "curl"
		if runtime.GOOS == "windows" {
			// PowerShell's curl is an alias of Invoke-WebRequest.
			curl = "curl.exe"
		}
		host := r.Sites[0].Host
		if work != "" {
			host = work
		}
		line("")
		line("Outside a browser, `*.localhost` might not resolve. Point command-line tools at the router with `--resolve`:")
		line("")
		line("```sh")
		line("%s -sS --resolve %s:80:127.0.0.1 http://%s/api/method/ping", curl, host, host)
		line("```")
		line("")
		line("For a method that needs a login, give one curl two URLs: the login, then the call after `--next`. `-b` with a file that does not exist turns on curl's cookie engine, which carries the login's cookie to the call, and writes no file:")
		line("")
		resolve := fmt.Sprintf("--resolve %s:80:127.0.0.1 -b no-such-cookie-file", host)
		line("```sh")
		line("%s -sS %s -d usr=Administrator -d pwd=%s http://%s/api/method/login --next %s http://%s/api/method/frappe.auth.get_logged_user",
			curl, resolve, frappe.AdminPassword, host, resolve, host)
		line("```")
		line("")
		line("A whitelisted method takes its arguments in the query string. Quote each URL that holds one: zsh, for example, fails on an unquoted `?`:")
		line("")
		line("```sh")
		line("%s -sS %s -d usr=Administrator -d pwd=%s http://%s/api/method/login --next %s 'http://%s/api/method/frappe.client.get_count?doctype=User'",
			curl, resolve, frappe.AdminPassword, host, resolve, host)
		line("```")
	}

	line("")
	line("## When something fails")
	line("")
	line("```sh")
	line("tamp status %s", e)
	line("tamp logs %s --tail 100", e)
	line("tamp logs %s mariadb --tail 100", e)
	line("tamp doctor %s", e)
	line("```")
	line("")
	line("`tamp logs` reads one of: %s. It reads frappe by default.", strings.Join(services, ", "))

	m.p.Print(strings.TrimSuffix(b.String(), "\n"))
	return nil
}

// moduleState is how a test example stands for the app it names.
type moduleState int

const (
	// moduleRuns defines a test and imports only what the bench holds.
	moduleRuns moduleState = iota
	// moduleEmpty imports only what the bench holds, and defines no test.
	moduleEmpty
	// moduleNone: the app has no test module, so the example is Frappe's.
	moduleNone
	// moduleUnsafe: each test module of the app imports a package that tamp
	// cannot vouch for, so the example is Frappe's.
	moduleUnsafe
)

// testModule is the dotted name of a test module of app's clone in apps, in
// walk order: the first that defines a test and imports only Frappe, the
// recipe's apps, and the standard library, or else the first that imports
// only those. A module that imports an app the bench lacks fails to load.
// Otherwise it is one of Frappe's, which runs in a second. Frappe's own
// suite is too long to suggest.
func (m *Manager) testModule(apps, app string, r Recipe) (string, moduleState) {
	const fallback = "frappe.tests.test_formatter"
	if app == "frappe" {
		return fallback, moduleNone
	}
	known := append([]string{"frappe"}, r.names()...)
	pkg := filepath.Join(apps, app, app)
	found, empty, runs := false, "", ""
	// Frappe's discovery skips these folders too.
	skip := []string{"node_modules", "public", "locals", "__pycache__"}
	_ = filepath.WalkDir(pkg, func(path string, d fs.DirEntry, err error) error {
		switch {
		case err != nil || runs != "":
			return fs.SkipAll
		case d.IsDir() && (slices.Contains(skip, d.Name()) || strings.HasPrefix(d.Name(), ".")):
			return fs.SkipDir
		case d.IsDir() || filepath.Dir(path) == pkg:
			return nil
		}
		name := d.Name()
		if !strings.HasPrefix(name, "test_") || !strings.HasSuffix(name, ".py") {
			return nil
		}
		rel, err := filepath.Rel(filepath.Join(apps, app), strings.TrimSuffix(path, ".py"))
		body, readErr := os.ReadFile(path)
		if err != nil || readErr != nil {
			return nil
		}
		found = true
		if !importsOnly(body, known) {
			return nil
		}
		dotted := strings.ReplaceAll(filepath.ToSlash(rel), "/", ".")
		switch {
		case testFunc.Match(body):
			runs = dotted
		case empty == "":
			empty = dotted
		}
		return nil
	})
	switch {
	case runs != "":
		return runs, moduleRuns
	case empty != "":
		return empty, moduleEmpty
	case found:
		return fallback, moduleUnsafe
	}
	return fallback, moduleNone
}

var importLine = regexp.MustCompile(`(?m)^\s*(?:from\s+(\w+)\S*\s+import|import\s+([\w., ]+))`)

// importsOnly reports whether a Python module imports only the packages in
// known and the standard library's that tests use.
func importsOnly(body []byte, known []string) bool {
	for _, m := range importLine.FindAllSubmatch(body, -1) {
		names := []string{string(m[1])}
		if len(m[1]) == 0 {
			names = strings.Split(string(m[2]), ",")
		}
		for _, n := range names {
			top, _, _ := strings.Cut(strings.Fields(n + " ")[0], ".")
			if !slices.Contains(known, top) && !slices.Contains(stdlib, top) {
				return false
			}
		}
	}
	return true
}

// stdlib is the standard library's packages that tests commonly import.
var stdlib = []string{
	"__future__", "abc", "base64", "collections", "contextlib", "copy", "csv", "dataclasses", "datetime",
	"decimal", "email", "enum", "functools", "hashlib", "hmac", "html", "http", "inspect", "io", "itertools",
	"json", "logging", "math", "os", "pathlib", "random", "re", "shutil", "string", "sys", "tempfile",
	"textwrap", "time", "types", "typing", "unittest", "urllib", "uuid", "warnings", "zoneinfo",
}

var testFunc = regexp.MustCompile(`(?m)^\s+def test\w*\(`)

var (
	nodeVersion    = regexp.MustCompile(`^v(\d+\.\d+\.\d+)`)
	mariaDBVersion = regexp.MustCompile(`(\d+\.\d+\.\d+)-MariaDB`)
)

// versions reads Frappe, Python, Node and MariaDB from the running
// containers, and gives the recipe's for each it cannot read, saying why.
func (m *Manager) versions(ctx context.Context, name string, r Recipe, up, frozen map[string]bool) []string {
	tc := matrix[r.Frappe]
	fromRecipe := func(what, version, why string) string {
		return fmt.Sprintf("%s %s, from the recipe, because %s", what, version, why)
	}
	frappeBranch := "branch " + r.Frappe
	fc, dc := frappeContainer(name), serviceContainer(name, dbService)
	down := func(service, container string) string {
		if frozen[container] {
			return "the " + service + " container is paused"
		}
		return "the " + service + " container is not running"
	}
	out := []string{
		fromRecipe("Frappe", frappeBranch, down("frappe", fc)),
		fromRecipe("Python", tc.Python, down("frappe", fc)),
		fromRecipe("Node", tc.Node, down("frappe", fc)),
		fromRecipe("MariaDB", tc.MariaDB, down("mariadb", dc)),
	}
	// Concurrent, so a hung container costs one ReadTimeout, not three.
	var wg sync.WaitGroup
	if up[fc] {
		wg.Go(func() {
			words, err := m.read(ctx, fc, frappe.Versions())
			switch {
			case err == nil && len(words) == 2:
				out[0] = fmt.Sprintf("Frappe %s, %s, read from the frappe container", words[1], frappeBranch)
				out[1] = fmt.Sprintf("Python %s, read from the frappe container", words[0])
			default:
				why := "the bench cannot report it yet"
				if errors.Is(err, context.DeadlineExceeded) {
					why = fmt.Sprintf("the frappe container did not answer within %s", ReadTimeout)
				}
				out[0] = fromRecipe("Frappe", frappeBranch, why)
				out[1] = fromRecipe("Python", tc.Python, why)
			}
		})
		wg.Go(func() {
			out[2] = m.version(ctx, fc, "frappe", []string{"node", "--version"}, nodeVersion, "Node", tc.Node)
		})
	}
	if up[dc] {
		wg.Go(func() {
			out[3] = m.version(ctx, dc, dbService, []string{"mariadb", "--version"}, mariaDBVersion, "MariaDB", tc.MariaDB)
		})
	}
	wg.Wait()
	return out
}

// version is one version line: the first group of re in cmd's stdout, or
// the recipe's version, saying why, when cmd fails or prints no match.
func (m *Manager) version(ctx context.Context, container, service string, cmd []string, re *regexp.Regexp, what, recipe string) string {
	words, err := m.read(ctx, container, cmd)
	if err == nil {
		if g := re.FindStringSubmatch(strings.Join(words, " ")); g != nil {
			return fmt.Sprintf("%s %s, read from the %s container", what, g[1], service)
		}
	}
	why := "the " + service + " container cannot report it"
	if errors.Is(err, context.DeadlineExceeded) {
		why = fmt.Sprintf("the %s container did not answer within %s", service, ReadTimeout)
	}
	return fmt.Sprintf("%s %s, from the recipe, because %s", what, recipe, why)
}

// read runs cmd in a container, for at most ReadTimeout, and returns the
// words of its stdout.
func (m *Manager) read(ctx context.Context, container string, cmd []string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, ReadTimeout)
	defer cancel()
	var stdout bytes.Buffer
	err := m.docker.Exec(ctx, docker.Exec{Container: container, Cmd: cmd, Stdout: &stdout})
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	return strings.Fields(stdout.String()), nil
}

// beforeTestsHook is a before_tests hook that calls an app the recipe lacks.
type beforeTestsHook struct{ app, hook, needs string }

// beforeTestsValue is the value of before_tests in a hooks.py: a string, or
// a list that may span lines.
var beforeTestsValue = regexp.MustCompile(`(?ms)^before_tests\s*=\s*(\[.*?\]|"[^"\n]*"|'[^'\n]*')`)

// missingBeforeTests reads the before_tests of each cloned recipe app in of,
// and returns each hook whose app is neither Frappe nor in the recipe.
func missingBeforeTests(apps string, r Recipe, of []string) []beforeTestsHook {
	var out []beforeTestsHook
	for _, a := range r.Apps {
		if a.Name == "" || !slices.Contains(of, a.Name) {
			continue
		}
		hooks, err := os.ReadFile(filepath.Join(apps, a.Name, a.Name, "hooks.py"))
		if err != nil {
			continue
		}
		m := beforeTestsValue.FindSubmatch(hooks)
		if m == nil {
			continue
		}
		for _, q := range quoted.FindAllSubmatch(m[1], -1) {
			hook := string(q[1])
			needs, _, _ := strings.Cut(hook, ".")
			if needs != "frappe" && !slices.Contains(r.names(), needs) {
				out = append(out, beforeTestsHook{app: a.Name, hook: hook, needs: needs})
			}
		}
	}
	return out
}
