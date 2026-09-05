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
// credentials, filled at most once per host, held in memory, and injected
// into individual container execs. tamp is a conduit, never a store.
type bridge struct {
	out   *ui.Printer
	creds map[string]gitcred.Credential
	// needs marks the sources whose preflight required the credential; a
	// public app on the same host still fetches bare.
	needs    map[string]bool
	approved map[string]bool
	// accepted marks the hosts where some source answered to the credential,
	// indicted those where some source refused it outright. Reject is
	// host-scoped: it fires only for a credential that worked nowhere.
	accepted map[string]bool
	indicted map[string]bool
}

func (m *Manager) newBridge() *bridge {
	return &bridge{
		out:      m.Out,
		creds:    map[string]gitcred.Credential{},
		needs:    map[string]bool{},
		approved: map[string]bool{},
		accepted: map[string]bool{},
		indicted: map[string]bool{},
	}
}

// credentialRefusal is a source refusing the injected credential. Its verdict
// waits until every source has answered: on a host that accepted the
// credential elsewhere it is a repository-scoped denial, not a stale sign-in.
type credentialRefusal struct {
	source, host string
}

func (r *credentialRefusal) Error() string {
	return refusedCredential(r.source, r.host).Error()
}

// preflightApps proves every source that will be fetched answers from inside
// the container, before the expensive bench build; a source that looks
// private goes over the bridge and is retried with the credential injected.
// An app already on the bench, or in the host tree a re-adoption's sync
// session will mirror in, is never fetched — so it is not probed either.
func (m *Manager) preflightApps(ctx context.Context, e *Environment, bench *frappe.Bench, apps []App, log *createLog) (*bridge, error) {
	br := m.newBridge()
	if len(apps) == 0 {
		return br, nil
	}
	onBench, err := bench.Apps(ctx)
	if err != nil {
		return nil, err
	}
	// The first failure is the one reported. A refused credential alone keeps
	// the preflight going: whether it is stale depends on what the rest of
	// its host says, and nothing is rejected before every source has answered.
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
// lets the preflight go on; err — the engine, or host git producing no
// credential — ends it undecided, so nothing is rejected.
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
	cred, err := br.credential(ctx, source, log)
	if err != nil {
		return nil, err
	}

	refusal, err = remoteRefusal(bench.CheckRemote(ctx, source, credentialEnv(cred)))
	if err != nil {
		return nil, err
	}
	if refusal == nil {
		br.needs[source] = true
		br.accepted[host] = true
		return nil, nil
	}
	fmt.Fprintln(log.stream(), strings.TrimSpace(refusal.Output))
	if !indicts(refusal.Output, host) {
		return deniedRepository(source, host), nil
	}
	br.indicted[host] = true
	return &credentialRefusal{source: source, host: host}, nil
}

// settle completes the credential protocol once every source has answered:
// only a credential that worked nowhere on its host is rejected.
func (b *bridge) settle(ctx context.Context) {
	for host := range b.indicted {
		if !b.accepted[host] {
			b.reject(ctx, host)
		}
	}
}

// verdict is the reportable error for a failed source, judged against the run.
func (b *bridge) verdict(failed error) error {
	var cr *credentialRefusal
	if !errors.As(failed, &cr) {
		return failed
	}
	if b.accepted[cr.host] {
		return deniedRepository(cr.source, cr.host)
	}
	return refusedCredential(cr.source, cr.host)
}

// credential fills at most once per host per run, delegating to whatever
// helper the host's git is configured with.
func (b *bridge) credential(ctx context.Context, source string, log *createLog) (gitcred.Credential, error) {
	protocol, host, path, err := sourceParts(source)
	if err != nil {
		return gitcred.Credential{}, err
	}
	if cred, ok := b.creds[host]; ok {
		return cred, nil
	}

	// The helper may open its own sign-in prompt; without this line the
	// pause reads as a hang.
	log.note(fmt.Sprintf("%s looks private — waiting on the host's git credential system for %s (a sign-in prompt may appear)", source, host))
	cred, err := gitcred.Fill(ctx, protocol, host, path, b.out.Err)
	switch {
	case errors.Is(err, gitcred.ErrNoGit):
		return gitcred.Credential{}, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("%s looks private, and tamp found no git on this machine to ask for its credentials", source),
			"install git, sign in to the repository's host once, and run this again")
	case errors.Is(err, gitcred.ErrNoCredential):
		return gitcred.Credential{}, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("%s looks private, and the host's git credential system has no credential for %s", source, host),
			signInFix(source))
	case err != nil:
		return gitcred.Credential{}, err
	}
	b.creds[host] = cred
	return cred, nil
}

// envFor is the injection for one fetch: the credential for the source's
// host, or nil when this source never needed one.
func (b *bridge) envFor(source string) []string {
	if !b.needs[source] {
		return nil
	}
	_, host, _, err := sourceParts(source)
	if err != nil {
		return nil
	}
	cred, ok := b.creds[host]
	if !ok {
		return nil
	}
	return credentialEnv(cred)
}

// approve runs once per host, so the helper caches what worked. Its own
// failure only warns — the fetch already succeeded.
func (b *bridge) approve(ctx context.Context, source string) {
	_, host, _, err := sourceParts(source)
	if err != nil {
		return
	}
	cred, ok := b.creds[host]
	if !ok || b.approved[host] {
		return
	}
	b.approved[host] = true
	if err := gitcred.Approve(ctx, cred, b.out.Err); err != nil {
		b.out.Warn(fmt.Sprintf("could not tell the host's credential helper the %s credential worked: %v", host, err))
	}
}

// reject tells the host's helper its credential was refused, so the next run
// prompts instead of replaying the same stale secret.
func (b *bridge) reject(ctx context.Context, host string) {
	cred, ok := b.creds[host]
	if !ok {
		return
	}
	delete(b.creds, host)
	if err := gitcred.Reject(ctx, cred, b.out.Err); err != nil {
		b.out.Warn(fmt.Sprintf("could not tell the host's credential helper the %s credential failed: %v", host, err))
	}
}

func credentialEnv(cred gitcred.Credential) []string {
	return frappe.CredentialEnv(cred.Protocol, cred.Host, cred.Username, cred.Password)
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

// indicts reports whether a presented credential was refused outright — the
// one answer that blames the sign-in rather than access to the repository.
// The host check keeps a fetch transcript's other-host failures out.
func indicts(output, host string) bool {
	return strings.Contains(output, "Authentication failed") && strings.Contains(output, host)
}

// authShaped reports whether git's output to a bare probe is a credential
// demand rather than any other failure — the trigger for the bridge.
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
		"check the URL for a typo, and that the repository still exists — git's answer is in the output above")
}

func refusedCredential(source, host string) error {
	return exitcode.New(exitcode.CodeFailed,
		fmt.Sprintf("%s refused the host's credential for %s", source, host),
		signInFix(source))
}

// deniedRepository is the verdict when the host took the credential but hid
// or denied the repository: the sign-in is fine, the access is not.
func deniedRepository(source, host string) error {
	return exitcode.New(exitcode.CodeFailed,
		fmt.Sprintf("%s took the host's credential but hid or denied %s", host, source),
		"check the URL, and that your account can access the repository (organization membership, SSO authorization)")
}

// signInFix is the one action that repairs any credential problem: the
// user's own git triggers their helper's sign-in, and the next run finds it.
func signInFix(source string) string {
	return fmt.Sprintf("sign in on this machine — 'git ls-remote %s' in your own shell will prompt — then run this again", source)
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
	return u.Scheme, u.Host, strings.TrimPrefix(u.Path, "/"), nil
}
