package env

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/frappe"
)

// preflightApps proves every source that will be fetched answers from inside
// the container, before the expensive bench build.
func (m *Manager) preflightApps(ctx context.Context, bench *frappe.Bench, apps []App, log *createLog) error {
	if len(apps) == 0 {
		return nil
	}
	onBench, err := bench.Apps(ctx)
	if err != nil {
		return err
	}
	for _, app := range apps {
		if slices.Contains(onBench, app.Name) {
			continue
		}
		if err := m.preflightSource(ctx, bench, app.Source, log); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) preflightSource(ctx context.Context, bench *frappe.Bench, source string, log *createLog) error {
	// A tamp.toml from before this check can carry an ssh source the flag
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
	// The honest error until the credential bridge (#29) lands: the host's
	// git credentials do not exist inside the container.
	return exitcode.New(exitcode.CodeFailed,
		fmt.Sprintf("%s looks private, and tamp cannot reach the host's git credentials inside the container", source),
		"fetch the app another way for now — the credential bridge will make this work")
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
// any other failure.
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
