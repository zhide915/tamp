package env

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/frappe"
	"github.com/zhide915/tamp/internal/router"
)

// siteSteps is how many numbered steps creating a site prints.
const siteSteps = 3

// SiteNewRequest is what `tamp site new` was asked for.
type SiteNewRequest struct {
	// Env names the environment, or is empty for the one the user is inside.
	Env string
	// Host is the site's hostname, still unvalidated.
	Host string
	// AdminPassword is what --admin-password supplied. Empty means tamp
	// generates one and prints it.
	AdminPassword string
}

// SiteNew creates a site on an environment's bench and routes it.
//
// The hostname is the whole design: it is the site's name, its directory on
// the bench, and the route the router matches on, so it has to be unique
// across every environment on the machine rather than only within this one.
func (m *Manager) SiteNew(ctx context.Context, req SiteNewRequest) error {
	e, err := m.resolve(req.Env)
	if err != nil {
		return err
	}
	host, err := ParseHost(req.Host)
	if err != nil {
		return err
	}
	if err := m.requireRunning(ctx, e, "so tamp cannot create a site in it"); err != nil {
		return err
	}

	password, err := ReadDBRootPassword(e.Dir)
	if err != nil {
		return err
	}
	admin := req.AdminPassword
	generated := admin == ""
	if generated {
		// The same generator the database credential uses: alphanumeric, so
		// that nothing between here and a browser's login form mangles it.
		admin = rand.Text()
	}

	// The hostname is claimed before the site is made, under the machine lock,
	// so that two tamps creating the same site at once cannot both find it
	// free. A create that then fails gives the claim back.
	if err := m.claimHost(e, host); err != nil {
		return err
	}

	m.Out.Step(1, siteSteps, "creating "+host.String()+" and its database")
	bench := e.bench(m.Engine, m.Out.Stream())
	if err := bench.NewSite(ctx, frappe.NewSiteRequest{
		Host:           host.String(),
		DBRootPassword: password,
		AdminPassword:  admin,
	}); err != nil {
		m.unclaimHost(e, host)
		return err
	}

	// The bench-wide config already says this, and the site says it again for
	// itself: a per-site key survives whatever later happens to the shared
	// file, and this is the setting that makes Frappe reload changed code.
	m.Out.Step(2, siteSteps, "turning on developer mode")
	if err := bench.SetSiteConfig(ctx, host.String(), "developer_mode", "1"); err != nil {
		return err
	}

	m.Out.Step(3, siteSteps, "routing "+host.String())
	status, err := m.applyRoutes(ctx, m.Out.Stream())
	if err != nil {
		return err
	}

	m.Out.OK(host.String() + " is ready on " + e.Name().String())
	m.Out.Note("site: " + status.URL(host.String()))
	// Only a password tamp invented is worth printing: echoing back the one
	// the user typed puts it in a second place for no one's benefit.
	if generated {
		m.Out.Note("Administrator password: " + admin)
		m.Out.Note("tamp generated it and prints it this once — it is not stored anywhere")
	}
	m.warnUnresolvable(host)
	return nil
}

// warnUnresolvable says what a hostname outside .localhost still needs.
//
// *.localhost resolves to loopback in every evergreen browser with no
// configuration at all; anything else resolves to whatever the internet says,
// which is not this machine. tamp will manage the hosts file itself in a
// later release — until then the user is given the exact line to add, because
// a warning that does not say what to type is a warning that wastes a search.
func (m *Manager) warnUnresolvable(host Host) {
	if host.IsLocal() {
		return
	}
	m.Out.Warn(host.String() + " is not a .localhost name, so nothing on this machine resolves it yet")
	m.Out.Note("tamp will manage your hosts file in a later release; for now add this line to " + hostsFile() + ":")
	m.Out.Note("  127.0.0.1  " + host.String())
}

// hostsFile is where the OS keeps its static name resolution.
func hostsFile() string {
	if runtime.GOOS == "windows" {
		return `C:\Windows\System32\drivers\etc\hosts`
	}
	return "/etc/hosts"
}

// claimHost records the hostname against this environment, refusing one the
// machine has already given out.
//
// The check is global rather than per-environment because the router is: it
// matches one Host header against every environment's routes at once, so two
// environments claiming a hostname would produce a configuration Caddy refuses
// to load — taking every other site on the machine down with it.
func (m *Manager) claimHost(e *Environment, host Host) error {
	return m.updateSites(e, func(reg Registry, sites []string) ([]string, error) {
		if owner, what, claimed := hostClaimedBy(reg, "", host.String()); claimed {
			return nil, exitcode.New(exitcode.CodeFailed,
				fmt.Sprintf("%s is already %s of the environment %q", host, what, owner),
				"pick another hostname — 'tamp list' shows what every environment answers to")
		}
		return append(sites, host.String()), nil
	})
}

// unclaimHost takes a hostname back out of the registry — because the site it
// was claimed for could not be made, or because that site has just been
// dropped.
//
// Its own failure is a warning rather than an error: it runs at the end of an
// operation whose outcome the user is already being told, and replacing that
// with a bookkeeping failure would bury the more useful message.
func (m *Manager) unclaimHost(e *Environment, host Host) {
	err := m.updateSites(e, func(_ Registry, sites []string) ([]string, error) {
		return slices.DeleteFunc(sites, func(s string) bool { return s == host.String() }), nil
	})
	if err != nil {
		m.Out.Warn(fmt.Sprintf("could not take %s back out of the registry: %v", host, err))
	}
}

// updateSites rewrites one environment's cached site list under the machine
// lock, keeping it sorted.
//
// Every change to that list goes through here, because the list is what the
// router's routes are assembled from: reading it, deciding, and writing it
// back is a read-modify-write cycle, and doing it anywhere else is how a
// concurrent tamp loses a route.
func (m *Manager) updateSites(e *Environment, change func(Registry, []string) ([]string, error)) error {
	return UpdateRegistry(m.Home, func(reg Registry) error {
		name := e.Name().String()
		entry := reg[name]
		sites, err := change(reg, entry.Sites)
		if err != nil {
			return err
		}
		slices.Sort(sites)
		entry.Sites = sites
		reg[name] = entry
		return nil
	})
}

// hostClaimedBy reports what on this machine already answers to a hostname,
// ignoring self's own sites.
//
// Both kinds of route count. An environment's mail UI is as much a claim on a
// hostname as a site is, and a site created at mail.demo.localhost would be a
// second block for an address the router already has — which is why a mail
// hostname is a clash even for the environment that owns it.
//
// self is empty when nothing may hold the hostname yet, which is the question
// a fresh claim asks. It names an environment when the question is instead
// "may this environment go on holding it", as it is when tamp reconciles a
// bench's own site list against the registry.
func hostClaimedBy(reg Registry, self, host string) (owner, what string, claimed bool) {
	for _, name := range reg.Names() {
		if router.MailHost(name) == host {
			return name, "the mail UI", true
		}
		if name != self && slices.Contains(reg[name].Sites, host) {
			return name, "a site", true
		}
	}
	return "", "", false
}

// SiteRemoveRequest is what `tamp site rm` was asked for.
type SiteRemoveRequest struct {
	Env  string
	Host string
	// Yes confirms a destructive action. tamp never prompts: agents run these
	// commands, so confirmation is a flag, not a question.
	Yes bool
}

// SiteRemove drops one site — its database and its files — and nothing else.
func (m *Manager) SiteRemove(ctx context.Context, req SiteRemoveRequest) error {
	e, err := m.resolve(req.Env)
	if err != nil {
		return err
	}
	host, err := ParseHost(req.Host)
	if err != nil {
		return err
	}

	// The bench is asked whenever it is up, so a site created through 'tamp
	// exec' can still be dropped by name rather than reported as missing.
	known, _, err := m.sites(ctx, e)
	if err != nil {
		return err
	}
	if !slices.Contains(known, host.String()) {
		return exitcode.New(exitcode.CodeNotFound,
			fmt.Sprintf("%s has no site %s", e.Name(), host),
			fmt.Sprintf("see 'tamp site list %s' for the sites it has", e.Name()))
	}

	if !req.Yes {
		m.previewSiteRemoval(e, host)
		return exitcode.New(exitcode.CodeConfirmationRequired,
			fmt.Sprintf("removing %s is destructive", host),
			"add --yes once the list above is what you meant")
	}

	if err := m.requireRunning(ctx, e, "so tamp cannot drop a site from it"); err != nil {
		return err
	}
	password, err := ReadDBRootPassword(e.Dir)
	if err != nil {
		return err
	}
	if err := e.bench(m.Engine, m.Out.Stream()).DropSite(ctx, host.String(), password); err != nil {
		return err
	}

	m.unclaimHost(e, host)
	// The registry is what the routes are assembled from, so this is what
	// takes the site's route away and leaves every other one in place.
	if _, err := m.refreshRoutes(ctx); err != nil {
		return err
	}

	m.Out.OK(host.String() + " removed from " + e.Name().String())
	m.Out.Note("its database is gone; every other site on this bench is untouched")
	m.Out.Note("bench backed the site up and moved its files into " + frappe.ArchivedSitesDir)
	return nil
}

// previewSiteRemoval prints exactly what --yes would destroy. This is the
// whole value of the exit-5 contract: the answer to "what am I confirming" has
// to be on screen before the user retypes the command.
func (m *Manager) previewSiteRemoval(e *Environment, host Host) {
	m.Out.Print(fmt.Sprintf("tamp site rm would destroy, in %s:", e.Name()))
	m.Out.Print("  database  the one database behind " + host.String())
	m.Out.Print("  route     " + host.String())
	m.Out.Print("")
	m.Out.Print("it would keep:")
	m.Out.Print("  files     " + frappe.SiteDir(host.String()) + ", backed up and moved into " + frappe.ArchivedSitesDir)
	m.Out.Print("  every other site on this bench, with its own database")
}

// siteRow is one row of `tamp site list`.
type siteRow struct {
	Host string
	URL  string
	// Apps is what the site has installed, or nil when tamp could not ask.
	Apps []string
}

// SiteList reports an environment's sites.
func (m *Manager) SiteList(ctx context.Context, name string) error {
	e, err := m.resolve(name)
	if err != nil {
		return err
	}

	hosts, live, err := m.sites(ctx, e)
	if err != nil {
		return err
	}
	// The router's port is tamp's own record, and Status carries it back even
	// when the engine could not be reached — so the URLs below are right with
	// Docker stopped. Only a port tamp cannot read at all leaves nothing to
	// build a URL from, and that is the failure worth stopping for.
	status, err := m.router().Status(ctx)
	if err != nil && status.Port == 0 {
		return err
	}

	if len(hosts) == 0 {
		m.Out.Print("no sites yet")
		m.Out.Hint(fmt.Sprintf("create one: tamp site new %s <host>", e.Name()))
		return nil
	}

	rows := make([]siteRow, 0, len(hosts))
	for _, host := range hosts {
		row := siteRow{Host: host, URL: status.URL(host)}
		if live {
			apps, err := e.bench(m.Engine, m.Out.Stream()).InstalledApps(ctx, host)
			if err != nil {
				m.Out.Warn(fmt.Sprintf("could not ask %s which apps it has: %v", host, err))
			} else {
				row.Apps = apps
			}
		}
		rows = append(rows, row)
	}
	m.printSites(rows)
	return nil
}

// sites lists an environment's sites, and says whether the bench answered.
//
// A running bench is the authority — its sites/ directory is what Frappe
// itself resolves against — and what it says is written back to the registry,
// so a site created behind tamp's back through 'tamp exec' still ends up
// routed. A stopped environment is answered from that cache instead, which is
// the reason the cache exists.
func (m *Manager) sites(ctx context.Context, e *Environment) (hosts []string, live bool, err error) {
	cached, err := m.knownSites(e)
	if err != nil {
		return nil, false, err
	}

	running, err := m.benchRunning(ctx, e)
	if err != nil {
		m.Out.Warn("Docker is unreachable, so tamp is listing the sites it last saw")
		return cached, false, nil
	}
	if !running {
		m.Out.Warn(fmt.Sprintf("%s is not running, so tamp is listing the sites it last saw", e.Name()))
		return cached, false, nil
	}

	hosts, err = e.bench(m.Engine, m.Out.Stream()).Sites(ctx)
	if err != nil {
		return nil, false, err
	}
	if !slices.Equal(hosts, cached) {
		if err := m.recordSites(e, hosts); err != nil {
			return nil, false, err
		}
	}
	return hosts, true, nil
}

// knownSites is the site list tamp last recorded for an environment.
func (m *Manager) knownSites(e *Environment) ([]string, error) {
	reg, err := LoadRegistry(m.Home)
	if err != nil {
		return nil, err
	}
	return reg[e.Name().String()].Sites, nil
}

// recordSites replaces an environment's cached site list with what its bench
// actually holds.
func (m *Manager) recordSites(e *Environment, hosts []string) error {
	return m.updateSites(e, func(reg Registry, _ []string) ([]string, error) {
		kept := make([]string, 0, len(hosts))
		for _, host := range hosts {
			// A bench can hold a hostname the machine has already given to
			// something else — tamp did not create every site on it. Routing
			// that one would put the address in the Caddyfile twice, so tamp
			// leaves it unrouted and says which site is unreachable and why.
			if owner, what, claimed := hostClaimedBy(reg, e.Name().String(), host); claimed {
				m.Out.Warn(fmt.Sprintf(
					"%s is on %s's bench but is already %s of %q, so tamp is not routing it",
					host, e.Name(), what, owner))
				continue
			}
			kept = append(kept, host)
		}
		return kept, nil
	})
}

func (m *Manager) printSites(rows []siteRow) {
	// tabwriter needs the whole table before it can align it, so it is
	// rendered into a buffer and handed to the Printer line by line rather
	// than writing straight past it.
	var table bytes.Buffer
	w := tabwriter.NewWriter(&table, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "HOST\tURL\tAPPS")
	for _, row := range rows {
		apps := unknownField
		if row.Apps != nil {
			apps = strings.Join(row.Apps, " ")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", row.Host, row.URL, apps)
	}
	if err := w.Flush(); err != nil {
		m.Out.Warn(fmt.Sprintf("could not lay out the table: %v", err))
		return
	}

	for line := range strings.SplitSeq(strings.TrimRight(table.String(), "\n"), "\n") {
		m.Out.Print(line)
	}
}
