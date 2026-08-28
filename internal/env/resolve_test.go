package env

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/exitcode"
)

// plant writes and registers a valid environment the way create would.
func plant(t *testing.T, home, dir, name string) {
	t.Helper()
	n, err := ParseName(name)
	if err != nil {
		t.Fatal(err)
	}
	_, tc, err := ParseFrappeVersion(string(DefaultFrappeVersion))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := NewConfig(n, DefaultFrappeVersion, nil, tc, FirstDBPort).Save(ConfigPath(dir)); err != nil {
		t.Fatal(err)
	}
	res, err := NewResources(n, dir)
	if err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := UpdateRegistry(home, func(reg Registry) error {
		reg[name] = Entry{Path: abs, Hash: res.Hash, Sites: []string{}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestResolveFindsTheEnvironmentTheCwdIsInside(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	dir := filepath.Join(root, "erp15")
	plant(t, home, dir, "erp15")
	deep := filepath.Join(dir, "apps", "erpnext", "erpnext")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	e, err := Resolve(home, deep, "")
	if err != nil {
		t.Fatalf("Resolve = %v", err)
	}
	if e.Name() != "erp15" {
		t.Errorf("resolved %q, want erp15", e.Name())
	}
}

func TestResolveByNameWorksFromAnUnrelatedDirectory(t *testing.T) {
	home, root, elsewhere := t.TempDir(), t.TempDir(), t.TempDir()
	plant(t, home, filepath.Join(root, "erp15"), "erp15")

	e, err := Resolve(home, elsewhere, "erp15")
	if err != nil {
		t.Fatalf("Resolve = %v", err)
	}
	if e.Name() != "erp15" {
		t.Errorf("resolved %q, want erp15", e.Name())
	}
	if e.Resources.Project() != "tamp-erp15-"+e.Resources.Hash {
		t.Errorf("project = %q, want it derived from the resolved directory", e.Resources.Project())
	}
}

func TestAnExplicitNameBeatsTheCurrentDirectory(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	plant(t, home, filepath.Join(root, "erp15"), "erp15")
	plant(t, home, filepath.Join(root, "next16"), "next16")

	e, err := Resolve(home, filepath.Join(root, "erp15"), "next16")
	if err != nil {
		t.Fatalf("Resolve = %v", err)
	}
	if e.Name() != "next16" {
		t.Errorf("resolved %q, want next16", e.Name())
	}
}

// The registry is an index; a directory can vanish behind it.
func TestARegisteredEnvironmentWhoseDirectoryIsGoneSaysSo(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	dir := filepath.Join(root, "erp15")
	plant(t, home, dir, "erp15")
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}

	_, err := Resolve(home, t.TempDir(), "erp15")
	assertNotFoundWithFix(t, err)
	if !strings.Contains(err.Error(), "tamp list") {
		t.Errorf("error = %q, want it to point at the command that prunes it", err)
	}
}

func assertNotFoundWithFix(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("got nil, want an error")
	}
	if got := exitcode.Of(err); got != exitcode.CodeNotFound {
		t.Errorf("exit code = %d, want %d (%v)", got, exitcode.CodeNotFound, err)
	}
}
