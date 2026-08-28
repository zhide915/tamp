package frappe

import (
	"context"

	"github.com/zhide915/tamp/internal/toolchain"
)

// AppsDir is the source layer: synced to the host, never deleted by tamp.
const AppsDir = BenchDir + "/apps"

// GetAppRequest describes an app to fetch onto the bench.
type GetAppRequest struct {
	// Source is the app's clone URL.
	Source string
	// Branch to fetch; empty means the repository's default.
	Branch string
}

// GetApp fetches an app onto the bench without installing it anywhere:
// apps are fetched per bench and installed per site.
func (b *Bench) GetApp(ctx context.Context, req GetAppRequest) error {
	return b.run(ctx, getAppScript, req.Source, req.Branch)
}

// bench treats an empty --branch as the literal name "", so the flag is
// passed only when a branch was given.
const getAppScript = `
set -eo pipefail
. ` + toolchain.EnvScript + `
if [ -n "$2" ]; then
  bench get-app --branch "$2" "$1"
else
  bench get-app "$1"
fi
`

// Apps lists the apps fetched onto the bench. The apps directory, not
// tamp.toml, is the authority: it also holds apps fetched through 'tamp exec'.
func (b *Bench) Apps(ctx context.Context) ([]string, error) {
	out, err := b.capture(ctx, listBenchAppsScript)
	if err != nil {
		return nil, err
	}
	return lines(out), nil
}

// An unmatched glob leaves the literal pattern in place; the -d test
// rejects it.
const listBenchAppsScript = `
cd ` + AppsDir + ` 2>/dev/null || exit 0
for entry in */; do
  if [ -d "$entry" ]; then
    printf '%s\n' "${entry%/}"
  fi
done
exit 0
`

// HandGitToHost configures each app repo so a clone made in the Linux
// container reads cleanly on Windows: filemode off (NTFS has no executable
// bit), autocrlf off (the container executes these files), longpaths on
// (Frappe paths exceed the 260-char default). .git syncs, so config written
// here reaches the host before its git ever runs.
func (b *Bench) HandGitToHost(ctx context.Context) error {
	return b.run(ctx, handGitToHostScript)
}

const handGitToHostScript = `
set -eo pipefail
cd ` + AppsDir + ` 2>/dev/null || exit 0
for entry in */; do
  app="${entry%/}"
  if [ -d "$app/.git" ]; then
    git -C "$app" config core.fileMode false
    git -C "$app" config core.autocrlf false
    git -C "$app" config core.longpaths true
  fi
done
`

// InstallApp installs an already-fetched app on the given site.
func (b *Bench) InstallApp(ctx context.Context, host, app string) error {
	return b.run(ctx, installAppScript, host, app)
}

const installAppScript = `
set -eo pipefail
. ` + toolchain.EnvScript + `
bench --site "$1" install-app "$2"
`
