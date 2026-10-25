package main

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/engine/enginetest"
	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/exitcode"
)

// The credential bridge (ADR 0002). These tests run the real host git,
// steered at a canned credential helper through the sandbox's global config.
// The engine stays the only fake point.

const privateApp = "https://github.com/myorg/private"

// cannedCredentials points the sandbox's host git at a helper script that
// answers fill with fixed values and logs every call. Each line of the
// returned log is one helper action: get, store or erase.
func (c *cli) cannedCredentials(t *testing.T, username, password string) string {
	t.Helper()
	return c.credentialHelper(t, fmt.Sprintf("printf 'username=%s\\npassword=%s\\n'\n", username, password), "")
}

// pathScopedCredentials answers by repository path ("" is a fill without one),
// beside config, the rest of the git config that sets credential.useHttpPath.
func (c *cli) pathScopedCredentials(t *testing.T, passwords map[string]string, config string) string {
	t.Helper()
	answer := "while read -r line; do case \"$line\" in path=*) p=${line#path=};; esac; done\ncase \"$p\" in\n"
	for path, password := range passwords {
		answer += fmt.Sprintf("  '%s') printf 'username=dev\\npassword=%s\\n';;\n", path, password)
	}
	return c.credentialHelper(t, answer+"esac\n", config)
}

// credentialHelper writes the helper script, whose get answer is the shell
// fragment answer, and the sandbox's git config naming it.
func (c *cli) credentialHelper(t *testing.T, answer, config string) string {
	t.Helper()
	log := filepath.ToSlash(filepath.Join(c.home, "credential.log"))
	helper := filepath.ToSlash(filepath.Join(c.home, "credential-helper.sh"))

	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$1\" >> '%s'\n[ \"$1\" = get ] || exit 0\n%s", log, answer)
	if err := os.WriteFile(helper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	global, _ := os.LookupEnv("GIT_CONFIG_GLOBAL")
	body := fmt.Sprintf("[credential]\n\thelper = \"!sh '%s'\"\n%s", helper, config)
	if err := os.WriteFile(global, []byte(body), 0o644); err != nil {
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
		t.Errorf("ls-remote at exec %d, bench init at %d: the preflight must come first", probe, init)
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

// The Azure DevOps multi-organization setup: one host, one credential per
// organization, and neither works for the other.
const (
	alphaApp = "https://dev.azure.com/orga/p/_git/alpha"
	betaApp  = "https://dev.azure.com/orgb/p/_git/beta"
)

func TestAPathScopedHostGivesEachRepositoryItsOwnCredential(t *testing.T) {
	c := sandbox(t)
	c.engine.PrivateRepos = map[string]string{alphaApp: "s3cret-A", betaApp: "s3cret-B"}
	log := c.pathScopedCredentials(t, map[string]string{"orga/p/_git/alpha": "s3cret-A", "orgb/p/_git/beta": "s3cret-B"},
		"\tuseHttpPath = true\n")

	r := c.run(t, "create", "demo", "--frappe", "version-15", "--apps", alphaApp+":main,"+betaApp+":main")

	r.assertCode(t, exitcode.CodeOK)
	if got := countOf(credentialCalls(t, log), "store"); got != 2 {
		t.Errorf("two path-scoped credentials were approved %d times, want 2", got)
	}
}

// Git's credential path keeps a URL's query, so the fill must ask for it too.
func TestAPathScopedSourceWithAQueryFillsForItsWholePath(t *testing.T) {
	c := sandbox(t)
	const source = alphaApp + "?ref=x"
	c.engine.PrivateRepos = map[string]string{source: "s3cret-A"}
	c.pathScopedCredentials(t, map[string]string{"orga/p/_git/alpha?ref=x": "s3cret-A"}, "\tuseHttpPath = true\n")

	c.run(t, "create", "demo", "--frappe", "version-15", "--apps", source+":main").assertCode(t, exitcode.CodeOK)
}

// useHttpPath set for one organization only: the other's host-wide
// credential must not be presented to it.
func TestAHostWideCredentialIsNeverReusedForARepositoryScopedByPath(t *testing.T) {
	c := sandbox(t)
	c.engine.PrivateRepos = map[string]string{alphaApp: "s3cret-A", betaApp: "s3cret-B"}
	log := c.pathScopedCredentials(t, map[string]string{"": "s3cret-A", "orgb/p/_git/beta": "s3cret-B"},
		"[credential \"https://dev.azure.com/orgb\"]\n\tuseHttpPath = true\n")

	r := c.run(t, "create", "demo", "--frappe", "version-15", "--apps", alphaApp+":main,"+betaApp+":main")

	r.assertCode(t, exitcode.CodeOK)
	if got := countOf(credentialCalls(t, log), "store"); got != 2 {
		t.Errorf("a host-wide and a path-scoped credential were approved %d times, want 2", got)
	}
}

// The fetch exec's helper answers only for the credential's own scope, so a
// dependency cloned from another repository on the host is not served it.
func TestAPathScopedFetchCarriesItsCredentialForItsRepositoryOnly(t *testing.T) {
	c := sandbox(t)
	c.engine.PrivateRepos = map[string]string{alphaApp: "s3cret-A"}
	c.pathScopedCredentials(t, map[string]string{"orga/p/_git/alpha": "s3cret-A"}, "\tuseHttpPath = true\n")

	c.run(t, "create", "demo", "--frappe", "version-15", "--apps", alphaApp+":main").assertCode(t, exitcode.CodeOK)

	fetch := c.benchRan(t, "bench get-app")
	if !slices.Contains(gitConfigKeys(fetch.Env), "credential."+alphaApp+".helper") {
		t.Errorf("the fetch's credential is not scoped to %s: %v", alphaApp, fetch.Env)
	}
}

// Git applies credential config in file order, so a per-URL override above a
// global useHttpPath loses. The scope must be what host git actually does.
func TestThePathScopeIsWhatHostGitDoesWhateverTheConfigOrder(t *testing.T) {
	c := sandbox(t)
	c.engine.PrivateRepos = map[string]string{alphaApp: "s3cret-A"}
	c.pathScopedCredentials(t, map[string]string{"orga/p/_git/alpha": "s3cret-A"},
		"[credential \"https://dev.azure.com/orga\"]\n\tuseHttpPath = false\n[credential]\n\tuseHttpPath = true\n")

	c.run(t, "create", "demo", "--frappe", "version-15", "--apps", alphaApp+":main").assertCode(t, exitcode.CodeOK)

	fetch := c.benchRan(t, "bench get-app")
	if !slices.Contains(gitConfigKeys(fetch.Env), "credential."+alphaApp+".helper") {
		t.Errorf("host git sends this repository's path, but the fetch's credential is not scoped to it: %v", fetch.Env)
	}
}

// Git takes a helper's host= answer as given, so the scope kind must come
// from tamp's own decision, not from the host the helper sent back.
func TestAHostCredentialStaysHostScopedWhateverHostTheHelperAnswers(t *testing.T) {
	c := sandbox(t)
	c.engine.PrivateRepos = map[string]string{alphaApp: "s3cret-A"}
	c.credentialHelper(t, "printf 'host=DEV.AZURE.COM\\nusername=dev\\npassword=s3cret-A\\n'\n",
		"[credential \"https://dev.azure.com/orgb\"]\n\tuseHttpPath = true\n")

	c.run(t, "create", "demo", "--frappe", "version-15", "--apps", alphaApp+":main").assertCode(t, exitcode.CodeOK)

	fetch := c.benchRan(t, "bench get-app")
	if got := fillUnder(t, fetch.Env, "orga/p/_git/alpha"); !strings.Contains(got, "password=s3cret-A") {
		t.Errorf("the fetch's git was not served the host credential for orga:\n%s", got)
	}
	if got := fillUnder(t, fetch.Env, "orgb/p/_git/beta"); strings.Contains(got, "s3cret-A") {
		t.Errorf("the fetch's git served the host credential to orgb, which the host scopes by path:\n%s", got)
	}
}

// The scope URL is the source's own, so a helper answering another protocol
// cannot move the injected credential off it.
func TestTheInjectedCredentialKeepsTheSourcesProtocolWhateverTheHelperAnswers(t *testing.T) {
	c := sandbox(t)
	c.engine.PrivateRepos = map[string]string{privateApp: "s3cret-9Lmn"}
	c.credentialHelper(t, "printf 'protocol=http\\nusername=dev\\npassword=s3cret-9Lmn\\n'\n", "")

	c.run(t, "create", "demo", "--frappe", "version-15", "--apps", privateApp+":version-15").assertCode(t, exitcode.CodeOK)

	fetch := c.benchRan(t, "bench get-app")
	if !slices.Contains(gitConfigKeys(fetch.Env), "credential.https://github.com.helper") {
		t.Errorf("the fetch's credential is not scoped to https://github.com: %v", fetch.Env)
	}
}

// The protocol is part of the scope: one host's http and https credentials
// are different credentials, each injected for its own URL.
func TestEachProtocolOnAHostGetsItsOwnScope(t *testing.T) {
	c := sandbox(t)
	const plain = "http://git.example.com/myorg/plain"
	const secure = "https://git.example.com/myorg/secure"
	c.engine.PrivateRepos = map[string]string{plain: "s3cret-9Lmn", secure: "s3cret-9Lmn"}
	log := c.cannedCredentials(t, "dev", "s3cret-9Lmn")

	c.run(t, "create", "demo", "--frappe", "version-15", "--apps", plain+":main,"+secure+":main").assertCode(t, exitcode.CodeOK)

	if got := countOf(credentialCalls(t, log), "get"); got != 2 {
		t.Errorf("an http and an https source on one host filled %d credentials, want 2", got)
	}
	for source, key := range map[string]string{plain: "credential.http://git.example.com.helper", secure: "credential.https://git.example.com.helper"} {
		i := slices.IndexFunc(c.engine.Execs, func(e enginetest.Exec) bool {
			return strings.Contains(e.Line(), "get-app") && strings.Contains(e.Line(), source)
		})
		if i < 0 {
			t.Fatalf("%s was never fetched", source)
		}
		if !slices.Contains(gitConfigKeys(c.engine.Execs[i].Env), key) {
			t.Errorf("the fetch of %s does not carry %s: %v", source, key, c.engine.Execs[i].Env)
		}
	}
}

// fillUnder runs real git's credential fill for a dev.azure.com path with an
// exec's environment as git config, as the container's git would see it.
func fillUnder(t *testing.T, env []string, path string) string {
	t.Helper()
	cmd := exec.Command("git", "credential", "fill")
	// Later entries win: none of the sandbox's own git config leaks in.
	cmd.Env = append(append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+filepath.Join(t.TempDir(), "gitconfig")), env...)
	cmd.Stdin = strings.NewReader("protocol=https\nhost=dev.azure.com\npath=" + path + "\n")
	out, _ := cmd.Output()
	return string(out)
}

// gitConfigKeys lists the keys of the GIT_CONFIG_KEY_n entries in env.
func gitConfigKeys(env []string) []string {
	var keys []string
	for _, kv := range env {
		if name, key, _ := strings.Cut(kv, "="); strings.HasPrefix(name, "GIT_CONFIG_KEY_") {
			keys = append(keys, key)
		}
	}
	return keys
}

// The host-wide credential's fetch must stand down for an organization the
// host's git scopes by path, so a dependency cloned from it is not served.
func TestAHostWideFetchDoesNotServeTheURLsTheHostScopesByPath(t *testing.T) {
	c := sandbox(t)
	c.engine.PrivateRepos = map[string]string{alphaApp: "s3cret-A"}
	// Path-scoped everywhere but orga, so orga's credential is host-wide.
	c.pathScopedCredentials(t, map[string]string{"": "s3cret-A"},
		"\tuseHttpPath = true\n[credential \"https://dev.azure.com/orga\"]\n\tuseHttpPath = false\n")

	c.run(t, "create", "demo", "--frappe", "version-15", "--apps", alphaApp+":main").assertCode(t, exitcode.CodeOK)

	fetch := c.benchRan(t, "bench get-app")
	for key, value := range map[string]string{"credential.usehttppath": "true", "credential.https://dev.azure.com/orga.usehttppath": "false"} {
		if got, ok := gitConfigIn(fetch.Env, key); !ok || got != value {
			t.Errorf("the fetch replays the host's %s as %q (set: %t), want %q", key, got, ok, value)
		}
	}
}

// gitConfigIn reads one entry of the GIT_CONFIG_KEY_n/VALUE_n pairs in env.
func gitConfigIn(env []string, key string) (string, bool) {
	for _, kv := range env {
		name, k, _ := strings.Cut(kv, "=")
		if n, ok := strings.CutPrefix(name, "GIT_CONFIG_KEY_"); ok && k == key {
			for _, kv := range env {
				if v, ok := strings.CutPrefix(kv, "GIT_CONFIG_VALUE_"+n+"="); ok {
					return v, true
				}
			}
		}
	}
	return "", false
}

// Per-scope reject: one organization's refusal drops only its own credential,
// however well another organization's fared on the same host.
func TestARefusalOnAPathScopedHostRejectsOnlyThatRepositorysCredential(t *testing.T) {
	c := sandbox(t)
	c.engine.PrivateRepos = map[string]string{alphaApp: "s3cret-A", betaApp: "s3cret-B"}
	log := c.pathScopedCredentials(t, map[string]string{"orga/p/_git/alpha": "s3cret-A", "orgb/p/_git/beta": "stale-B"},
		"\tuseHttpPath = true\n")

	r := c.run(t, "create", "demo", "--frappe", "version-15", "--apps", alphaApp+":main,"+betaApp+":main")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, betaApp, "refused", "sign in")
	if got := countOf(credentialCalls(t, log), "erase"); got != 1 {
		t.Errorf("the helper was told to drop %d credentials, want 1, orgb's", got)
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
		t.Errorf("the credential was approved %d times, want 1, after the authenticated fetch", got)
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

// A host git that cannot read its own config has not said there is no
// credential. Sending the user to sign in would mislead.
func TestABrokenHostGitConfigIsReportedAsSuchNotAsAMissingCredential(t *testing.T) {
	c := sandbox(t)
	c.engine.PrivateRepos = map[string]string{privateApp: "s3cret"}
	global, _ := os.LookupEnv("GIT_CONFIG_GLOBAL")
	if err := os.WriteFile(global, []byte("[credential\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := c.run(t, "create", "demo", "--frappe", "version-15", "--apps", privateApp+":version-15")

	r.assertCode(t, exitcode.CodeFailed)
	r.assertStderrContains(t, privateApp, "the host's git failed", "bad config", "git config --list")
	if strings.Contains(r.stderr, "sign in") {
		t.Errorf("a broken git config was blamed on a missing sign-in:\n%s", r.stderr)
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

// --- reject is scoped to the credential --------------------------------------

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
// exist, the same words a typo gets.
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
