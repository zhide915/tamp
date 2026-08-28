package env

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/syncer"
)

// ConfigFile marks a directory as an environment; commands find it by walking
// up from the cwd, like git finds .git.
const ConfigFile = "tamp.toml"

// SchemaVersion is the tamp.toml schema this build reads and writes. A
// mismatch is an error to upgrade past, never a file to reinterpret.
const SchemaVersion = 1

// ProfileDev is the only profile tamp v1 accepts; "prod" is reserved.
const ProfileDev = "dev"

// configHeader warns hand-editors that the generated files are rewritten from
// this one.
const configHeader = `# tamp environment. This file is the source of truth.
# compose.yaml and the other generated files are rewritten from it on every
# 'tamp start' — edit this, not them.

`

// Config is tamp.toml.
type Config struct {
	Schema    int           `toml:"schema"`
	Name      Name          `toml:"name"`
	Profile   string        `toml:"profile"`
	Frappe    FrappeSection `toml:"frappe"`
	Toolchain Toolchain     `toml:"toolchain"`
	Engine    EngineSection `toml:"engine"`
	Sync      SyncSection   `toml:"sync"`
	Router    RouterSection `toml:"router"`
	Ports     PortsSection  `toml:"ports"`
}

// FrappeSection is the bench's Frappe release and its apps.
type FrappeSection struct {
	Version FrappeVersion `toml:"version"`
	// Apps are fetched onto the bench, not installed to any site. An empty
	// Branch means the repo's default branch.
	Apps []App `toml:"apps"`
}

// App is one app on the bench.
type App struct {
	Name   string `toml:"name"`
	Source string `toml:"source"`
	Branch string `toml:"branch,omitempty"`
}

// EngineSection exists so a future engine is a migration rather than a guess;
// Docker is the only kind today.
type EngineSection struct {
	Kind string `toml:"kind"`
}

// SyncSection selects how the host apps/ tree reaches the container.
type SyncSection struct {
	Mode syncer.Mode `toml:"mode"`
}

// RouterSection selects the routing mode; "ports" is reserved.
type RouterSection struct {
	Mode string `toml:"mode"`
}

// RouterModeAuto — hostname routing through the shared router — is the only
// mode tamp v1 accepts.
const RouterModeAuto = "auto"

// PortsSection holds the MariaDB host port — the one port an environment
// publishes, because database GUI clients need real TCP.
type PortsSection struct {
	DB int `toml:"db"`
}

const EngineDocker = "docker"

func NewConfig(name Name, version FrappeVersion, apps []App, tc Toolchain, dbPort int) *Config {
	if apps == nil {
		apps = []App{}
	}
	return &Config{
		Schema:    SchemaVersion,
		Name:      name,
		Profile:   ProfileDev,
		Frappe:    FrappeSection{Version: version, Apps: apps},
		Toolchain: tc,
		Engine:    EngineSection{Kind: EngineDocker},
		Sync:      SyncSection{Mode: syncer.ModeAuto},
		Router:    RouterSection{Mode: RouterModeAuto},
		Ports:     PortsSection{DB: dbPort},
	}
}

func ConfigPath(dir string) string { return filepath.Join(dir, ConfigFile) }

// LoadConfig reads and validates an environment's tamp.toml. warnings are
// unrecognised keys, returned rather than printed — this package has no
// terminal, and the caller decides where they go.
func LoadConfig(path string) (cfg *Config, warnings []string, err error) {
	cfg = &Config{}
	md, err := toml.DecodeFile(path, cfg)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, exitcode.New(exitcode.CodeNotFound,
				fmt.Sprintf("%s is not an environment: no %s in it", filepath.Dir(path), ConfigFile),
				"run 'tamp create <name>' to make one, or 'tamp list' to see the ones you have")
		}
		return nil, nil, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot read %s: %v", path, err),
			"fix the file's syntax, or delete the environment and create it again")
	}

	// Checked first: a newer schema may use keys this build would misread as
	// invalid values.
	if cfg.Schema != SchemaVersion {
		return nil, nil, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("%s has schema %d, and this tamp understands schema %d", path, cfg.Schema, SchemaVersion),
			"upgrade tamp to a version that reads this environment")
	}
	if err := cfg.validate(path); err != nil {
		return nil, nil, err
	}

	for _, key := range md.Undecoded() {
		warnings = append(warnings, fmt.Sprintf("%s: unknown key %q — tamp ignores it", filepath.Base(path), key.String()))
	}
	return cfg, warnings, nil
}

func (c *Config) validate(path string) error {
	invalid := func(msg, fix string) error {
		return exitcode.New(exitcode.CodeFailed, fmt.Sprintf("%s: %s", path, msg), fix)
	}

	if _, err := ParseName(string(c.Name)); err != nil {
		return invalid(fmt.Sprintf("%q is not a usable environment name", c.Name),
			"environment names are 1-32 characters of a-z, 0-9 and '-'")
	}
	if c.Profile != ProfileDev {
		return invalid(fmt.Sprintf("profile %q is not supported", c.Profile),
			`tamp v1 ships only profile = "dev"`)
	}
	if _, ok := ToolchainFor(c.Frappe.Version); !ok {
		return invalid(fmt.Sprintf("Frappe %s is not supported", c.Frappe.Version),
			"supported versions: "+strings.Join(versionNames(), ", "))
	}
	if c.Engine.Kind != EngineDocker {
		return invalid(fmt.Sprintf("engine %q is not supported", c.Engine.Kind),
			`Docker is tamp's only engine — set kind = "docker"`)
	}
	if _, err := syncer.ParseMode(string(c.Sync.Mode)); err != nil {
		return invalid(fmt.Sprintf("[sync] mode = %q is not a way of syncing source", c.Sync.Mode),
			"modes: "+strings.Join(syncer.ModeNames(), ", "))
	}
	// Empty means the default; only a mode tamp does not have is refused.
	if c.Router.Mode != "" && c.Router.Mode != RouterModeAuto {
		return invalid(fmt.Sprintf("[router] mode = %q is not supported", c.Router.Mode),
			`tamp v1 routes by hostname only — set mode = "auto"`)
	}
	if c.Ports.DB <= 0 {
		return invalid(fmt.Sprintf("[ports] db = %d is not a port", c.Ports.DB),
			"set it to the host port MariaDB should publish, or recreate the environment")
	}
	return nil
}

// Save writes tamp.toml. It encodes only the fields tamp owns — unknown keys
// are lost — so call it only where nothing needs preserving: create, and
// re-adoption of a config tamp is regenerating anyway.
func (c *Config) Save(path string) error {
	var buf strings.Builder
	buf.WriteString(configHeader)
	if err := toml.NewEncoder(&buf).Encode(c); err != nil {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot render %s: %v", ConfigFile, err), "")
	}
	if err := os.WriteFile(path, []byte(buf.String()), 0o644); err != nil {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot write %s: %v", path, err),
			"check that the directory is writable")
	}
	return nil
}
