package env

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/frappe"
)

// SnapshotsDirName is where an environment keeps its snapshots, inside the
// state directory the generated .gitignore already keeps out of a repository.
const SnapshotsDirName = "snapshots"

// A snapshot is two files: the bundle, and what tamp knows about it.
const (
	snapshotBundleExt   = ".tar.gz"
	snapshotManifestExt = ".json"
)

// snapshotSchema versions the manifest. A manifest tamp cannot read is a
// snapshot it cannot vouch for, so it is refused rather than reinterpreted —
// the opposite of the template store, where an unreadable manifest costs only
// time.
const snapshotSchema = 1

func snapshotsDir(dir string) string { return filepath.Join(StateDir(dir), SnapshotsDirName) }

// snapshotManifest is what tamp records beside a bundle. The per-site app
// lists are the point: a restore checks them against the bench before it
// touches anything.
type snapshotManifest struct {
	Schema  int            `json:"schema"`
	Name    string         `json:"name"`
	Created time.Time      `json:"created"`
	Frappe  FrappeVersion  `json:"frappe"`
	Sites   []snapshotSite `json:"sites"`
}

type snapshotSite struct {
	Host string   `json:"host"`
	Apps []string `json:"apps"`
}

func (s snapshotManifest) hosts() []string {
	out := make([]string, 0, len(s.Sites))
	for _, site := range s.Sites {
		out = append(out, site.Host)
	}
	return out
}

// snapshotNamePattern is the environment-name pattern without the reserved
// words: a snapshot name is only ever a filename and a --name value, so it
// can be "restore" without ambiguity.
var snapshotNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// snapshotTimeLayout names a snapshot after the moment it was taken, which
// sorts and reads the same way.
const snapshotTimeLayout = "2006-01-02-150405"

func parseSnapshotName(s string) (string, error) {
	if !snapshotNamePattern.MatchString(s) {
		return "", exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("%q is not a usable snapshot name", s),
			"use 1-64 characters of a-z, 0-9 and '-', starting with a letter or digit")
	}
	return s, nil
}

// SnapshotCreateRequest is what `tamp snapshot create` was asked for.
type SnapshotCreateRequest struct {
	Env string
	// Name empty means a timestamp.
	Name string
}

// SnapshotCreate backs every site up with its files and bundles the lot into
// the environment's own state directory.
func (m *Manager) SnapshotCreate(ctx context.Context, req SnapshotCreateRequest) error {
	e, err := m.resolve(req.Env)
	if err != nil {
		return err
	}
	if err := m.requireRunning(ctx, e, "so tamp cannot snapshot it"); err != nil {
		return err
	}

	name := req.Name
	if name == "" {
		name = time.Now().Format(snapshotTimeLayout)
	}
	if name, err = parseSnapshotName(name); err != nil {
		return err
	}
	if _, err := os.Stat(snapshotManifestPath(e.Dir, name)); err == nil {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("%s already has a snapshot named %q", e.Name(), name),
			"pick another --name, or leave it out and tamp names it after the moment")
	}

	hosts, _, err := m.sites(ctx, e)
	if err != nil {
		return err
	}
	if len(hosts) == 0 {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("%s has no sites, so there is nothing in its data layer to snapshot", e.Name()),
			fmt.Sprintf("create one first: tamp site new %s <host>", e.Name()))
	}

	bench := e.bench(m.Engine, m.Out.Stream())
	// A staging area left behind by an interrupted run would otherwise
	// travel into this bundle.
	if err := bench.ClearStage(ctx); err != nil {
		return err
	}

	manifest := snapshotManifest{
		Schema:  snapshotSchema,
		Name:    name,
		Created: time.Now(),
		Frappe:  e.Config.Frappe.Version,
	}

	steps := m.Out.Steps(len(hosts) + 1)
	for _, host := range hosts {
		steps.Step("backing up " + host + " with its files")
		apps, err := bench.InstalledApps(ctx, host)
		if err != nil {
			return err
		}
		if err := bench.StageBackup(ctx, host); err != nil {
			return err
		}
		manifest.Sites = append(manifest.Sites, snapshotSite{Host: host, Apps: apps})
	}

	steps.Step("bundling the snapshot into " + snapshotsDir(e.Dir))
	size, err := m.writeSnapshot(ctx, e, bench, manifest)
	if err != nil {
		return err
	}
	if err := bench.ClearStage(ctx); err != nil {
		return err
	}

	m.Out.OK(fmt.Sprintf("snapshot %s of %s: %d site(s), %s",
		name, e.Name(), len(manifest.Sites), humanSize(size)))
	m.Out.Note("it is a file in " + snapshotsDir(e.Dir) + " — yours to copy, move or delete")
	m.Out.Hint(fmt.Sprintf("next: tamp snapshot list %s", e.Name()))
	return nil
}

// writeSnapshot streams the bundle out of the container and records the
// manifest after it, so a manifest never describes a bundle that is not
// there. The bundle is written beside its final name and renamed, so an
// interrupted create cannot leave half a snapshot to restore from.
func (m *Manager) writeSnapshot(ctx context.Context, e *Environment, bench *frappe.Bench, manifest snapshotManifest) (int64, error) {
	dir := snapshotsDir(e.Dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot create %s: %v", dir, err),
			"check that the environment directory is writable")
	}

	final := snapshotBundlePath(e.Dir, manifest.Name)
	partial := final + ".part"
	file, err := os.Create(partial)
	if err != nil {
		return 0, snapshotWriteError(partial, err)
	}
	defer func() { _ = os.Remove(partial) }()

	if err := bench.BundleSnapshot(ctx, file); err != nil {
		_ = file.Close()
		return 0, err
	}
	size, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		_ = file.Close()
		return 0, snapshotWriteError(partial, err)
	}
	if err := file.Close(); err != nil {
		return 0, snapshotWriteError(partial, err)
	}
	if err := os.Rename(partial, final); err != nil {
		return 0, snapshotWriteError(final, err)
	}

	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return 0, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot render the snapshot manifest: %v", err), "")
	}
	path := snapshotManifestPath(e.Dir, manifest.Name)
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		return 0, snapshotWriteError(path, err)
	}
	return size, nil
}

func snapshotBundlePath(dir, name string) string {
	return filepath.Join(snapshotsDir(dir), name+snapshotBundleExt)
}

func snapshotManifestPath(dir, name string) string {
	return filepath.Join(snapshotsDir(dir), name+snapshotManifestExt)
}

func snapshotWriteError(path string, err error) error {
	return exitcode.New(exitcode.CodeFailed,
		fmt.Sprintf("cannot write %s: %v", path, err),
		"check that the environment directory is writable and has room")
}

// SnapshotList reports the snapshots an environment holds, newest first.
func (m *Manager) SnapshotList(name string) error {
	e, err := m.resolve(name)
	if err != nil {
		return err
	}
	held, err := m.snapshots(e)
	if err != nil {
		return err
	}
	if len(held) == 0 {
		m.Out.Print("no snapshots yet")
		m.Out.Hint(fmt.Sprintf("take one: tamp snapshot create %s", e.Name()))
		return nil
	}

	rows := make([][]string, 0, len(held))
	for _, s := range held {
		size := unknownField
		if info, err := os.Stat(snapshotBundlePath(e.Dir, s.Name)); err == nil {
			size = humanSize(info.Size())
		}
		rows = append(rows, []string{
			s.Name,
			s.Created.Local().Format(time.RFC3339),
			size,
			fmt.Sprint(len(s.Sites)),
		})
	}
	m.Out.Table([]string{"NAME", "CREATED", "SIZE", "SITES"}, rows)
	m.Out.Hint(fmt.Sprintf("restore the newest: tamp snapshot restore %s", e.Name()))
	return nil
}

// snapshots reads every manifest in the directory, newest first. A manifest
// tamp cannot read is reported and skipped: one damaged file must not hide
// the snapshots beside it.
func (m *Manager) snapshots(e *Environment) ([]snapshotManifest, error) {
	entries, err := os.ReadDir(snapshotsDir(e.Dir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot read %s: %v", snapshotsDir(e.Dir), err),
			"check the permissions on the environment directory")
	}

	var held []snapshotManifest
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), snapshotManifestExt) {
			continue
		}
		manifest, err := m.readSnapshot(e, strings.TrimSuffix(entry.Name(), snapshotManifestExt))
		if err != nil {
			m.Out.Warn(err.Error())
			continue
		}
		held = append(held, manifest)
	}
	slices.SortFunc(held, func(a, b snapshotManifest) int { return b.Created.Compare(a.Created) })
	return held, nil
}

func (m *Manager) readSnapshot(e *Environment, name string) (snapshotManifest, error) {
	path := snapshotManifestPath(e.Dir, name)
	body, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return snapshotManifest{}, exitcode.New(exitcode.CodeNotFound,
			fmt.Sprintf("%s has no snapshot named %q", e.Name(), name),
			fmt.Sprintf("see 'tamp snapshot list %s' for the ones it has", e.Name()))
	}
	if err != nil {
		return snapshotManifest{}, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot read %s: %v", path, err),
			"check the permissions on the environment directory")
	}

	var manifest snapshotManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return snapshotManifest{}, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("%s is not valid JSON: %v", path, err),
			"the snapshot cannot be vouched for — remove it, or repair the file")
	}
	if manifest.Schema != snapshotSchema {
		return snapshotManifest{}, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("%s has schema %d, and this tamp understands schema %d", path, manifest.Schema, snapshotSchema),
			"upgrade tamp to a version that reads this snapshot")
	}
	if _, err := os.Stat(snapshotBundlePath(e.Dir, name)); err != nil {
		return snapshotManifest{}, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("the snapshot %q has a manifest but no bundle beside it at %s",
				name, snapshotBundlePath(e.Dir, name)),
			"remove the manifest, or put the bundle back beside it")
	}
	manifest.Name = name
	return manifest, nil
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for size := n / unit; size >= unit; size /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGT"[exp])
}
