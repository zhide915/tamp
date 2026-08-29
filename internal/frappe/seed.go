package frappe

import (
	"context"

	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/toolchain"
)

// The seed store: one pristine site backup per key, in a volume shared by
// every environment. Wiping it only costs the next site its full install.
const (
	SeedVolume = "tamp-seeds"
	SeedDir    = "/home/frappe/.tamp-seeds"
)

// A seed is two files: the backup, and what tamp knows about it.
func SeedPath(key string) string         { return storePath(SeedDir, key) }
func SeedManifestPath(key string) string { return storeManifestPath(SeedDir, key) }

// HasSeed reports whether the store holds this seed's tarball.
func (b *Bench) HasSeed(ctx context.Context, key string) (bool, error) {
	return b.hasStored(ctx, SeedPath(key))
}

// SaveSeed stores one staged site's backup as this key's seed; StageBackup
// stages it, the same choreography a snapshot uses.
func (b *Bench) SaveSeed(ctx context.Context, key, host string) error {
	if err := b.ensureSeedDir(ctx); err != nil {
		return err
	}
	return b.run(ctx, saveSeedScript, SeedPath(key), stageDir, host)
}

// ensureSeedDir hands the store's mount point to the bench user. Docker
// creates a new volume root-owned, and an environment made before this store
// existed never ran the provisioning that chowns it; only a save writes.
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

var saveSeedScript = saveScript(`tar -C "$2/$3" -cf - .`)

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

// WriteSeedManifest records what a stored seed is, after its tarball.
func (b *Bench) WriteSeedManifest(ctx context.Context, key string, body []byte) error {
	return b.writeStoredManifest(ctx, SeedManifestPath(key), body)
}
