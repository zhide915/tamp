package env

import (
	"context"
	"crypto/rand"
	"fmt"
	"runtime"
	"slices"
	"strings"

	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/frappe"
)

// siteSteps is how many numbered steps creating a site prints before its apps
// are counted in — one step each.
const siteSteps = 3

// SiteNewRequest is what `tamp site new` was asked for.
type SiteNewRequest struct {
	// Env names the environment, or is empty for the one the user is inside.
	Env string
	// Host is the site's hostname, still unvalidated.
	Host string
	// Apps is the --apps value, still unvalidated: a comma-separated list of
	// apps to install, each already fetched onto the bench.
	Apps string
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
	apps, err := ParseInstallApps(req.Apps)
	if err != nil {
		return err
	}
	bench := e.bench(m.Engine, m.Out.Stream())
	// Before the site exists, before the hostname is claimed, before anything
	// at all: an app that is not on the bench is a create that would fail
	// halfway and leave a site with some of the apps asked for.
	if err := m.requireApps(ctx, e, bench, apps); err != nil {
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

	steps := m.Out.Steps(siteSteps + len(apps))
	steps.Step("creating " + host.String() + " and its database")
	if err := bench.NewSite(ctx, frappe.NewSiteRequest{
		Host:           host.String(),
		DBRootPassword: password,
		AdminPassword:  admin,
	}); err != nil {
		m.unclaimHost(e, host)
		return err
	}

	for _, app := range apps {
		steps.Step("installing " + app + " on " + host.String())
		if err := bench.InstallApp(ctx, host.String(), app); err != nil {
			return m.salvageSite(ctx, generated, admin, err)
		}
	}

	// The bench-wide config already says this, and the site says it again for
	// itself: a per-site key survives whatever later happens to the shared
	// file, and this is the setting that makes Frappe reload changed code.
	steps.Step("turning on developer mode")
	if err := bench.SetSiteConfig(ctx, host.String(), "developer_mode", "1"); err != nil {
		return m.salvageSite(ctx, generated, admin, err)
	}

	steps.Step("routing " + host.String())
	status, err := m.applyRoutes(ctx, m.Out.Stream())
	if err != nil {
		m.revealAdmin(generated, admin)
		return err
	}

	m.Out.OK(host.String() + " is ready on " + e.Name().String())
	m.Out.Note("site: " + status.URL(host.String()))
	m.revealAdmin(generated, admin)
	m.warnUnresolvable(host)
	return nil
}

// revealAdmin prints a generated Administrator password. Only a password tamp
// invented is worth printing: echoing back the one the user typed puts it in a
// second place for no one's benefit.
func (m *Manager) revealAdmin(generated bool, admin string) {
	if !generated {
		return
	}
	m.Out.Note("Administrator password: " + admin)
	m.Out.Note("tamp generated it and prints it this once — it is not stored anywhere")
}

// salvageSite is the error path for a failure after `bench new-site` has
// succeeded: the site exists and keeps its hostname claim, so the one thing
// only this run knows — the generated password — is printed anyway, and the
// routes are reassembled so that the site the user is about to repair is at
// least reachable.
func (m *Manager) salvageSite(ctx context.Context, generated bool, admin string, err error) error {
	m.revealAdmin(generated, admin)
	if _, rerr := m.applyRoutes(ctx, m.Out.Stream()); rerr != nil {
		m.Out.Warn("the new site is not routed yet: " + rerr.Error())
	}
	return err
}

// requireApps refuses a site whose apps the bench does not have.
//
// It refuses rather than fetching, and it refuses before anything has been
// done: tamp has no way to know which branch of an app this bench wants, so
// guessing one would be the difference between a working site and a broken
// one. The hint is the command that settles it, with the branch left for the
// user to fill in because that is exactly the part tamp cannot supply.
func (m *Manager) requireApps(ctx context.Context, e *Environment, bench *frappe.Bench, apps []string) error {
	if len(apps) == 0 {
		return nil
	}
	onBench, err := bench.Apps(ctx)
	if err != nil {
		return err
	}

	var missing []string
	for _, app := range apps {
		if !slices.Contains(onBench, app) {
			missing = append(missing, app)
		}
	}
	if len(missing) == 0 {
		return nil
	}

	m.Out.Print(fmt.Sprintf("%s does not have, on its bench:", e.Name()))
	for _, app := range missing {
		m.Out.Print("  " + app)
		m.Out.Hint(fmt.Sprintf("  fetch it: tamp exec %s -- bench get-app %s --branch <branch>", e.Name(), app))
	}
	return exitcode.New(exitcode.CodeFailed,
		fmt.Sprintf("%s cannot install %s", e.Name(), strings.Join(missing, ", ")),
		"fetch the apps above onto the bench first — tamp will not guess a branch")
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

// claimHost records the hostname against this environment in the machine's
// claims ledger, which refuses one already given out.
func (m *Manager) claimHost(e *Environment, host Host) error {
	return ClaimHost(m.Home, e.Name(), host.String())
}

// unclaimHost takes a hostname back — because the site it was claimed for
// could not be made, or because that site has just been dropped.
//
// Its own failure is a warning rather than an error: it runs at the end of an
// operation whose outcome the user is already being told, and replacing that
// with a bookkeeping failure would bury the more useful message.
func (m *Manager) unclaimHost(e *Environment, host Host) {
	if err := ReleaseHost(m.Home, e.Name(), host.String()); err != nil {
		m.Out.Warn(fmt.Sprintf("could not take %s back out of the registry: %v", host, err))
	}
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
// actually holds, and says which sites the ledger refused to route and why.
func (m *Manager) recordSites(e *Environment, hosts []string) error {
	skipped, err := RecordSites(m.Home, e.Name(), hosts)
	for _, c := range skipped {
		m.Out.Warn(fmt.Sprintf(
			"%s is on %s's bench but is already %s of %q, so tamp is not routing it",
			c.Host, e.Name(), c.What, c.Owner))
	}
	return err
}

func (m *Manager) printSites(rows []siteRow) {
	table := make([][]string, 0, len(rows))
	for _, row := range rows {
		apps := unknownField
		if row.Apps != nil {
			apps = strings.Join(row.Apps, " ")
		}
		table = append(table, []string{row.Host, row.URL, apps})
	}
	m.Out.Table([]string{"HOST", "URL", "APPS"}, table)
}
