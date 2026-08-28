package frappe

import (
	"context"

	"github.com/zhide915/tamp/internal/toolchain"
)

// AppsDir is where the bench keeps the apps fetched onto it. It is the source
// layer: the one directory tamp syncs to the host and never deletes.
const AppsDir = BenchDir + "/apps"

// GetAppRequest is one app to fetch onto the bench.
type GetAppRequest struct {
	// Source is the app's clone URL.
	Source string
	// Branch is the branch to fetch, or empty for the repository's default —
	// which tamp takes as an answer rather than filling in itself.
	Branch string
}

// GetApp fetches an app onto the bench. It installs the app to no site: apps
// are fetched once per bench and installed per site, which is what lets two
// sites on one bench run different sets of them.
func (b *Bench) GetApp(ctx context.Context, req GetAppRequest) error {
	return b.run(ctx, getAppScript, req.Source, req.Branch)
}

// getAppScript clones the app and installs its Python requirements.
//
// The branch flag is passed only when there is a branch, because bench reads
// an empty --branch as the literal branch name "" and fails to clone. No
// branch means the repository's own default, which is git's behaviour and so
// needs nothing said at all.
const getAppScript = `
set -eo pipefail
. ` + toolchain.EnvScript + `
if [ -n "$2" ]; then
  bench get-app --branch "$2" "$1"
else
  bench get-app "$1"
fi
`

// Apps lists the apps fetched onto the bench.
//
// The bench is the authority rather than tamp.toml: an app fetched through
// 'tamp exec ... -- bench get-app' is as present as one tamp fetched itself,
// and a site install that refused it would be refusing something that is there.
func (b *Bench) Apps(ctx context.Context) ([]string, error) {
	out, err := b.capture(ctx, listBenchAppsScript)
	if err != nil {
		return nil, err
	}
	return lines(out), nil
}

// listBenchAppsScript names the directories under apps/, which is where
// bench get-app puts every app it clones. The glob matching nothing leaves the
// literal pattern in place, and the directory test rejects it like anything else.
const listBenchAppsScript = `
cd ` + AppsDir + ` 2>/dev/null || exit 0
for entry in */; do
  if [ -d "$entry" ]; then
    printf '%s\n' "${entry%/}"
  fi
done
exit 0
`

// HandGitToHost settles the three git settings an app needs once it is cloned
// on Linux and read on a filesystem that cannot describe what Linux wrote.
//
// All three are tamp's mess to clean up. The clone happens in the container,
// so git records filemode = true; the host then compares an executable bit
// NTFS cannot store and calls every such file modified with nothing changed in
// it. Git for Windows converts line endings on checkout by default, which
// would put CRLF into files the Linux container is about to execute. And
// Frappe's own paths are long enough that an app a few directories down runs
// past the 260 characters Windows allows by default — which git reports the
// same way, as files it could not read and so assumes have changed.
//
// They are set in the container because that is where the repository is made
// and where git already is, and because .git syncs — so the host reads a
// config that is right for it before it ever runs a git command of its own.
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

// InstallApp installs one of the bench's apps onto one site.
func (b *Bench) InstallApp(ctx context.Context, host, app string) error {
	return b.run(ctx, installAppScript, host, app)
}

const installAppScript = `
set -eo pipefail
. ` + toolchain.EnvScript + `
bench --site "$1" install-app "$2"
`
