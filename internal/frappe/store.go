package frappe

import (
	"context"

	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/toolchain"
)

// A machine-wide store: keyed tarballs in a shared volume, each with a
// manifest beside it. The two stores differ only in what the tarballs hold.

func storePath(dir, key string) string         { return dir + "/" + key + ".tar.gz" }
func storeManifestPath(dir, key string) string { return dir + "/" + key + ".json" }

// hasStored reports whether the store holds a tarball at path. An unreachable
// container is an error, not "not stored" — engine.Probe draws that line.
func (b *Bench) hasStored(ctx context.Context, path string) (bool, error) {
	return engine.Probe(ctx, b.Engine, engine.ExecRequest{
		Container: b.Container,
		Cmd:       engine.Script(`test -f "$1"`, path),
	})
}

// writeStoredManifest records what a stored tarball is. Callers write it
// after the tarball, so a manifest never describes what is not there.
func (b *Bench) writeStoredManifest(ctx context.Context, path string, body []byte) error {
	return b.Engine.WriteFile(ctx, b.Container, engine.FileSpec{
		Path: path,
		Data: body,
		Mode: 0o644,
		UID:  toolchain.UID,
		GID:  toolchain.GID,
	})
}

// saveScript wraps one tar line in the write discipline: beside the target,
// then renamed — an interrupted save must not leave a half-written tarball.
func saveScript(tar string) string {
	return `
set -eo pipefail
mkdir -p "$(dirname "$1")"
tmp="$1.part.$$"
trap 'rm -f "$tmp"' EXIT
` + tar + ` | gzip -1 > "$tmp"
mv -f "$tmp" "$1"
`
}
