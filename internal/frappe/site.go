package frappe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/toolchain"
)

// SiteDir returns a site's directory, named by hostname: Frappe resolves
// sites from the request's Host header, so tamp never sets a default site.
func SiteDir(host string) string { return SitesDir + "/" + host }

// ArchivedSitesDir is where bench drop-site parks a dropped site's files and
// final backup; the database is gone, the files are not.
const ArchivedSitesDir = ArchivedDir + "/sites"

// NewSiteRequest describes a site to create on the bench.
type NewSiteRequest struct {
	// Host is the site's hostname, which doubles as its name and directory.
	Host string
	// DBRootPassword is the environment's MariaDB root credential.
	DBRootPassword string
	// AdminPassword is for the site's Administrator user.
	AdminPassword string
}

// NewSite creates a site and its database in the environment's MariaDB.
func (b *Bench) NewSite(ctx context.Context, req NewSiteRequest) error {
	return b.run(ctx, newSiteScript, req.Host, req.DBRootPassword, req.AdminPassword)
}

// --mariadb-user-host-login-scope=% is required in containers: the bench
// connects from its own container's address, not the database's localhost.
// Both passwords are supplied so bench never prompts.
const newSiteScript = `
set -eo pipefail
. ` + toolchain.EnvScript + `
bench new-site "$1" \
  --mariadb-user-host-login-scope=% \
  --db-root-password "$2" \
  --admin-password "$3"
`

// SetSiteConfig writes one key into a single site's configuration.
func (b *Bench) SetSiteConfig(ctx context.Context, host, key, value string) error {
	return b.run(ctx, setConfigScript, host, key, value)
}

// -p parses the value as a Python literal, so numbers land as numbers.
const setConfigScript = `
set -eo pipefail
. ` + toolchain.EnvScript + `
bench --site "$1" set-config -p "$2" "$3"
`

// DropSite destroys one site's database and files; other sites keep theirs.
func (b *Bench) DropSite(ctx context.Context, host, dbRootPassword string) error {
	return b.run(ctx, dropSiteScript, host, dbRootPassword)
}

// --force: removal is already confirmed on the command line, and a site with
// a broken database is exactly the one being removed.
const dropSiteScript = `
set -eo pipefail
. ` + toolchain.EnvScript + `
bench drop-site "$1" --db-root-password "$2" --force
`

// Sites lists the bench's sites. The sites/ directory is what Frappe itself
// resolves against, so it is the authority over anything tamp cached.
func (b *Bench) Sites(ctx context.Context) ([]string, error) {
	out, err := b.capture(ctx, listSitesScript)
	if err != nil {
		return nil, err
	}
	return lines(out), nil
}

// A site is a directory holding site_config.json, which excludes assets/ and
// logs/. An unmatched glob's literal pattern fails the same test.
const listSitesScript = `
cd ` + SitesDir + `
for entry in */; do
  if [ -f "${entry}site_config.json" ]; then
    printf '%s\n' "${entry%/}"
  fi
done
exit 0
`

// InstalledApps lists one site's installed apps; sites on the same bench may
// run different sets.
func (b *Bench) InstalledApps(ctx context.Context, host string) ([]string, error) {
	out, err := b.capture(ctx, listAppsScript, host)
	if err != nil {
		return nil, err
	}
	return parseApps(out), nil
}

// parseApps reads app names from bench list-apps output, which has been both
// "name" and "name version branch" across releases; the first field is the name.
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

// capture runs a script in the container and returns its stdout. stderr still
// streams to b.Out, so a failure has explained itself before the error returns.
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

func lines(out string) []string {
	var got []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			got = append(got, line)
		}
	}
	return got
}

// SiteConfigPath returns the site's own config file, which names its database.
func SiteConfigPath(host string) string { return SiteDir(host) + "/site_config.json" }

// SiteDatabase reads the site's db_name from its config: Frappe derives it at
// creation and no bench command prints it.
func (b *Bench) SiteDatabase(ctx context.Context, host string) (string, error) {
	body, err := b.Engine.ReadFile(ctx, b.Container, SiteConfigPath(host))
	if err != nil {
		return "", err
	}

	var config struct {
		DBName string `json:"db_name"`
	}
	if err := json.Unmarshal(body, &config); err != nil {
		return "", exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("%s is not valid JSON: %v", SiteConfigPath(host), err),
			"the site's own configuration is damaged — repair it with 'tamp exec <env> -- cat "+SiteConfigPath(host)+"'")
	}
	return config.DBName, nil
}
