package frappe

import (
	"context"

	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/toolchain"
)

// The template store: one tarball per Frappe version, in a volume shared by
// every environment, so the first bench init on a machine is the only one.
// Nothing here is irreplaceable — wiping it costs the next create its full
// price and nothing else.
const (
	TemplateVolume = "tamp-templates"
	TemplateDir    = "/home/frappe/.tamp-templates"
)

// A template is two files: the bench, and what tamp knows about it.
func TemplatePath(key string) string         { return TemplateDir + "/" + key + ".tar.gz" }
func TemplateManifestPath(key string) string { return TemplateDir + "/" + key + ".json" }

// HasTemplate reports whether the store holds this template's tarball. An
// unreachable container is an error, not "no template" — engine.Probe draws
// that line.
func (b *Bench) HasTemplate(ctx context.Context, key string) (bool, error) {
	return engine.Probe(ctx, b.Engine, engine.ExecRequest{
		Container: b.Container,
		Cmd:       engine.Script(`test -f "$1"`, TemplatePath(key)),
	})
}

// SaveTemplate stores the bench as it stands. gzip -1 rather than zstd: zstd
// is not in the pinned bench image, and level 1 costs seconds to write.
func (b *Bench) SaveTemplate(ctx context.Context, key string) error {
	return b.run(ctx, saveTemplateScript, TemplatePath(key), WorkspaceDir, benchDirName)
}

// benchDirName is the bench's directory relative to the workspace — what the
// tarball holds, so it unpacks to the same place on any machine.
const benchDirName = "frappe-bench"

// Written beside the target and renamed: an interrupted save must never leave
// a half-written tarball for the next create to unpack.
const saveTemplateScript = `
set -eo pipefail
mkdir -p "$(dirname "$1")"
tmp="$1.part.$$"
trap 'rm -f "$tmp"' EXIT
tar -C "$2" -cf - "$3" | gzip -1 > "$tmp"
mv -f "$tmp" "$1"
`

// RestoreTemplate unpacks a stored template over the empty bench directory
// the volumes pre-created.
func (b *Bench) RestoreTemplate(ctx context.Context, key string) error {
	return b.run(ctx, restoreTemplateScript, TemplatePath(key), WorkspaceDir)
}

const restoreTemplateScript = `
set -eo pipefail
tar -C "$2" -xzf "$1"
`

// ReadTemplateManifest returns what tamp recorded about a stored template.
func (b *Bench) ReadTemplateManifest(ctx context.Context, key string) ([]byte, error) {
	return b.Engine.ReadFile(ctx, b.Container, TemplateManifestPath(key))
}

// WriteTemplateManifest records what a stored template is. Written after the
// tarball, so a manifest never describes a template that is not there.
func (b *Bench) WriteTemplateManifest(ctx context.Context, key string, body []byte) error {
	return b.Engine.WriteFile(ctx, b.Container, engine.FileSpec{
		Path: TemplateManifestPath(key),
		Data: body,
		Mode: 0o644,
		UID:  toolchain.UID,
		GID:  toolchain.GID,
	})
}
