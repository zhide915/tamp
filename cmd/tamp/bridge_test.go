package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/engine/enginetest"
	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/exitcode"
)

// The credential bridge (ADR 0002). These tests run the real host git,
// steered at a canned credential helper through the sandbox's global config
// — the engine stays the only fake point.

const privateApp = "https://github.com/myorg/private"

// cannedCredentials points the sandbox's host git at a helper script that
// answers fill with fixed values and logs every call. Each line of the
// returned log is one helper action: get, store or erase.
func (c *cli) cannedCredentials(t *testing.T, username, password string) string {
	t.Helper()
	log := filepath.ToSlash(filepath.Join(c.home, "credential.log"))
	helper := filepath.ToSlash(filepath.Join(c.home, "credential-helper.sh"))

	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$1\" >> '%s'\nif [ \"$1\" = get ]; then\n  printf 'username=%s\\npassword=%s\\n'\nfi\n",
		log, username, password)
	if err := os.WriteFile(helper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	config, _ := os.LookupEnv("GIT_CONFIG_GLOBAL")
	body := fmt.Sprintf("[credential]\n\thelper = \"!sh '%s'\"\n", helper)
	if err := os.WriteFile(config, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return log
}

// credentialCalls reads the helper's log; a log never written is no calls.
func credentialCalls(t *testing.T, log string) []string {
	t.Helper()
	body, err := os.ReadFile(log)
	if err != nil {
		return nil
	}
	return strings.Fields(string(body))
}

func execIndex(execs []enginetest.Exec, fragment string) int {
	for i, e := range execs {
		if strings.Contains(e.Line(), fragment) {
			return i
		}
	}
	return -1
}

// --- preflight ---------------------------------------------------------------

func TestCreateFailsInThePreflightWhenAnAppSourceDoesNotExist(t *testing.T) {
	c := sandbox(t)
	const typo = "https://github.com/myorg/typo"
	c.engine.MissingRepos = map[string]bool{typo: true}

	r := c.run(t, "create", "demo", "--frappe", "version-15", "--apps", typo+":version-15")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, typo, "check the URL")
	if c.engine.Ran("bench init") {
		t.Error("the bench was initialized for a source that does not exist")
	}
	if log := c.read(t, "demo", env.StateDirName, env.CreateLogFile); !strings.Contains(log, "checking that every app source answers") {
		t.Errorf("create.log does not show the preflight step:\n%s", log)
	}
}

func TestThePreflightRunsBeforeTheBenchIsInitialized(t *testing.T) {
	c := sandbox(t)

	c.create(t, "demo", "--apps", "erpnext:version-15")

	probe := execIndex(c.engine.Execs, "git ls-remote")
	init := execIndex(c.engine.Execs, "bench init")
	if probe < 0 || init < 0 || probe > init {
		t.Errorf("ls-remote at exec %d, bench init at %d — the preflight must come first", probe, init)
	}
}

// The honest error, replacing "the output above says why": before any
// credential exists the user learns the repo looks private and what to do.
func TestCreateWithAPrivateSourceAndNoHostCredentialFailsBeforeTheBenchIsBuilt(t *testing.T) {
	c := sandbox(t)
	c.engine.PrivateRepos = map[string]string{privateApp: "s3cret"}
	// No canned helper: the sandbox's git config knows no credentials.

	r := c.run(t, "create", "demo", "--frappe", "version-15", "--apps", privateApp+":version-15")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, privateApp, "looks private", "sign in")
	if c.engine.Ran("bench init") {
		t.Error("the bench was initialized for a fetch that could never work")
	}
}

func TestInitPreflightsAppSourcesLikeCreateDoes(t *testing.T) {
	c := sandbox(t)
	const typo = "https://github.com/myorg/typo"
	c.engine.MissingRepos = map[string]bool{typo: true}

	r := c.inside(t, c.path("demo"), "init", "--frappe", "version-15", "--apps", typo+":version-15")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, typo)
	if c.engine.Ran("bench init") {
		t.Error("init initialized the bench for a source that does not exist")
	}
}

// A tamp.toml from before the bridge can carry an ssh source the flag parser
// never sees again; the preflight must hand back the https rewrite, not a
// reachability guess.
func TestAnSSHSourceInAnOldTampTomlGetsTheHTTPSRewriteAtPreflight(t *testing.T) {
	c := sandbox(t)
	c.create(t, "demo", "--apps", "https://github.com/myorg/app:main")
	c.run(t, "rm", "demo", "--volumes", "--yes").assertCode(t, exitcode.CodeOK)

	// What an older tamp would have recorded.
	config := strings.Replace(c.read(t, "demo", "tamp.toml"),
		"https://github.com/myorg/app", "git@github.com:myorg/app.git", 1)
	if err := os.WriteFile(c.path("demo", "tamp.toml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	c.leaveSource(t, "demo")
	before := len(c.engine.Execs)

	r := c.inside(t, c.path("demo"), "init")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, "tamp.toml", "https://github.com/myorg/app.git")
	if execIndex(c.engine.Execs[before:], "bench init") >= 0 {
		t.Error("the adoption initialized the bench for a source tamp cannot fetch")
	}
}

// --- the credential bridge ---------------------------------------------------

func TestCreateBridgesTheHostCredentialIntoAPrivateFetch(t *testing.T) {
	c := sandbox(t)
	c.engine.PrivateRepos = map[string]string{privateApp: "s3cret-9Lmn"}
	log := c.cannedCredentials(t, "dev", "s3cret-9Lmn")

	r := c.run(t, "create", "demo", "--frappe", "version-15", "--apps", privateApp+":version-15")

	r.assertCode(t, exitcode.CodeOK)
	if got := c.engine.Apps(); !slices.Contains(got, "private") {
		t.Errorf("the private app never reached the bench: %v", got)
	}
	calls := credentialCalls(t, log)
	if got := countOf(calls, "get"); got != 1 {
		t.Errorf("the helper was asked for credentials %d times, want 1: %v", got, calls)
	}
	// approve, after the first successful authenticated fetch.
	if got := countOf(calls, "store"); got != 1 {
		t.Errorf("the helper was told to store %d times, want 1: %v", got, calls)
	}
	// The user was told why the create paused.
	r.assertStdoutContains(t, "waiting on the host's git credential system")
}

func TestOneSignInServesEveryPrivateAppOnTheSameHost(t *testing.T) {
	c := sandbox(t)
	const second = "https://github.com/myorg/hidden"
	c.engine.PrivateRepos = map[string]string{privateApp: "s3cret-9Lmn", second: "s3cret-9Lmn"}
	log := c.cannedCredentials(t, "dev", "s3cret-9Lmn")

	r := c.run(t, "create", "demo", "--frappe", "version-15",
		"--apps", privateApp+":version-15,"+second+":version-15")

	r.assertCode(t, exitcode.CodeOK)
	calls := credentialCalls(t, log)
	if got := countOf(calls, "get"); got != 1 {
		t.Errorf("two apps on one host asked for credentials %d times, want 1: %v", got, calls)
	}
	if got := countOf(calls, "store"); got != 1 {
		t.Errorf("one host was approved %d times, want 1: %v", got, calls)
	}
}

// The bridge must cost nothing where it is not needed: a public app fetches
// bare even when a private app on the same host filled a credential, and
// only the authenticated fetch can be what approves it.
func TestAPublicAppOnABridgedHostStillFetchesWithoutTheCredential(t *testing.T) {
	c := sandbox(t)
	const public = "https://github.com/myorg/open"
	c.engine.PrivateRepos = map[string]string{privateApp: "s3cret-2Wxy"}
	log := c.cannedCredentials(t, "dev", "s3cret-2Wxy")

	// The public app first, so a bare fetch cannot be the one approving.
	r := c.run(t, "create", "demo", "--frappe", "version-15",
		"--apps", public+":version-15,"+privateApp+":version-15")

	r.assertCode(t, exitcode.CodeOK)
	for _, e := range c.engine.Execs {
		if !strings.Contains(e.Line(), "get-app") || !strings.Contains(e.Line(), public) {
			continue
		}
		for _, kv := range e.Env {
			if strings.Contains(kv, "TAMP_GIT") {
				t.Errorf("the public fetch carries credential configuration: %s", kv)
			}
		}
	}
	if got := countOf(credentialCalls(t, log), "store"); got != 1 {
		t.Errorf("the credential was approved %d times, want 1 — after the authenticated fetch", got)
	}
}

// The at-rest guarantee: the secret lives only in exec environments, never in
// a command line, a generated file, the log, or the terminal.
func TestTheBridgedSecretReachesNoFileNoCommandLineAndNoOutput(t *testing.T) {
	c := sandbox(t)
	const secret = "s3cret-7Pqr"
	c.engine.PrivateRepos = map[string]string{privateApp: secret}
	c.cannedCredentials(t, "dev", secret)

	r := c.run(t, "create", "demo", "--frappe", "version-15", "--apps", privateApp+":version-15")

	r.assertCode(t, exitcode.CodeOK)
	if strings.Contains(r.stdout+r.stderr, secret) {
		t.Error("the secret appears in the terminal output")
	}
	for _, e := range c.engine.Execs {
		if strings.Contains(e.Line(), secret) {
			t.Errorf("the secret appears in an engine command line: %s", e.Line())
		}
	}
	err := filepath.WalkDir(c.path("demo"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(body), secret) {
			t.Errorf("the secret appears in %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// The clone's remote must be the clean URL: the fetch exec names the
	// source exactly as tamp.toml records it, with nothing embedded.
	if !strings.Contains(c.benchRan(t, "bench get-app").Line(), privateApp) {
		t.Error("the fetch did not use the clean source URL")
	}
}

func TestARefusedCredentialIsRejectedAndTheCreateNamesTheRepository(t *testing.T) {
	c := sandbox(t)
	c.engine.PrivateRepos = map[string]string{privateApp: "the-right-one"}
	log := c.cannedCredentials(t, "dev", "stale-secret")

	r := c.run(t, "create", "demo", "--frappe", "version-15", "--apps", privateApp+":version-15")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, privateApp, "refused", "sign in")
	if !slices.Contains(credentialCalls(t, log), "erase") {
		t.Error("the helper was never told its credential failed")
	}
	if c.engine.Ran("bench init") {
		t.Error("the bench was initialized for a fetch that could never work")
	}
}

func TestAPublicOnlyCreateRunsNoCredentialMachinery(t *testing.T) {
	c := sandbox(t)
	log := c.cannedCredentials(t, "dev", "s3cret-unused")

	c.create(t, "demo", "--apps", "erpnext:version-15")

	if calls := credentialCalls(t, log); len(calls) != 0 {
		t.Errorf("a public create still spoke to the credential helper: %v", calls)
	}
	for _, e := range c.engine.Execs {
		for _, kv := range e.Env {
			if strings.Contains(kv, "TAMP_GIT") {
				t.Errorf("a public create still injected credential configuration: %s", kv)
			}
		}
	}
}

// Host git is a lazy dependency: its absence costs exactly the private path.
func TestWithoutHostGitOnlyThePrivatePathFails(t *testing.T) {
	c := sandbox(t)
	t.Setenv("PATH", t.TempDir()) // a PATH with no git on it

	c.create(t, "public", "--apps", "erpnext:version-15")

	c.engine.PrivateRepos = map[string]string{privateApp: "s3cret"}
	r := c.run(t, "create", "demo", "--frappe", "version-15",
		"--dir", t.TempDir(), "--apps", privateApp+":version-15")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, privateApp, "no git on this machine", "install git")
}

func countOf(calls []string, verb string) int {
	n := 0
	for _, call := range calls {
		if call == verb {
			n++
		}
	}
	return n
}

// --- reject is host-scoped ---------------------------------------------------

// Another organization's repository, or one behind SSO the account has not
// authorized, refuses a credential the host accepts everywhere else.
func TestARepositoryRefusingACredentialTheHostAcceptedElsewhereIsADenialNotAReject(t *testing.T) {
	const other = "https://github.com/otherorg/locked"
	for name, order := range map[string]string{
		"accepted first": privateApp + ":version-15," + other + ":version-15",
		"refused first":  other + ":version-15," + privateApp + ":version-15",
	} {
		t.Run(name, func(t *testing.T) {
			c := sandbox(t)
			c.engine.PrivateRepos = map[string]string{privateApp: "s3cret-9Lmn", other: "someone-elses"}
			log := c.cannedCredentials(t, "dev", "s3cret-9Lmn")

			r := c.run(t, "create", "demo", "--frappe", "version-15", "--apps", order)

			r.assertCode(t, exitcode.CodeFailed)
			r.assertStderrContains(t, other, "can access the repository")
			if strings.Contains(r.stderr, "sign in") {
				t.Errorf("the run blamed the sign-in for a repository-scoped denial:\n%s", r.stderr)
			}
			if calls := credentialCalls(t, log); slices.Contains(calls, "erase") {
				t.Errorf("the helper was told to drop a credential the host accepted: %v", calls)
			}
		})
	}
}

// GitHub tells a credential without access that the repository does not
// exist — the same words a typo gets.
func TestARepositoryHiddenFromTheCorrectCredentialIsADenialNotATypo(t *testing.T) {
	c := sandbox(t)
	c.engine.DeniedRepos = map[string]string{privateApp: "s3cret-9Lmn"}
	log := c.cannedCredentials(t, "dev", "s3cret-9Lmn")

	r := c.run(t, "create", "demo", "--frappe", "version-15", "--apps", privateApp+":version-15")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, privateApp, "can access the repository")
	for _, wrong := range []string{"check the URL for a typo", "sign in"} {
		if strings.Contains(r.stderr, wrong) {
			t.Errorf("the verdict reads %q for an access-scope denial:\n%s", wrong, r.stderr)
		}
	}
	if calls := credentialCalls(t, log); slices.Contains(calls, "erase") {
		t.Errorf("the helper was told to drop a credential the host accepted: %v", calls)
	}
}

// A typo must not cost the user a sign-in prompt for a run already lost.
func TestATypoAheadOfAPrivateSourceFailsWithoutAskingForCredentials(t *testing.T) {
	c := sandbox(t)
	const typo = "https://github.com/myorg/typo"
	c.engine.MissingRepos = map[string]bool{typo: true}
	c.engine.PrivateRepos = map[string]string{privateApp: "s3cret-9Lmn"}
	log := c.cannedCredentials(t, "dev", "s3cret-9Lmn")

	r := c.run(t, "create", "demo", "--frappe", "version-15", "--apps", typo+":version-15,"+privateApp+":version-15")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, typo, "check the URL")
	if calls := credentialCalls(t, log); len(calls) != 0 {
		t.Errorf("the helper was consulted for a run a typo had already failed: %v", calls)
	}
}
