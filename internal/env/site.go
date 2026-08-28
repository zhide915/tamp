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

// siteSteps is the numbered step count before the per-app steps.
const siteSteps = 3

// SiteNewRequest is what `tamp site new` was asked for.
type SiteNewRequest struct {
	// Env is empty for the environment the user is inside.
	Env  string
	Host string
	// Apps names apps already fetched onto the bench, comma-separated.
	Apps string
	// AdminPassword empty means tamp generates one and prints it.
	AdminPassword string
}

// SiteNew creates a site and routes it. The hostname is the site's name, its
// directory on the bench and the route the router matches, so it must be
// unique across every environment on the machine.
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
	// Checked before anything exists: an app missing from the bench would
	// fail the create halfway, leaving a site with only some of its apps.
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
		// Alphanumeric, like the DB credential, so nothing between here and a
		// login form mangles it.
		admin = rand.Text()
	}

	// Claimed under the machine lock before the site is made, so two
	// concurrent creates cannot both find the hostname free. A failed create
	// gives the claim back.
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

	// Set per-site as well as bench-wide: the per-site key survives whatever
	// later happens to the shared file, and it is what makes Frappe reload
	// changed code.
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

// revealAdmin prints only a password tamp generated — echoing back a typed
// one puts it in a second place for no one's benefit.
func (m *Manager) revealAdmin(generated bool, admin string) {
	if !generated {
		return
	}
	m.Out.Note("Administrator password: " + admin)
	m.Out.Note("tamp generated it and prints it this once — it is not stored anywhere")
}

// salvageSite handles a failure after `bench new-site` succeeded: the site
// exists and keeps its claim, so print the generated password — only this run
// knows it — and route what is there, so the site being repaired is
// reachable.
func (m *Manager) salvageSite(ctx context.Context, generated bool, admin string, err error) error {
	m.revealAdmin(generated, admin)
	if _, rerr := m.applyRoutes(ctx, m.Out.Stream()); rerr != nil {
		m.Out.Warn("the new site is not routed yet: " + rerr.Error())
	}
	return err
}

// requireApps refuses rather than fetches: tamp cannot know which branch of
// an app this bench wants, and the hint leaves the branch for the user —
// exactly the part tamp cannot supply.
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

// warnUnresolvable covers hostnames outside .localhost, which resolve to
// whatever the internet says. Until tamp manages the hosts file, the warning
// gives the exact line to add.
func (m *Manager) warnUnresolvable(host Host) {
	if host.IsLocal() {
		return
	}
	m.Out.Warn(host.String() + " is not a .localhost name, so nothing on this machine resolves it yet")
	m.Out.Note("tamp will manage your hosts file in a later release; for now add this line to " + hostsFile() + ":")
	m.Out.Note("  127.0.0.1  " + host.String())
}

func hostsFile() string {
	if runtime.GOOS == "windows" {
		return `C:\Windows\System32\drivers\etc\hosts`
	}
	return "/etc/hosts"
}

func (m *Manager) claimHost(e *Environment, host Host) error {
	return ClaimHost(m.Home, e.Name(), host.String())
}

// unclaimHost failure is a warning, not an error: it runs while a more useful
// outcome is already being reported.
func (m *Manager) unclaimHost(e *Environment, host Host) {
	if err := ReleaseHost(m.Home, e.Name(), host.String()); err != nil {
		m.Out.Warn(fmt.Sprintf("could not take %s back out of the registry: %v", host, err))
	}
}

// SiteRemoveRequest is what `tamp site rm` was asked for.
type SiteRemoveRequest struct {
	Env  string
	Host string
	// Yes replaces a prompt — agents run these commands, so confirmation is a
	// flag.
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

	// Asked of the bench when up, so a site created through 'tamp exec' is
	// still removable by name.
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
	// Routes come from the registry, so this removes only this site's route.
	if _, err := m.refreshRoutes(ctx); err != nil {
		return err
	}

	m.Out.OK(host.String() + " removed from " + e.Name().String())
	m.Out.Note("its database is gone; every other site on this bench is untouched")
	m.Out.Note("bench backed the site up and moved its files into " + frappe.ArchivedSitesDir)
	return nil
}

// previewSiteRemoval prints exactly what --yes would destroy — the point of
// the exit-5 contract.
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
	// Apps is nil when tamp could not ask.
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
	// Status carries tamp's recorded port even when the engine is down, so
	// the URLs stay right; only no port at all leaves nothing to build from.
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

// sites prefers the running bench — its sites/ directory is what Frappe
// resolves against — and writes its answer back to the registry, so a site
// created through 'tamp exec' still gets routed. A stopped environment
// answers from the cache, which is why the cache exists.
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

func (m *Manager) knownSites(e *Environment) ([]string, error) {
	reg, err := LoadRegistry(m.Home)
	if err != nil {
		return nil, err
	}
	return reg[e.Name().String()].Sites, nil
}

// recordSites also reports the hostnames the ledger refused to route.
func (m *Manager) recordSites(e *Environment, hosts []string) error {
	routable := make([]string, 0, len(hosts))
	for _, host := range hosts {
		// A malformed directory name must not reach the Caddyfile: one bad
		// address there takes down every route on the machine.
		if _, err := ParseHost(host); err != nil {
			m.Out.Warn(fmt.Sprintf(
				"%s is on %s's bench but is not a hostname tamp can route, so tamp is not routing it",
				host, e.Name()))
			continue
		}
		routable = append(routable, host)
	}
	skipped, err := RecordSites(m.Home, e.Name(), routable)
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
