package env

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/zhide915/tamp/internal/exitcode"
)

// Environment is one resolved environment: where it is, what it says about
// itself, and the Docker names it owns.
type Environment struct {
	Dir       string
	Config    *Config
	Resources Resources
	// Warnings are things tamp noticed while loading the config — unknown
	// keys, so far. The caller prints them; this package has no terminal.
	Warnings []string
}

// Name is the environment's name, as validated by ParseName.
func (e *Environment) Name() Name { return e.Config.Name }

// Open loads the environment rooted at dir. dir must already contain a
// tamp.toml; use Resolve to find one.
func Open(dir string) (*Environment, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot resolve %q to an absolute path: %v", dir, err),
			"run tamp from a directory that still exists")
	}

	cfg, warnings, err := LoadConfig(ConfigPath(abs))
	if err != nil {
		return nil, err
	}
	res, err := NewResources(cfg.Name, abs)
	if err != nil {
		return nil, err
	}
	return &Environment{Dir: abs, Config: cfg, Resources: res, Warnings: warnings}, nil
}

// Resolve finds the environment a command should act on: the one named, or —
// when name is empty — the one the working directory is inside.
//
// Both routes exist because both are natural. An agent or a script names the
// environment from anywhere; a person already sitting in the directory should
// not have to repeat its name, any more than git makes them name the repo.
func Resolve(home, cwd, name string) (*Environment, error) {
	if name != "" {
		return resolveByName(home, name)
	}

	dir, found := findConfigUpward(cwd)
	if !found {
		return nil, exitcode.New(exitcode.CodeNotFound,
			fmt.Sprintf("no tamp environment here — looked for %s in %s and every directory above it", ConfigFile, cwd),
			"run this inside an environment directory, or name one: see 'tamp list'")
	}
	return Open(dir)
}

// resolveByName looks the environment up in the global registry.
func resolveByName(home, name string) (*Environment, error) {
	reg, err := LoadRegistry(home)
	if err != nil {
		return nil, err
	}
	entry, ok := reg[name]
	if !ok {
		return nil, exitcode.New(exitcode.CodeNotFound,
			fmt.Sprintf("environment %q not found", name),
			"see 'tamp list' for the environments on this machine")
	}

	if _, err := os.Stat(ConfigPath(entry.Path)); err != nil {
		// The registry is an index, not the truth: the directory it points at
		// can be moved or deleted behind tamp's back. Saying so beats
		// reporting whatever the next operation fails with.
		return nil, exitcode.New(exitcode.CodeNotFound,
			fmt.Sprintf("environment %q is registered at %s, but there is no %s there", name, entry.Path, ConfigFile),
			"run 'tamp list' to drop registry entries whose directory is gone")
	}
	return Open(entry.Path)
}

// findConfigUpward walks from dir to the filesystem root looking for the
// nearest tamp.toml.
func findConfigUpward(dir string) (string, bool) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", false
	}
	for {
		if _, err := os.Stat(ConfigPath(dir)); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// filepath.Dir is its own fixed point at the root of a volume.
			return "", false
		}
		dir = parent
	}
}
