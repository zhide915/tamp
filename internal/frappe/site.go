package frappe

import (
	"bytes"
	"context"
	"strings"

	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/toolchain"
)

// SiteDir is where one site's own files live.
//
// The directory is named exactly as the site's hostname because that is how
// Frappe finds it: with no default site forced, the site is resolved from the
// Host header of the request. Nothing here ever writes currentsite.txt — a
// bench with a default site would answer for it whatever hostname was asked
// for, which is the opposite of what the router is doing.
func SiteDir(host string) string { return SitesDir + "/" + host }

// ArchivedSitesDir is where bench puts a site it has dropped.
//
// Dropping a site takes its database away for good and moves its files here,
// with a final backup alongside them. tamp reports that rather than claiming
// the files are gone: they are still on the bench's disk, and someone who
// removed the wrong site needs to know that before they stop looking.
const ArchivedSitesDir = BenchDir + "/archived/sites"

// NewSiteRequest is one site to create on the bench.
type NewSiteRequest struct {
	// Host is the site's hostname, which is also its name and its directory.
	Host string
	// DBRootPassword is the environment's own MariaDB root credential: the
	// site's database is created with it, inside this bench's MariaDB.
	DBRootPassword string
	// AdminPassword is what the site's Administrator will log in with.
	AdminPassword string
}

// NewSite creates a site, and with it one database inside the environment's
// MariaDB.
func (b *Bench) NewSite(ctx context.Context, req NewSiteRequest) error {
	return b.run(ctx, newSiteScript, req.Host, req.DBRootPassword, req.AdminPassword)
}

// newSiteScript creates the site non-interactively.
//
// The login-scope flag is what makes this work in containers at all: MariaDB
// grants are per host, and the bench connects from its own container's address
// rather than from the database's localhost, so a site created without it
// cannot reach the database it was just given.
//
// Both passwords are supplied so that bench asks for neither. tamp is often
// driven by an agent, and a command that stops to prompt is a command that
// hangs.
const newSiteScript = `
set -eo pipefail
. ` + toolchain.EnvScript + `
bench new-site "$1" \
  --mariadb-user-host-login-scope=% \
  --db-root-password "$2" \
  --admin-password "$3"
`

// SetSiteConfig writes one key into a single site's own configuration.
func (b *Bench) SetSiteConfig(ctx context.Context, host, key, value string) error {
	return b.run(ctx, setConfigScript, host, key, value)
}

// setConfigScript sets a per-site key.
//
// -p parses the value as a Python literal, so developer_mode lands as the
// number 1 rather than the string "1" — the same shape the bench-wide config
// tamp writes has, and the shape anything reading it back expects.
const setConfigScript = `
set -eo pipefail
. ` + toolchain.EnvScript + `
bench --site "$1" set-config -p "$2" "$3"
`

// DropSite destroys one site: its database, and its files.
//
// Only that site. Every other site on the bench keeps its own database, which
// is the whole reason one site is one database rather than one bench being
// one.
func (b *Bench) DropSite(ctx context.Context, host, dbRootPassword string) error {
	return b.run(ctx, dropSiteScript, host, dbRootPassword)
}

// dropSiteScript drops the site.
//
// --force because the user has already confirmed the removal on the command
// line: a site whose database is half-broken is exactly the one they are
// trying to get rid of, and a refusal here would leave them nothing but a
// manual DROP DATABASE.
const dropSiteScript = `
set -eo pipefail
. ` + toolchain.EnvScript + `
bench drop-site "$1" --db-root-password "$2" --force
`

// Sites lists the bench's sites.
//
// This is the authority whenever the environment is running: the bench's
// sites/ directory is what Frappe itself resolves against, so anything tamp
// has cached elsewhere is only a record of what it last saw here.
func (b *Bench) Sites(ctx context.Context) ([]string, error) {
	out, err := b.capture(ctx, listSitesScript)
	if err != nil {
		return nil, err
	}
	return lines(out), nil
}

// listSitesScript names the directories under sites/ that are sites.
//
// A site is a directory with a site config in it, which is what separates one
// from the bench's own assets/ and logs/. The glob matching nothing leaves the
// literal pattern in place, and the config test rejects it like anything else.
const listSitesScript = `
cd ` + SitesDir + `
for entry in */; do
  if [ -f "${entry}site_config.json" ]; then
    printf '%s\n' "${entry%/}"
  fi
done
exit 0
`

// InstalledApps lists the apps installed on one site.
//
// It asks the site rather than the bench: apps are fetched onto a bench once
// and installed per site, so two sites on one bench legitimately run different
// sets of them.
func (b *Bench) InstalledApps(ctx context.Context, host string) ([]string, error) {
	out, err := b.capture(ctx, listAppsScript, host)
	if err != nil {
		return nil, err
	}
	return parseApps(out), nil
}

// parseApps reads the app names out of what bench list-apps printed.
//
// bench has printed this as a bare name and as "name version branch" across
// releases, and tamp wants the same answer from both: the name is the first
// field either way.
func parseApps(out string) []string {
	apps := make([]string, 0, 4)
	for _, line := range lines(out) {
		apps = append(apps, strings.Fields(line)[0])
	}
	return apps
}

const listAppsScript = `
set -eo pipefail
. ` + toolchain.EnvScript + `
bench --site "$1" list-apps
`

// capture runs a script inside the bench container and returns what it wrote
// to standard output. Its standard error still goes to the caller's stream, so
// a command that failed has said why by the time the error is returned.
func (b *Bench) capture(ctx context.Context, script string, args ...string) (string, error) {
	var out bytes.Buffer
	err := b.Engine.Exec(ctx, engine.ExecRequest{
		Container: b.Container,
		Cmd:       engine.Script(script, args...),
		WorkDir:   BenchDir,
		Stdout:    &out,
		Stderr:    b.Out,
	})
	return out.String(), err
}

// lines splits captured output into the non-empty lines it holds.
func lines(out string) []string {
	var got []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			got = append(got, line)
		}
	}
	return got
}
