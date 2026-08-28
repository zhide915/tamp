package env

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func mustResources(t *testing.T, name, dir string) Resources {
	t.Helper()
	n, err := ParseName(name)
	if err != nil {
		t.Fatalf("ParseName(%q) = %v", name, err)
	}
	r, err := NewResources(n, dir)
	if err != nil {
		t.Fatalf("NewResources(%q, %q) = %v", name, dir, err)
	}
	return r
}

func TestResourceNamesFollowTheDocumentedShape(t *testing.T) {
	r := mustResources(t, "erp15", t.TempDir())

	if got, want := r.Project(), "tamp-erp15-"+r.Hash; got != want {
		t.Errorf("Project() = %q, want %q", got, want)
	}
	if got, want := r.Volume("db"), r.Project()+"-db"; got != want {
		t.Errorf("Volume(db) = %q, want %q", got, want)
	}
	if got, want := r.Network(), r.Project(); got != want {
		t.Errorf("Network() = %q, want %q", got, want)
	}
	if len(r.Hash) != hashLength {
		t.Errorf("Hash = %q, want %d characters", r.Hash, hashLength)
	}
}

// The whole point of the hash: the same name in two directories is two
// environments whose resources never touch.
func TestSameNameInDifferentDirectoriesGetsDifferentResources(t *testing.T) {
	a := mustResources(t, "erp15", t.TempDir())
	b := mustResources(t, "erp15", t.TempDir())

	if a.Project() == b.Project() {
		t.Errorf("both directories produced project %q", a.Project())
	}
}

// Re-adoption finds surviving volumes by name and path hash, so reaching
// the same directory by a different spelling has to hash the same.
func TestHashIsStableAcrossSpellingsOfTheSamePath(t *testing.T) {
	dir := t.TempDir()
	want := mustResources(t, "erp15", dir).Hash

	for _, spelling := range []string{
		dir + string(filepath.Separator),
		filepath.Join(dir, "sub", ".."),
		filepath.ToSlash(dir),
	} {
		if got := mustResources(t, "erp15", spelling).Hash; got != want {
			t.Errorf("hash of %q = %q, want %q", spelling, got, want)
		}
	}

	if runtime.GOOS == "windows" {
		if got := mustResources(t, "erp15", strings.ToUpper(dir)).Hash; got != want {
			t.Errorf("hash of the upper-cased path = %q, want %q — Windows paths are case-insensitive", got, want)
		}
	}
}
