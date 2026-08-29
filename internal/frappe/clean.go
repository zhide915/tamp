package frappe

import (
	"context"

	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/toolchain"
)

// ArchivedDir is what bench drop-site parks its output under; emptying it is
// part of the data layer, since that is whose remains it holds.
const ArchivedDir = BenchDir + "/archived"

// CleanDeps empties the deps layer. env/ is a volume mount point: its
// contents go and the directory stays, or the mount would be left dangling.
func (b *Bench) CleanDeps(ctx context.Context) error {
	return b.run(ctx, cleanDepsScript, EnvDir, AppsDir)
}

// -prune keeps find out of a directory it is about to delete. node_modules
// and __pycache__ sit inside apps/ but are build output, not source.
const cleanDepsScript = `
set -eo pipefail
if [ -d "$1" ]; then
  find "$1" -mindepth 1 -maxdepth 1 -exec rm -rf {} +
fi
if [ -d "$2" ]; then
  find "$2" -name node_modules -type d -prune -exec rm -rf {} +
  find "$2" -name __pycache__ -type d -prune -exec rm -rf {} +
fi
`

// HasDeps reports whether the deps layer is populated. bench itself runs
// from the virtualenv, so nothing bench can be asked until a rebuild puts it
// back.
func (b *Bench) HasDeps(ctx context.Context) (bool, error) {
	return engine.Probe(ctx, b.Engine, engine.ExecRequest{
		Container: b.Container,
		Cmd:       engine.Script(`test -x "$1"`, EnvDir+"/bin/python"),
	})
}

// AssetsDir is the assets layer: everything bench build writes, under the
// sites tree so it travels with the site files.
const AssetsDir = SitesDir + "/assets"

// CleanAssets empties the assets layer.
func (b *Bench) CleanAssets(ctx context.Context) error {
	return b.run(ctx, removeTreeScript, AssetsDir)
}

// ClearArchivedSites removes what drop-site archived. Part of the data layer:
// leaving it would keep the files of every site just destroyed.
func (b *Bench) ClearArchivedSites(ctx context.Context) error {
	return b.run(ctx, removeTreeScript, ArchivedDir)
}

const removeTreeScript = `
set -eo pipefail
rm -rf "$1"
`

// Build re-runs the asset build, restoring what an assets clean removed.
func (b *Bench) Build(ctx context.Context) error {
	return b.run(ctx, buildScript)
}

const buildScript = `
set -eo pipefail
. ` + toolchain.EnvScript + `
cd ` + BenchDir + `
bench build
`
