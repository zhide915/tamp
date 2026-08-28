package env

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/zhide915/tamp/internal/exitcode"
)

// Environment is one resolved environment.
type Environment struct {
	Dir       string
	Config    *Config
	Resources Resources
	// Warnings were noticed while loading the config. The caller prints them;
	// this package has no terminal.
	Warnings []string
}

func (e *Environment) Name() Name { return e.Config.Name }

// Open loads the environment rooted at dir, which must already hold a
// tamp.toml; Resolve finds one.
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

// Resolve returns the named environment, or — when name is empty — the one
// the working directory is inside, found the way git finds a repo.
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
		// The registry is an index, not the truth: the directory can vanish
		// behind it.
		return nil, exitcode.New(exitcode.CodeNotFound,
			fmt.Sprintf("environment %q is registered at %s, but there is no %s there", name, entry.Path, ConfigFile),
			"run 'tamp list' to drop registry entries whose directory is gone")
	}
	return Open(entry.Path)
}

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
			// filepath.Dir is its own fixed point at a volume root.
			return "", false
		}
		dir = parent
	}
}
