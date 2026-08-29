package frappe

import (
	"context"

	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/toolchain"
)

// The seed store: one pristine site backup per key, in a volume shared by
// every environment, so an app set is installed onto a site once per machine
// and restored after that. Nothing here is irreplaceable — wiping it costs
// the next site its full install and nothing else.
const (
	SeedVolume = "tamp-seeds"
	SeedDir    = "/home/frappe/.tamp-seeds"
)

// A seed is two files: the backup, and what tamp knows about it.
func SeedPath(key string) string         { return SeedDir + "/" + key + ".tar.gz" }
func SeedManifestPath(key string) string { return SeedDir + "/" + key + ".json" }

// HasSeed reports whether the store holds this seed's tarball. An
// unreachable container is an error, not "no seed" — engine.Probe draws that
// line.
func (b *Bench) HasSeed(ctx context.Context, key string) (bool, error) {
	return engine.Probe(ctx, b.Engine, engine.ExecRequest{
		Container: b.Container,
		Cmd:       engine.Script(`test -f "$1"`, SeedPath(key)),
	})
}

// SaveSeed stores one staged site's backup as this key's seed. The caller
// stages it with StageBackup first, which is the same choreography a
// snapshot goes through.
func (b *Bench) SaveSeed(ctx context.Context, key, host string) error {
	if err := b.ensureSeedDir(ctx); err != nil {
		return err
	}
	return b.run(ctx, saveSeedScript, SeedPath(key), stageDir, host)
}

// ensureSeedDir hands the store's mount point to the bench user. Docker
// creates a newly mounted volume root-owned, and only a create runs the
// provisioning that chowns the other shared directories — so an environment
// made before this store existed would never be able to write to it.
// Reading needs no such repair, which is why only a save asks for it.
func (b *Bench) ensureSeedDir(ctx context.Context) error {
	return b.Engine.Exec(ctx, engine.ExecRequest{
		Container: b.Container,
		Cmd: engine.Script(
			`set -e; mkdir -p "$1"; chown `+toolchain.User+`:`+toolchain.User+` "$1"`, SeedDir),
		User:   "root",
		Stdout: b.Out,
		Stderr: b.Out,
	})
}

// Written beside the target and renamed: an interrupted save must never
// leave a half-written tarball for the next site to restore.
const saveSeedScript = `
set -eo pipefail
mkdir -p "$(dirname "$1")"
tmp="$1.part.$$"
trap 'rm -f "$tmp"' EXIT
tar -C "$2/$3" -cf - . | gzip -1 > "$tmp"
mv -f "$tmp" "$1"
`

// RestoreSeed lays a stored seed out in the staging area under host's name,
// where RestoreSite reads it.
func (b *Bench) RestoreSeed(ctx context.Context, key, host string) error {
	return b.run(ctx, restoreSeedScript, SeedPath(key), stageDir, host)
}

const restoreSeedScript = `
set -eo pipefail
stage="$2/$3"
rm -rf "$stage"
mkdir -p "$stage"
tar -C "$stage" -xzf "$1"
`

// ReadSeedManifest returns what tamp recorded about a stored seed.
func (b *Bench) ReadSeedManifest(ctx context.Context, key string) ([]byte, error) {
	return b.Engine.ReadFile(ctx, b.Container, SeedManifestPath(key))
}

// WriteSeedManifest records what a stored seed is. Written after the
// tarball, so a manifest never describes a seed that is not there.
func (b *Bench) WriteSeedManifest(ctx context.Context, key string, body []byte) error {
	return b.Engine.WriteFile(ctx, b.Container, engine.FileSpec{
		Path: SeedManifestPath(key),
		Data: body,
		Mode: 0o644,
		UID:  toolchain.UID,
		GID:  toolchain.GID,
	})
}
