package env

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/zhide915/tamp/internal/exitcode"
)

// RegistryFile indexes every environment on the machine, keyed by name.
const RegistryFile = "registry.json"

// Entry is one environment's row in the global registry.
type Entry struct {
	// Path is the absolute environment directory. It is what makes
	// `tamp start erp15` work from anywhere.
	Path string `json:"path"`
	// Hash is the path hash baked into the environment's Docker resources,
	// stored so tamp can name them without re-deriving from a path that may
	// since have moved.
	Hash string `json:"hash"`
	// DBPort is the host port this environment's MariaDB publishes.
	//
	// tamp.toml is where the environment records its own port, and where the
	// compose generator reads it. This is the allocator's ledger, and it is
	// deliberately not the same thing: allocation has to be decided from data
	// that is already written when the machine lock is released, and an
	// environment's tamp.toml is not.
	DBPort int `json:"db_port"`
	// Sites caches the environment's site hostnames. It exists so the router
	// and the hosts block can be assembled while an environment is stopped —
	// the bench's sites/ directory is the authority whenever it is running.
	Sites []string `json:"sites"`
}

// Registry is the whole of ~/.tamp/registry.json.
//
// Names are unique in it: it is keyed by name, and `tamp start erp15`
// has to be unambiguous from any directory on the machine.
type Registry map[string]Entry

// RegistryPath is the registry inside a tamp home directory.
func RegistryPath(home string) string { return filepath.Join(home, RegistryFile) }

// LoadRegistry reads the registry. A machine that has never run tamp create
// has none, which is an empty registry rather than an error.
func LoadRegistry(home string) (Registry, error) {
	blob, err := os.ReadFile(RegistryPath(home))
	if errors.Is(err, fs.ErrNotExist) {
		return Registry{}, nil
	}
	if err != nil {
		return nil, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot read %s: %v", RegistryPath(home), err),
			"check the permissions on your ~/.tamp directory")
	}

	reg := Registry{}
	if err := json.Unmarshal(blob, &reg); err != nil {
		return nil, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("%s is not valid JSON: %v", RegistryPath(home), err),
			"repair or delete it — tamp rebuilds it as environments are created")
	}
	return reg, nil
}

// Names lists the registered environments in the order a user reads them.
func (r Registry) Names() []string {
	names := make([]string, 0, len(r))
	for name := range r {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// save writes the registry, replacing the old file only once the new one is
// complete on disk: an interrupted write must not leave the machine's index of
// every environment half-written.
func (r Registry) save(home string) error {
	blob, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot render the registry: %v", err), "")
	}
	blob = append(blob, '\n')

	path := RegistryPath(home)
	tmp, err := os.CreateTemp(home, RegistryFile+".*")
	if err != nil {
		return registryWriteError(path, err)
	}
	// Expected to fail on the happy path, where the rename below has already
	// taken the temp file away; it is here for the paths that do not get there.
	defer func() { _ = os.Remove(tmp.Name()) }()

	if _, err := tmp.Write(blob); err != nil {
		_ = tmp.Close()
		return registryWriteError(path, err)
	}
	if err := tmp.Close(); err != nil {
		return registryWriteError(path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return registryWriteError(path, err)
	}
	return nil
}

func registryWriteError(path string, err error) error {
	return exitcode.New(exitcode.CodeFailed,
		fmt.Sprintf("cannot write %s: %v", path, err),
		"check the permissions on your ~/.tamp directory")
}

// UpdateRegistry runs change against the registry under the machine-global
// lock, and writes the result back only if change succeeded.
//
// Every mutation goes through here. Reading the registry, deciding, and
// writing it back is a read-modify-write cycle, and doing it outside the lock
// is exactly how a concurrent tamp loses an environment.
func UpdateRegistry(home string, change func(Registry) error) error {
	lock, err := AcquireLock(home)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()

	reg, err := LoadRegistry(home)
	if err != nil {
		return err
	}
	if err := change(reg); err != nil {
		return err
	}
	return reg.save(home)
}
