package frappe

import (
	"context"
)

// The template store: one tarball per Frappe version, in a volume shared by
// every environment. Wiping it only costs the next create its full price.
const (
	TemplateVolume = "tamp-templates"
	TemplateDir    = "/home/frappe/.tamp-templates"
)

// A template is two files: the bench, and what tamp knows about it.
func TemplatePath(key string) string         { return storePath(TemplateDir, key) }
func TemplateManifestPath(key string) string { return storeManifestPath(TemplateDir, key) }

// HasTemplate reports whether the store holds this template's tarball.
func (b *Bench) HasTemplate(ctx context.Context, key string) (bool, error) {
	return b.hasStored(ctx, TemplatePath(key))
}

// SaveTemplate stores the bench as it stands. gzip -1 rather than zstd: zstd
// is not in the pinned bench image, and level 1 costs seconds to write.
func (b *Bench) SaveTemplate(ctx context.Context, key string) error {
	return b.run(ctx, saveTemplateScript, TemplatePath(key), WorkspaceDir, benchDirName)
}

// benchDirName is the bench's directory relative to the workspace — what the
// tarball holds, so it unpacks to the same place on any machine.
const benchDirName = "frappe-bench"

var saveTemplateScript = saveScript(`tar -C "$2" -cf - "$3"`)

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

// WriteTemplateManifest records what a stored template is, after its tarball.
func (b *Bench) WriteTemplateManifest(ctx context.Context, key string, body []byte) error {
	return b.writeStoredManifest(ctx, TemplateManifestPath(key), body)
}
