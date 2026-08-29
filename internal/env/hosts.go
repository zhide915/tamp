package env

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/hosts"
	"github.com/zhide915/tamp/internal/ui"
)

// wantedHosts are the hostnames tamp's block should carry: every site on
// every environment that does not resolve by itself. A mail UI is always a
// .localhost name, so it never needs an entry. The list comes from the
// registry's cached sites, which is what makes a sync work with every
// environment stopped.
func wantedHosts(reg Registry) []string {
	var want []string
	for _, name := range reg.Names() {
		want = append(want, customDomains(reg[name].Sites)...)
	}
	slices.Sort(want)
	return slices.Compact(want)
}

// customDomains are the hostnames that resolve nowhere without an entry.
func customDomains(hostnames []string) []string {
	var custom []string
	for _, host := range hostnames {
		if !Host(host).IsLocal() {
			custom = append(custom, host)
		}
	}
	return custom
}

// notePendingHostsEntries names the custom domains tamp's block does not
// carry yet. The sites are made and routed either way; only their names are
// pending, and one command supplies them.
func (m *Manager) notePendingHostsEntries(hostnames []string) {
	pending := hosts.Missing(m.hostsEntries(), customDomains(hostnames))
	if len(pending) == 0 {
		return
	}
	for _, host := range pending {
		m.Out.Warn(host + " is not a .localhost name, so nothing on this machine resolves it yet")
	}
	m.Out.Note("the hosts entry is pending — tamp writes it into its own block in " + m.HostsFile)
	m.Out.Hint("next: tamp hosts sync")
}

// HostsSync reconciles tamp's block with every custom domain on the machine.
func (m *Manager) HostsSync(ctx context.Context) error {
	reg, err := LoadRegistry(m.Home)
	if err != nil {
		return err
	}
	wanted := wantedHosts(reg)

	current, err := hosts.Read(m.HostsFile)
	if err != nil {
		return err
	}
	desired := hosts.Reconcile(current, wanted)
	if desired == current {
		m.Out.OK(m.HostsFile + " is already in sync")
		m.reportEntries(wanted)
		return nil
	}

	m.previewHostsChange(hosts.Entries(current), wanted)
	if err := m.writeHosts(ctx, current, desired); err != nil {
		return err
	}

	m.Out.OK("synced tamp's block in " + m.HostsFile)
	m.reportEntries(wanted)
	m.Out.Note("no line outside the block was changed")
	return nil
}

// writeHosts writes directly when it may, and elevates only when the system
// refuses — so a machine where the user already has the rights never sees a
// prompt.
func (m *Manager) writeHosts(ctx context.Context, current, desired string) error {
	err := hosts.Write(m.HostsFile, desired)
	if err == nil || !hosts.Denied(err) {
		return err
	}
	if m.HostsRedirected {
		// The redirect exists for tests; elevating would aim tamp's
		// privileges at a file the user picked, which is exactly what the
		// elevated half must never do.
		return err
	}
	return m.elevateHostsWrite(ctx, desired)
}

// pendingHostsFile is where the unprivileged half leaves what the privileged
// half should write. It lives under ~/.tamp because the elevated process is
// the same user on Windows and root on Unix, and both can read it there.
const pendingHostsFile = "hosts.pending"

func (m *Manager) elevateHostsWrite(ctx context.Context, desired string) error {
	exe, err := hosts.Self()
	if err != nil {
		return err
	}
	pending := filepath.Join(m.Home, pendingHostsFile)
	if err := os.WriteFile(pending, []byte(desired), 0o644); err != nil {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot stage the hosts file at %s: %v", pending, err),
			"check the permissions on your ~/.tamp directory")
	}
	defer func() { _ = os.Remove(pending) }()

	m.Out.Note(m.HostsFile + " belongs to the system, so tamp needs elevated rights for this one write")
	m.Out.Note("it runs 'tamp hosts apply', which writes tamp's block and exits — nothing else runs elevated")
	return hosts.Elevate(ctx, exe, []string{"hosts", "apply", pending}, m.Out.Stream())
}

// ApplyHostsFile is the elevated half of a sync: it writes a staged hosts
// file over target. Its one caller passes hosts.OSPath(), never anything the
// environment chose, and the content is refused unless the only difference
// from what is on disk is inside tamp's block — so the elevated step can do
// exactly one thing, whatever it is handed. target is a parameter so that
// refusal can be tested against a temp file.
//
// Deliberately not a Manager method, unlike everything else a command calls:
// under sudo the home directory is root's, and building a Manager there would
// create a root-owned ~/.tamp the user never asked for.
func ApplyHostsFile(out *ui.Printer, source, target string) error {
	staged, err := os.ReadFile(source)
	if err != nil {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot read the staged hosts file %s: %v", source, err),
			"run 'tamp hosts sync' rather than this command — tamp stages the file itself")
	}

	current, err := hosts.Read(target)
	if err != nil {
		return err
	}
	if !hosts.ChangesOnlyTheBlock(current, string(staged)) {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("refusing to write %s: %s would change lines outside tamp's block", target, source),
			"run 'tamp hosts sync' rather than this command — tamp stages the file itself")
	}
	if err := hosts.Write(target, string(staged)); err != nil {
		return err
	}
	out.OK("wrote tamp's block into " + target)
	return nil
}

// previewHostsChange names what the sync adds and drops, so the elevation
// prompt that may follow is not a blank cheque.
func (m *Manager) previewHostsChange(before, after []string) {
	for _, host := range hosts.Missing(before, after) {
		m.Out.Print("  + " + hosts.Loopback + "  " + host)
	}
	for _, host := range hosts.Missing(after, before) {
		m.Out.Print("  - " + hosts.Loopback + "  " + host)
	}
}

func (m *Manager) reportEntries(wanted []string) {
	if len(wanted) == 0 {
		m.Out.Note("no environment has a site outside .localhost, so the block is empty")
		return
	}
	m.Out.Note(fmt.Sprintf("%d custom domain(s) resolve to %s on this machine", len(wanted), hosts.Loopback))
}

// The hosts-entry states 'tamp site list' reports.
const (
	// hostEntryNotNeeded — a .localhost name resolves with no help.
	hostEntryNotNeeded = "not needed"
	hostEntryPresent   = "ok"
	hostEntryPending   = "pending"
)

// hostsEntries reads tamp's block, or reports none when the file cannot be
// read — an unreadable hosts file must not stop a site listing.
func (m *Manager) hostsEntries() []string {
	body, err := hosts.Read(m.HostsFile)
	if err != nil {
		m.Out.Warn(err.Error())
		return nil
	}
	return hosts.Entries(body)
}

func hostEntryState(host string, entries []string) string {
	if Host(host).IsLocal() {
		return hostEntryNotNeeded
	}
	if slices.Contains(entries, host) {
		return hostEntryPresent
	}
	return hostEntryPending
}
