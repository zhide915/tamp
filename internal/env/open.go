package env

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/router"
)

// MailTarget is the literal argument that opens the environment's mail UI
// instead of a site.
const MailTarget = "mail"

// OpenRequest is what `tamp open` was asked for.
type OpenRequest struct {
	// Env is empty for the environment the user is inside.
	Env string
	// Target is a site's hostname, MailTarget, or empty for the
	// environment's first site.
	Target string
}

// ParseOpenArgs reads `tamp open`'s optional [env] and [host|mail]. One
// argument is a target when it could not be an environment name: 'mail',
// which is a reserved name, or a hostname, which always carries a dot where
// an environment name never may.
func ParseOpenArgs(args []string) OpenRequest {
	switch len(args) {
	case 0:
		return OpenRequest{}
	case 1:
		if args[0] == MailTarget || strings.Contains(args[0], ".") {
			return OpenRequest{Target: args[0]}
		}
		return OpenRequest{Env: args[0]}
	default:
		return OpenRequest{Env: args[0], Target: args[1]}
	}
}

// Open hands one of an environment's URLs to the default browser. Nothing is
// opened until the address is known to work: a stopped environment serves
// nothing, and a custom domain with no hosts entry resolves nowhere.
func (m *Manager) Open(ctx context.Context, req OpenRequest) error {
	e, err := m.resolve(req.Env)
	if err != nil {
		return err
	}
	if err := m.requireRunning(ctx, e, "so nothing it serves would answer"); err != nil {
		return err
	}

	// Status carries tamp's recorded port even when the router is down, so
	// the URL stays right; only no port at all leaves nothing to build from.
	status, err := m.router().Status(ctx)
	if err != nil && status.Port == 0 {
		return err
	}
	if req.Target == MailTarget {
		return m.launch(ctx, e, status, MailURL(e.Name(), status))
	}

	hosts, _, err := m.sites(ctx, e)
	if err != nil {
		return err
	}
	host, err := chooseSite(e, hosts, req.Target)
	if err != nil {
		return err
	}
	if hostEntryState(host, m.hostsEntries()) == hostEntryPending {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("%s has no line in %s yet, so nothing on this machine resolves it", host, m.HostsFile),
			"write it with 'tamp hosts sync', then open the site again")
	}
	return m.launch(ctx, e, status, status.URL(host))
}

// chooseSite resolves the target argument against the sites the bench has.
func chooseSite(e *Environment, hosts []string, target string) (string, error) {
	if target == "" {
		if len(hosts) == 0 {
			return "", exitcode.New(exitcode.CodeFailed,
				fmt.Sprintf("%s has no sites to open", e.Name()),
				fmt.Sprintf("create one: tamp site new %s <host>", e.Name()))
		}
		return hosts[0], nil
	}
	if !slices.Contains(hosts, target) {
		return "", exitcode.New(exitcode.CodeNotFound,
			fmt.Sprintf("%s has no site %s", e.Name(), target),
			fmt.Sprintf("see 'tamp site list %s' for the sites it has", e.Name()))
	}
	return target, nil
}

// launch opens the URL, once the last thing between it and an answer — the
// machine's one router — is known to be up.
func (m *Manager) launch(ctx context.Context, e *Environment, status router.Status, url string) error {
	if !status.Running {
		return exitcode.New(exitcode.CodeFailed,
			"the router is not running, so nothing on this machine answers "+url,
			fmt.Sprintf("bring it back with 'tamp start %s'", e.Name()))
	}
	if err := m.Browser(ctx, url); err != nil {
		return err
	}
	m.Out.OK("opening " + url)
	return nil
}
