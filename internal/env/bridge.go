package env

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/frappe"
	"github.com/zhide915/tamp/internal/gitcred"
	"github.com/zhide915/tamp/internal/ui"
)

// bridge is one run's credential bridge (ADR 0002): the host's git
// credentials, filled at most once per scope, held in memory, and injected
// into individual container execs. tamp is a conduit, never a store.
type bridge struct {
	out *ui.Printer
	// creds is keyed by scope: protocol://host, or protocol://host/path where
	// the host's git scopes credentials by path (credential.useHttpPath).
	creds map[string]gitcred.Credential
	// scopes maps each source that went over the bridge to its scope.
	scopes map[string]string
	// whole marks the scopes that cover a whole host.
	whole map[string]bool
	// httpPath is the host git's useHttpPath config, read once and replayed
	// with each host scope's credential (frappe.CredentialEnv).
	httpPath     []gitcred.Setting
	httpPathRead bool
	// needs marks the sources whose preflight required the credential; a
	// public app on the same host still fetches bare.
	needs    map[string]bool
	approved map[string]bool
	// accepted marks the scopes where some source answered to the credential,
	// indicted those where some source refused it outright.
	accepted map[string]bool
	indicted map[string]bool
}

func (m *Manager) newBridge() *bridge {
	return &bridge{
		out:      m.Out,
		creds:    map[string]gitcred.Credential{},
		scopes:   map[string]string{},
		whole:    map[string]bool{},
		needs:    map[string]bool{},
		approved: map[string]bool{},
		accepted: map[string]bool{},
		indicted: map[string]bool{},
	}
}

// credentialRefusal is a source refusing the injected credential. Its verdict
// waits until every source has answered: where the same credential was
// accepted elsewhere it is a repository-scoped denial, not a stale sign-in.
type credentialRefusal struct {
	source, host, scope string
}

func (r *credentialRefusal) Error() string {
	return refusedCredential(r.source, r.host).Error()
}

// preflightApps proves every source that will be fetched answers from inside
// the container, before the expensive bench build; a source that looks
// private goes over the bridge and is retried with the credential injected.
// An app already on the bench, or in the host tree a re-adoption's sync
// session will mirror in, is never fetched, so it is not probed either.
func (m *Manager) preflightApps(ctx context.Context, e *Environment, bench *frappe.Bench, apps []App, log *createLog) (*bridge, error) {
	br := m.newBridge()
	if len(apps) == 0 {
		return br, nil
	}
	onBench, err := bench.Apps(ctx)
	if err != nil {
		return nil, err
	}
	// The first failure is reported. Only a refused credential keeps the
	// preflight going: its verdict depends on what the rest of its host says.
	var first error
	for _, app := range apps {
		if slices.Contains(onBench, app.Name) || hasHostApp(e.Dir, app.Name) {
			continue
		}
		failed, err := m.preflightSource(ctx, bench, app.Source, br, log)
		if err != nil {
			return nil, err
		}
		if failed != nil && first == nil {
			first = failed
			var pending *credentialRefusal
			if !errors.As(first, &pending) {
				break
			}
		}
	}
	if first == nil {
		return br, nil
	}
	br.settle(ctx)
	return nil, br.verdict(first)
}

// preflightSource probes one source. failed is that source's own verdict and
// lets the preflight go on; err (the engine, or host git producing no
// credential) ends it undecided, so nothing is rejected.
func (m *Manager) preflightSource(ctx context.Context, bench *frappe.Bench, source string, br *bridge, log *createLog) (failed, err error) {
	// A tamp.toml from before the bridge can carry an ssh source the flag
	// parser now refuses; the fix is rewriting it, not a reachability answer.
	if https, ssh := httpsFormOfSSH(source); ssh {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("this environment's %s records the ssh app source %s, which tamp cannot fetch", ConfigFile, redactedURL(source)),
			fmt.Sprintf("edit %s: replace the source with %s", ConfigFile, https)), nil
	}

	refusal, err := remoteRefusal(bench.CheckRemote(ctx, source, nil))
	if err != nil || refusal == nil {
		return nil, err
	}
	if !authShaped(refusal.Output) {
		return unreachableSource(refusal, log), nil
	}

	// Auth-shaped: the repository looks private (a host that hides private
	// repositories answers exactly like this for a missing one, too).
	_, host, _, err := sourceParts(source)
	if err != nil {
		return nil, err
	}
	cred, scope, err := br.credential(ctx, source, log)
	if err != nil {
		return nil, err
	}

	refusal, err = remoteRefusal(bench.CheckRemote(ctx, source, br.env(cred, scope)))
	if err != nil {
		return nil, err
	}
	if refusal == nil {
		br.needs[source] = true
		br.accepted[scope] = true
		return nil, nil
	}
	fmt.Fprintln(log.stream(), strings.TrimSpace(refusal.Output))
	if !indicts(refusal.Output, host) {
		return deniedRepository(source, host), nil
	}
	br.indicted[scope] = true
	return &credentialRefusal{source: source, host: host, scope: scope}, nil
}

// settle completes the credential protocol once every source has answered:
// only a credential that worked nowhere it was presented is rejected.
func (b *bridge) settle(ctx context.Context) {
	for scope := range b.indicted {
		if !b.accepted[scope] {
			b.reject(ctx, scope)
		}
	}
}

// verdict is the reportable error for a failed source, judged against the run.
func (b *bridge) verdict(failed error) error {
	var cr *credentialRefusal
	if !errors.As(failed, &cr) {
		return failed
	}
	if b.accepted[cr.scope] {
		return deniedRepository(cr.source, cr.host)
	}
	return refusedCredential(cr.source, cr.host)
}

// credential fills at most once per scope per run, through the host git's own
// helper. Where that git scopes by path, each repository fills its own.
func (b *bridge) credential(ctx context.Context, source string, log *createLog) (gitcred.Credential, string, error) {
	protocol, host, path, err := sourceParts(source)
	if err != nil {
		return gitcred.Credential{}, "", err
	}
	pathScoped, err := gitcred.PathScoped(ctx, protocol, host, path)
	if err != nil {
		return gitcred.Credential{}, "", hostGitFailed(ctx, source, err)
	}
	scope := protocol + "://" + host
	if pathScoped {
		scope += "/" + path
	}
	b.scopes[source] = scope
	b.whole[scope] = !pathScoped
	if cred, ok := b.creds[scope]; ok {
		return cred, scope, nil
	}

	// The helper may open its own sign-in prompt; without this line the
	// pause reads as a hang.
	log.note(fmt.Sprintf("%s looks private, waiting on the host's git credential system for %s (a sign-in prompt may appear)", source, host))
	cred, err := gitcred.Fill(ctx, protocol, host, path, b.out.Err)
	switch {
	case errors.Is(err, gitcred.ErrNoCredential):
		return gitcred.Credential{}, "", exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("%s looks private, and the host's git credential system has no credential for %s", source, host),
			signInFix(source))
	case err != nil:
		return gitcred.Credential{}, "", hostGitFailed(ctx, source, err)
	}
	if !pathScoped && !b.httpPathRead {
		if b.httpPath, err = gitcred.HTTPPathSettings(ctx); err != nil {
			return gitcred.Credential{}, "", hostGitFailed(ctx, source, err)
		}
		b.httpPathRead = true
	}
	b.creds[scope] = cred
	return cred, scope, nil
}

// hostGitFailed is the verdict on host git failing the bridge. A cancelled
// run stays a cancellation.
func hostGitFailed(ctx context.Context, source string, err error) error {
	switch {
	case ctx.Err() != nil:
		return ctx.Err()
	case errors.Is(err, gitcred.ErrNoGit):
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("%s looks private, and tamp found no git on this machine to ask for its credentials", source),
			"install git, sign in to the repository's host once, and run this again")
	}
	return exitcode.New(exitcode.CodeFailed,
		fmt.Sprintf("%s looks private, and the host's git failed when tamp asked it: %v", source, err),
		"check that 'git config --list' runs cleanly in your own shell, then run this again")
}

// held is the credential already filled for a source, and its scope.
func (b *bridge) held(source string) (gitcred.Credential, string, bool) {
	scope, ok := b.scopes[source]
	if !ok {
		return gitcred.Credential{}, "", false
	}
	cred, ok := b.creds[scope]
	return cred, scope, ok
}

// envFor is the injection for one fetch: the credential for the source's
// scope, or nil when this source never needed one.
func (b *bridge) envFor(source string) []string {
	if !b.needs[source] {
		return nil
	}
	cred, scope, ok := b.held(source)
	if !ok {
		return nil
	}
	return b.env(cred, scope)
}

// approve runs once per scope, so the helper caches what worked. Its own
// failure only warns: the fetch already succeeded.
func (b *bridge) approve(ctx context.Context, source string) {
	cred, scope, ok := b.held(source)
	if !ok || b.approved[scope] {
		return
	}
	b.approved[scope] = true
	if err := gitcred.Approve(ctx, cred, b.out.Err); err != nil {
		b.out.Warn(fmt.Sprintf("could not tell the host's credential helper the %s credential worked: %v", scope, err))
	}
}

// reject tells the host's helper its credential was refused, so the next run
// prompts instead of replaying the same stale secret.
func (b *bridge) reject(ctx context.Context, scope string) {
	cred, ok := b.creds[scope]
	if !ok {
		return
	}
	delete(b.creds, scope)
	if err := gitcred.Reject(ctx, cred, b.out.Err); err != nil {
		b.out.Warn(fmt.Sprintf("could not tell the host's credential helper the %s credential failed: %v", scope, err))
	}
}

func (b *bridge) env(cred gitcred.Credential, scope string) []string {
	return frappe.CredentialEnv(scope, cred.Username, cred.Password, b.whole[scope], b.httpPath)
}

// remoteRefusal separates git's "no" from an engine fault: only the former
// carries output tamp can classify.
func remoteRefusal(err error) (*frappe.RemoteError, error) {
	if err == nil {
		return nil, nil
	}
	var refusal *frappe.RemoteError
	if errors.As(err, &refusal) {
		return refusal, nil
	}
	return nil, err
}

// indicts reports whether a presented credential was refused outright, the
// one answer that blames the sign-in rather than access to the repository.
// The host check keeps a fetch transcript's other-host failures out.
func indicts(output, host string) bool {
	return strings.Contains(output, "Authentication failed") && strings.Contains(output, host)
}

// authShaped reports whether git's output to a bare probe is a credential
// demand rather than any other failure, the trigger for the bridge.
func authShaped(output string) bool {
	for _, marker := range []string{
		"could not read Username",
		"could not read Password",
		"terminal prompts disabled",
		"Authentication failed",
		"Access denied",
	} {
		if strings.Contains(output, marker) {
			return true
		}
	}
	return false
}

// unreachableSource is the preflight's verdict on a source git refused for a
// non-auth reason; git's own words go to the log first.
func unreachableSource(refusal *frappe.RemoteError, log *createLog) error {
	fmt.Fprintln(log.stream(), strings.TrimSpace(refusal.Output))
	return exitcode.New(exitcode.CodeFailed,
		fmt.Sprintf("cannot reach the app source %s from inside the environment", refusal.Source),
		"check the URL for a typo, and that the repository still exists (git's answer is in the output above)")
}

func refusedCredential(source, host string) error {
	return exitcode.New(exitcode.CodeFailed,
		fmt.Sprintf("%s refused the host's credential for %s", source, host),
		signInFix(source))
}

func deniedRepository(source, host string) error {
	return exitcode.New(exitcode.CodeFailed,
		fmt.Sprintf("%s took the host's credential but hid or denied %s", host, source),
		"check the URL, and that your account can access the repository (organization membership, SSO authorization)")
}

// signInFix is the one action that repairs any credential problem: the
// user's own git triggers their helper's sign-in, and the next run finds it.
func signInFix(source string) string {
	return fmt.Sprintf("sign in on this machine ('git ls-remote %s' in your own shell will prompt), then run this again", source)
}

// sourceParts splits an app source URL into what the credential protocol
// wants. Parse-time refusals guarantee a scheme URL with no userinfo here.
func sourceParts(source string) (protocol, host, path string, err error) {
	u, err := url.Parse(source)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", "", "", exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot make sense of the app source %q as a URL", source),
			"write the app as a full https URL")
	}
	// Git's credential path is everything after the host, decoded.
	path = strings.TrimPrefix(u.Path, "/")
	if u.RawQuery != "" {
		query, err := url.PathUnescape(u.RawQuery)
		if err != nil {
			query = u.RawQuery
		}
		path += "?" + query
	}
	if u.Fragment != "" {
		path += "#" + u.Fragment
	}
	return u.Scheme, u.Host, path, nil
}
