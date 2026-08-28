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

// ConfigFile is the marker that makes a directory an environment. Every
// command that takes an optional <env> finds it by walking up from the cwd,
// the way git finds .git.
const ConfigFile = "tamp.toml"

// SchemaVersion is the tamp.toml schema tamp writes and understands.
// It is a public contract from the first release: a file tamp cannot read is
// a tamp that needs upgrading, never a file tamp quietly reinterprets.
const SchemaVersion = 1

// ProfileDev selects the template pack the environment is generated from.
// Only "dev" is valid today; "prod" is reserved and rejected until it exists.
const ProfileDev = "dev"

// configHeader opens every generated tamp.toml. The second line is the whole
// of that rule in one sentence, printed where the person about to hand-edit
// compose.yaml will actually read it.
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

// FrappeSection is the bench's Frappe release and the apps fetched onto it.
type FrappeSection struct {
	Version FrappeVersion `toml:"version"`
	// Apps are fetched onto the bench, not installed to any site. Branch
	// is empty when the repo's default branch was taken.
	Apps []App `toml:"apps"`
}

// App is one app on the bench.
type App struct {
	Name   string `toml:"name"`
	Source string `toml:"source"`
	Branch string `toml:"branch,omitempty"`
}

// EngineSection records which container engine the environment was built for.
// Docker is the only one; the field exists so a future engine is a
// migration rather than a guess.
type EngineSection struct {
	Kind string `toml:"kind"`
}

// SyncSection selects how the host apps/ tree reaches the container.
type SyncSection struct {
	Mode syncer.Mode `toml:"mode"`
}

// RouterSection selects hostname routing or the published-port fallback.
type RouterSection struct {
	Mode string `toml:"mode"`
}

// PortsSection holds the only host port an environment publishes: MariaDB, the
// deliberate exception to hostname-only access, because database GUI clients
// need a real TCP port.
type PortsSection struct {
	DB int `toml:"db"`
}

// EngineDocker is the only supported [engine].kind.
const EngineDocker = "docker"

// NewConfig builds the tamp.toml a fresh environment starts life with.
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
		Router:    RouterSection{Mode: "auto"},
		Ports:     PortsSection{DB: dbPort},
	}
}

// ConfigPath is the tamp.toml inside an environment directory.
func ConfigPath(dir string) string { return filepath.Join(dir, ConfigFile) }

// LoadConfig reads and validates an environment's tamp.toml.
//
// The returned warnings are keys tamp did not recognise. They are handed back
// rather than printed because this package has no terminal: the caller decides
// where a warning goes, which is what keeps every command testable.
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

	// The schema check comes before every other one: a file from a newer tamp
	// may use keys this build would otherwise reject as invalid values.
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
	if c.Ports.DB <= 0 {
		return invalid(fmt.Sprintf("[ports] db = %d is not a port", c.Ports.DB),
			"set it to the host port MariaDB should publish, or recreate the environment")
	}
	return nil
}

// Save writes tamp.toml.
//
// It encodes only the fields tamp owns, so it is called where there is
// nothing to lose: at create, and at re-adoption of a directory whose config
// tamp is regenerating anyway. Rewriting a user's existing file in place —
// which has to preserve their unknown keys — is a separate job, and this is
// deliberately not it.
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
