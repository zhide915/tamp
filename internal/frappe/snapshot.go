package frappe

import (
	"context"
	"io"

	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/toolchain"
)

// A snapshot is assembled here and then streamed straight out of the
// container to the host, so nothing but the staging area is ever written
// twice. The area sits under the sites tree on purpose: a fresh backup lands
// there too, so moving it in is a rename rather than a second copy of
// everything the site holds. The leading dot keeps it out of the site
// listing, which counts directories holding a site_config.json.
const snapshotWorkDir = SitesDir + "/.tamp-snapshot"

// The fixed names a staged site's parts take. bench names its output after
// the minute it ran, which a restore would then have to go looking for.
const (
	snapshotDatabase     = "database.sql.gz"
	snapshotPublicFiles  = "files.tar"
	snapshotPrivateFiles = "private-files.tar"
	snapshotSiteConfig   = "site_config.json"
)

// StageBackup backs one site up with its files and moves the result into the
// staging area under fixed names.
func (b *Bench) StageBackup(ctx context.Context, host string) error {
	return b.run(ctx, stageBackupScript, host, snapshotWorkDir)
}

// The newest dump is found by comparing the glob's matches rather than
// through 'ls | head': under pipefail, head closing the pipe early would fail
// the script for no reason.
const stageBackupScript = `
set -eo pipefail
. ` + toolchain.EnvScript + `
cd ` + BenchDir + `
bench --site "$1" backup --with-files

backups="sites/$1/private/backups"
newest=""
for candidate in "$backups"/*-database.sql.gz; do
  if [ -f "$candidate" ] && { [ -z "$newest" ] || [ "$candidate" -nt "$newest" ]; }; then
    newest="$candidate"
  fi
done
if [ -z "$newest" ]; then
  echo "bench backup left no database dump for $1 in $backups" >&2
  exit 1
fi

prefix="${newest%-database.sql.gz}"
stage="$2/$1"
rm -rf "$stage"
mkdir -p "$stage"
# Moved rather than copied: tamp made these files for this snapshot, and the
# snapshot is where they live from now on.
mv "$newest" "$stage/` + snapshotDatabase + `"
if [ -f "$prefix-files.tar" ]; then
  mv "$prefix-files.tar" "$stage/` + snapshotPublicFiles + `"
fi
if [ -f "$prefix-private-files.tar" ]; then
  mv "$prefix-private-files.tar" "$stage/` + snapshotPrivateFiles + `"
fi
if [ -f "$prefix-site_config_backup.json" ]; then
  mv "$prefix-site_config_backup.json" "$stage/` + snapshotSiteConfig + `"
fi
`

// BundleSnapshot writes the staged sites to out as one gzipped tar. gzip -1
// rather than zstd, for the same reason the template store uses it: zstd is
// not in the pinned bench image.
func (b *Bench) BundleSnapshot(ctx context.Context, out io.Writer) error {
	return b.stream(ctx, out, nil, bundleScript, snapshotWorkDir)
}

const bundleScript = `
set -eo pipefail
cd "$1"
tar -cf - . | gzip -1
`

// UnpackSnapshot reads a bundle from in and lays it out in the staging area.
func (b *Bench) UnpackSnapshot(ctx context.Context, in io.Reader) error {
	return b.stream(ctx, nil, in, unpackScript, snapshotWorkDir)
}

const unpackScript = `
set -eo pipefail
rm -rf "$1"
mkdir -p "$1"
tar -C "$1" -xzf -
`

// ClearSnapshotWork empties the staging area. Both a snapshot and a restore
// end with it: what it holds is a whole copy of the data layer.
func (b *Bench) ClearSnapshotWork(ctx context.Context) error {
	return b.run(ctx, clearSnapshotWorkScript, snapshotWorkDir)
}

const clearSnapshotWorkScript = `
set -eo pipefail
rm -rf "$1"
`

// RestoreSite replaces one existing site's database and files with the staged
// ones. The site has to be there already — bench restore drops and refills a
// database that a site config names.
func (b *Bench) RestoreSite(ctx context.Context, host, dbRootPassword string) error {
	return b.run(ctx, restoreSiteScript, host, snapshotWorkDir, dbRootPassword)
}

// --force: the confirmation happened at tamp's own prompt, and bench would
// otherwise ask again with nobody at the terminal. The file arguments are
// added only when the snapshot has them, so a database-only backup still
// restores.
const restoreSiteScript = `
set -eo pipefail
. ` + toolchain.EnvScript + `
cd ` + BenchDir + `
stage="$2/$1"
files=()
if [ -f "$stage/` + snapshotPublicFiles + `" ]; then
  files+=(--with-public-files "$stage/` + snapshotPublicFiles + `")
fi
if [ -f "$stage/` + snapshotPrivateFiles + `" ]; then
  files+=(--with-private-files "$stage/` + snapshotPrivateFiles + `")
fi
bench --site "$1" restore "$stage/` + snapshotDatabase + `" \
  --db-root-password "$3" \
  --force \
  "${files[@]}"

# The snapshot's encryption key, not the site's: every encrypted field in the
# database that just landed was written with it, and a site the restore
# recreated has a fresh one that decrypts none of them.
config="$stage/` + snapshotSiteConfig + `"
if [ -f "$config" ]; then
  key=$(env/bin/python -c 'import json,sys; print(json.load(open(sys.argv[1])).get("encryption_key",""))' "$config")
  if [ -n "$key" ]; then
    bench --site "$1" set-config encryption_key "$key"
  fi
fi
`

// Migrate brings a restored site's schema up to the code on the bench, which
// is what makes a snapshot from an older checkout usable.
func (b *Bench) Migrate(ctx context.Context, host string) error {
	return b.run(ctx, migrateScript, host)
}

const migrateScript = `
set -eo pipefail
. ` + toolchain.EnvScript + `
cd ` + BenchDir + `
bench --site "$1" migrate
`

// stream runs a script wired to the host directly: a snapshot bundle is too
// big to hold in memory, and Exec already frames the container's streams.
func (b *Bench) stream(ctx context.Context, out io.Writer, in io.Reader, script string, args ...string) error {
	return b.Engine.Exec(ctx, engine.ExecRequest{
		Container: b.Container,
		Cmd:       engine.Script(script, args...),
		WorkDir:   BenchDir,
		Stdin:     in,
		Stdout:    out,
		Stderr:    b.Out,
	})
}
