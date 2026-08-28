package env

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/zhide915/tamp/internal/exitcode"
)

// GlobalConfigFile holds the machine's tamp settings, beside the registry.
// Absent is the normal case: every setting here has a default, and tamp never
// writes the file itself.
const GlobalConfigFile = "config.toml"

// DefaultTemplateTTLDays is how long a cached bench template is trusted.
// Release branches move, so an old template would be a bench weeks behind the
// branch the user asked for.
const DefaultTemplateTTLDays = 14

// GlobalConfig is ~/.tamp/config.toml.
type GlobalConfig struct {
	Cache CacheSection `toml:"cache"`
}

// CacheSection tunes the machine-global caches.
type CacheSection struct {
	// TemplateTTLDays is a pointer so that an unset key and an explicit 0 are
	// different answers: unset takes the default, 0 expires every template
	// immediately, which is how the cache is turned off for good.
	TemplateTTLDays *int `toml:"template_ttl_days"`
}

func GlobalConfigPath(home string) string { return filepath.Join(home, GlobalConfigFile) }

// LoadGlobalConfig reads the machine's settings; a missing file is the
// defaults.
func LoadGlobalConfig(home string) (*GlobalConfig, error) {
	cfg := &GlobalConfig{}
	md, err := toml.DecodeFile(GlobalConfigPath(home), cfg)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return nil, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot read %s: %v", GlobalConfigPath(home), err),
			"fix the file's syntax, or delete it — tamp falls back to its defaults")
	}
	if len(md.Undecoded()) > 0 {
		// Unknown keys are the user's typo, and a silently ignored setting is
		// worse than a refusal: they would believe it took effect.
		return nil, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("%s has a setting tamp does not know: %q", GlobalConfigPath(home), md.Undecoded()[0].String()),
			"remove the key, or check its spelling")
	}
	return cfg, nil
}

// TemplateTTL is how long a stored template stays usable. A negative setting
// reads as zero — expire everything — rather than as time running backwards.
func (c *GlobalConfig) TemplateTTL() time.Duration {
	days := DefaultTemplateTTLDays
	if c.Cache.TemplateTTLDays != nil {
		days = *c.Cache.TemplateTTLDays
	}
	if days < 0 {
		days = 0
	}
	return time.Duration(days) * 24 * time.Hour
}
