package env

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/frappe"
	"github.com/zhide915/tamp/internal/ui"
)

// SnapshotRestoreRequest is what `tamp snapshot restore` was asked for.
type SnapshotRestoreRequest struct {
	Env string
	// Name empty means the newest snapshot.
	Name string
	// Yes confirms overwriting site data that is there now.
	Yes bool
}

// SnapshotRestore brings a snapshot's sites back, recreating the ones that
// are gone.
func (m *Manager) SnapshotRestore(ctx context.Context, req SnapshotRestoreRequest) error {
	e, err := m.resolve(req.Env)
	if err != nil {
		return err
	}
	if err := m.requireRunning(ctx, e, "so tamp cannot restore into it"); err != nil {
		return err
	}
	manifest, err := m.chooseSnapshot(e, req.Name)
	if err != nil {
		return err
	}

	present, _, err := m.sites(ctx, e)
	if err != nil {
		return err
	}
	bench := e.bench(m.Engine, m.Out.Stream())
	// The restore is bench choreography, and bench runs from the virtualenv
	// an earlier deps clean may have emptied.
	if err := m.requireDeps(ctx, e, bench, "a restore"); err != nil {
		return err
	}
	overwritten, err := m.preflight(ctx, e, bench, manifest, present, req.Yes)
	if err != nil {
		return err
	}

	// Claimed under the machine lock: the preflight's lockless answer can go
	// stale during a long restore, and a hostname claimed twice would put one
	// address in the Caddyfile twice.
	clashes, err := ClaimHosts(m.Home, e.Name(), manifest.hosts())
	if err != nil {
		return err
	}
	if len(clashes) > 0 {
		return m.hostsGivenAway(manifest, clashes)
	}

	steps := m.Out.Steps(len(manifest.Sites) + 2)
	if err := m.restoreSites(ctx, e, bench, manifest, present, steps); err != nil {
		// The bench is the authority on what the failure left behind;
		// re-asking reconciles the ledger's claims with the sites that exist.
		if _, _, rerr := m.sites(ctx, e); rerr != nil {
			m.Out.Warn(fmt.Sprintf("could not re-check %s's sites after the failure: %v", e.Name(), rerr))
		}
		return err
	}

	// Re-asked because the bench is the authority: recording its site list is
	// what gives the restored sites their routes back.
	steps.Step("routing the restored sites")
	if _, _, err := m.sites(ctx, e); err != nil {
		return err
	}
	status, err := m.refreshRoutes(ctx)
	if err != nil {
		return err
	}

	m.Out.OK(fmt.Sprintf("restored %s into %s: %d site(s)", manifest.Name, e.Name(), len(manifest.Sites)))
	for _, site := range manifest.Sites {
		m.Out.Note("site: " + status.URL(site.Host))
	}
	m.Out.Note("each site's Administrator password is the one the snapshot was taken with")
	m.notePendingHostsEntries(manifest.hosts())
	if len(overwritten) > 0 {
		m.Out.Note("the data that was there for " + strings.Join(overwritten, ", ") + " is gone")
	}
	return nil
}

// restoreSites unpacks the bundle and brings every site back, recreating the
// ones that are gone.
func (m *Manager) restoreSites(
	ctx context.Context,
	e *Environment,
	bench *frappe.Bench,
	manifest snapshotManifest,
	present []string,
	steps *ui.Stepper,
) error {
	steps.Step("unpacking " + manifest.Name + " into " + e.Name().String())
	if err := m.unpackSnapshot(ctx, e, bench, manifest); err != nil {
		return err
	}
	// Deferred from here on: what the staging area holds now is a whole copy
	// of every site, and every path out must drop it.
	defer func() {
		if err := bench.ClearStage(ctx); err != nil {
			m.Out.Warn(fmt.Sprintf("could not clear the staging area after the restore: %v", err))
		}
	}()

	password, err := ReadDBRootPassword(e.Dir)
	if err != nil {
		return err
	}
	for _, site := range manifest.Sites {
		steps.Step("restoring " + site.Host + " and migrating it")
		if !slices.Contains(present, site.Host) {
			// The site's own admin password comes back with its database, so
			// the one bench wants here is thrown away seconds later.
			if err := bench.NewSite(ctx, frappe.NewSiteRequest{
				Host:           site.Host,
				DBRootPassword: password,
				AdminPassword:  rand.Text(),
			}); err != nil {
				return err
			}
		}
		if err := bench.RestoreSite(ctx, site.Host, password); err != nil {
			return err
		}
		if err := bench.Migrate(ctx, site.Host); err != nil {
			return err
		}
	}
	return nil
}

// chooseSnapshot resolves --name, or the newest snapshot when there is none.
func (m *Manager) chooseSnapshot(e *Environment, name string) (snapshotManifest, error) {
	if name != "" {
		if _, err := parseSnapshotName(name); err != nil {
			return snapshotManifest{}, err
		}
		return m.readSnapshot(e, name)
	}

	held, err := m.snapshots(e)
	if err != nil {
		return snapshotManifest{}, err
	}
	if len(held) == 0 {
		return snapshotManifest{}, exitcode.New(exitcode.CodeNotFound,
			fmt.Sprintf("%s has no snapshots to restore", e.Name()),
			fmt.Sprintf("take one first: tamp snapshot create %s", e.Name()))
	}
	return held[0], nil
}

// preflight answers every question a restore could fail on while the bench is
// still untouched, and returns the sites whose current data the restore would
// write over.
func (m *Manager) preflight(
	ctx context.Context,
	e *Environment,
	bench *frappe.Bench,
	manifest snapshotManifest,
	present []string,
	yes bool,
) ([]string, error) {
	if err := m.requireSnapshotApps(ctx, e, bench, manifest); err != nil {
		return nil, err
	}
	if err := m.requireFreeHostnames(e, manifest); err != nil {
		return nil, err
	}

	var overwritten []string
	for _, host := range manifest.hosts() {
		if slices.Contains(present, host) {
			overwritten = append(overwritten, host)
		}
	}
	if len(overwritten) > 0 && !yes {
		m.previewRestore(e, manifest, present, overwritten)
		return nil, exitcode.New(exitcode.CodeConfirmationRequired,
			fmt.Sprintf("restoring %s would write over site data in %s", manifest.Name, e.Name()),
			"add --yes once the list above is what you meant")
	}
	return overwritten, nil
}

// requireSnapshotApps refuses a restore the bench cannot carry. Like site
// creation, tamp names the apps and leaves the branch to the user — it is the
// one part tamp cannot supply.
func (m *Manager) requireSnapshotApps(ctx context.Context, e *Environment, bench *frappe.Bench, manifest snapshotManifest) error {
	onBench, err := bench.Apps(ctx)
	if err != nil {
		return err
	}

	var missing []string
	for _, site := range manifest.Sites {
		for _, app := range site.Apps {
			if !slices.Contains(onBench, app) && !slices.Contains(missing, app) {
				missing = append(missing, app)
			}
		}
	}
	if len(missing) == 0 {
		return nil
	}

	m.Out.Print(fmt.Sprintf("%s needs these apps to restore %s, and its bench does not have them:",
		e.Name(), manifest.Name))
	for _, app := range missing {
		m.Out.Print("  " + app)
		m.Out.Hint(fmt.Sprintf("  fetch it: tamp exec %s -- bench get-app %s --branch <branch>", e.Name(), app))
	}
	return exitcode.New(exitcode.CodeFailed,
		fmt.Sprintf("%s cannot restore %s: the bench is missing %s",
			e.Name(), manifest.Name, strings.Join(missing, ", ")),
		"fetch the apps above onto the bench first — nothing was restored")
}

// requireFreeHostnames refuses to recreate a site whose hostname the machine
// has since given to someone else: hostnames are unique across every
// environment, and a duplicate address takes every route down.
func (m *Manager) requireFreeHostnames(e *Environment, manifest snapshotManifest) error {
	clashes, err := HostConflicts(m.Home, e.Name(), manifest.hosts())
	if err != nil {
		return err
	}
	if len(clashes) == 0 {
		return nil
	}
	return m.hostsGivenAway(manifest, clashes)
}

func (m *Manager) hostsGivenAway(manifest snapshotManifest, clashes []HostClaim) error {
	m.Out.Print(fmt.Sprintf("%s cannot take back, because this machine gave the name away:", manifest.Name))
	for _, c := range clashes {
		m.Out.Print(fmt.Sprintf("  %s  now %s of %q", c.Host, c.What, c.Owner))
	}
	return exitcode.New(exitcode.CodeFailed,
		fmt.Sprintf("%s holds hostnames that belong to another environment now", manifest.Name),
		"remove the other site, or rename that environment — nothing was restored")
}

// previewRestore prints exactly what --yes would destroy — the point of the
// exit-5 contract.
func (m *Manager) previewRestore(e *Environment, manifest snapshotManifest, present, overwritten []string) {
	m.Out.Print(fmt.Sprintf("restoring %s would replace, in %s:", manifest.Name, e.Name()))
	for _, host := range overwritten {
		m.Out.Print("  data    " + host + "  (its database and files, with the snapshot's)")
	}

	m.Out.Print("")
	m.Out.Print("it would keep:")
	m.Out.Print(keptSource(e))
	for _, host := range present {
		if !slices.Contains(overwritten, host) {
			m.Out.Print("  data    " + host + "  (the snapshot says nothing about it)")
		}
	}
	m.Out.Print("")
	m.Out.Print("to restore:")
	m.Out.Print("  tamp snapshot restore " + e.Name().String() + " --name " + manifest.Name + " --yes")
}

func (m *Manager) unpackSnapshot(ctx context.Context, e *Environment, bench *frappe.Bench, manifest snapshotManifest) error {
	path := snapshotBundlePath(e.Dir, manifest.Name)
	file, err := os.Open(path)
	if err != nil {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot read %s: %v", path, err),
			fmt.Sprintf("see 'tamp snapshot list %s' for the snapshots it has", e.Name()))
	}
	defer func() { _ = file.Close() }()

	return bench.UnpackSnapshot(ctx, file)
}
