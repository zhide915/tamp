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
}

func (m *Manager) newBridge() *bridge {
	return &bridge{
		out:      m.Out,
		creds:    map[string]gitcred.Credential{},
		needs:    map[string]bool{},
		approved: map[string]bool{},
	}
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
	for _, app := range apps {
		if slices.Contains(onBench, app.Name) || hasHostApp(e.Dir, app.Name) {
			continue
		}
		if err := m.preflightSource(ctx, bench, app.Source, br, log); err != nil {
			return nil, err
		}
	}
	return br, nil
}

func (m *Manager) preflightSource(ctx context.Context, bench *frappe.Bench, source string, br *bridge, log *createLog) error {
	// A tamp.toml from before the bridge can carry an ssh source the flag
	// parser now refuses; the fix is rewriting it, not a reachability answer.
	if https, ssh := httpsFormOfSSH(source); ssh {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("this environment's %s records the ssh app source %s, which tamp cannot fetch", ConfigFile, redactedURL(source)),
			fmt.Sprintf("edit %s: replace the source with %s", ConfigFile, https))
	}

	refusal, err := remoteRefusal(bench.CheckRemote(ctx, source, nil))
	if err != nil || refusal == nil {
		return err
	}
	if !authShaped(refusal.Output) {
		return unreachableSource(refusal, log)
	}

	// Auth-shaped: the repository looks private (a host that hides private
	// repositories answers exactly like this for a missing one, too).
	cred, err := br.credential(ctx, source, log)
	if err != nil {
		return err
	}

	refusal, err = remoteRefusal(bench.CheckRemote(ctx, source, credentialEnv(cred)))
	if err == nil && refusal == nil {
		br.needs[source] = true
		return nil
	}
	if err != nil {
		return err
	}
	if !authShaped(refusal.Output) {
		return unreachableSource(refusal, log)
	}
	br.reject(ctx, source)
	return refusedCredential(source, cred.Host)
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
func (b *bridge) reject(ctx context.Context, source string) {
	_, host, _, err := sourceParts(source)
	if err != nil {
		return
	}
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

// authShaped reports whether git's output is a credential demand rather than
// any other failure — the trigger for the bridge.
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
