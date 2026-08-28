package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/zhide915/tamp/internal/exitcode"
)

// Source names where a Docker address came from. tamp prints it, because "you
// told me via DOCKER_HOST" and "I found a socket lying there" are different
// facts to someone whose setup is misbehaving.
type Source string

const (
	SourceEnv     Source = "DOCKER_HOST"
	SourceContext Source = "docker context"
	SourceProbe   Source = "probed socket"
)

// Address is the Docker endpoint tamp resolved, and how it got there.
type Address struct {
	// Host is a docker connection string: unix://, npipe://, tcp:// or ssh://.
	Host   string
	Source Source
	// Context is the docker context name, set only when Source is SourceContext.
	Context string
}

// String renders the address the way doctor and error messages want it: the
// endpoint, then how tamp arrived at it.
func (a Address) String() string {
	if a.Context != "" {
		return fmt.Sprintf("%s (%s %q)", a.Host, a.Source, a.Context)
	}
	return fmt.Sprintf("%s (%s)", a.Host, a.Source)
}

// defaultCandidates are the well-known endpoints tamp probes when nothing
// points it anywhere. Both are listed on every OS: the stat simply fails for
// the one that cannot exist there, which is cheaper than a per-platform list
// and keeps the probe order identical to the spec everywhere.
var defaultCandidates = []string{
	`npipe:////./pipe/docker_engine`,
	"unix:///var/run/docker.sock",
}

// Detector resolves the Docker endpoint. Its fields are the whole of its
// contact with the outside world, so a test substitutes a temp config dir and
// a candidate list instead of needing Docker installed.
type Detector struct {
	// ConfigDir is docker's own config directory — ~/.docker, or DOCKER_CONFIG.
	ConfigDir  string
	LookupEnv  func(string) (string, bool)
	Candidates []string
	// Exists reports whether a candidate endpoint is present on this machine.
	Exists func(host string) bool
}

// NewDetector builds the Detector tamp uses in production. Pass os.LookupEnv.
func NewDetector(lookupEnv func(string) (string, bool)) Detector {
	return Detector{
		ConfigDir:  dockerConfigDir(lookupEnv),
		LookupEnv:  lookupEnv,
		Candidates: defaultCandidates,
		Exists:     endpointExists,
	}
}

// Detect resolves the Docker endpoint in the documented order: DOCKER_HOST, the
// active docker context, then the well-known sockets. Finding nothing is an
// engine-unavailable error, not an empty result — every caller of Detect needs
// an engine, so there is no useful "no address, no error" case.
func (d Detector) Detect() (Address, error) {
	if host, ok := d.LookupEnv("DOCKER_HOST"); ok && host != "" {
		return Address{Host: host, Source: SourceEnv}, nil
	}

	addr, err := d.fromContext()
	if err != nil {
		return Address{}, err
	}
	if addr.Host != "" {
		return addr, nil
	}

	for _, host := range d.Candidates {
		if d.Exists(host) {
			return Address{Host: host, Source: SourceProbe}, nil
		}
	}

	return Address{}, exitcode.New(
		exitcode.CodeEngineUnavailable,
		"no Docker engine found — looked at DOCKER_HOST, the active docker context, and "+
			strings.Join(d.Candidates, ", "),
		"start Docker Desktop, or point DOCKER_HOST at your engine's socket",
	)
}

// fromContext resolves the active docker context's endpoint. A zero Address
// with a nil error means "no context is selected" — probing should continue.
func (d Detector) fromContext() (Address, error) {
	name, err := d.activeContext()
	if err != nil {
		return Address{}, err
	}
	// "default" is docker's own name for the platform's built-in socket rather
	// than a stored context, and it has no metadata on disk.
	if name == "" || name == "default" {
		return Address{}, nil
	}

	host, err := d.contextEndpoint(name)
	if err != nil {
		return Address{}, err
	}
	return Address{Host: host, Source: SourceContext, Context: name}, nil
}

// activeContext reports the selected context name, "" when none is.
func (d Detector) activeContext() (string, error) {
	if name, ok := d.LookupEnv("DOCKER_CONTEXT"); ok && name != "" {
		return name, nil
	}

	if d.ConfigDir == "" {
		// No home directory and no DOCKER_CONFIG. Joining onto "" would make
		// the path relative and read whatever config.json happens to sit in
		// the working directory, so there is nothing to consult here.
		return "", nil
	}

	blob, err := os.ReadFile(filepath.Join(d.ConfigDir, "config.json"))
	if errors.Is(err, fs.ErrNotExist) {
		// A machine that has never run the docker CLI has no config at all.
		return "", nil
	}
	if err != nil {
		return "", d.configError(fmt.Sprintf("cannot read docker's config.json: %v", err))
	}

	var cfg struct {
		CurrentContext string `json:"currentContext"`
	}
	if err := json.Unmarshal(blob, &cfg); err != nil {
		return "", d.configError(fmt.Sprintf("docker's config.json is not valid JSON: %v", err))
	}
	return cfg.CurrentContext, nil
}

// contextEndpoint reads the docker endpoint out of a stored context. The
// docker CLI names each context's directory for the SHA-256 of its name.
func (d Detector) contextEndpoint(name string) (string, error) {
	sum := sha256.Sum256([]byte(name))
	path := filepath.Join(d.ConfigDir, "contexts", "meta", hex.EncodeToString(sum[:]), "meta.json")

	blob, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", exitcode.New(
			exitcode.CodeEngineUnavailable,
			fmt.Sprintf("the active docker context %q is not defined on this machine", name),
			"run 'docker context ls' and select an existing one with 'docker context use'",
		)
	}
	if err != nil {
		return "", d.configError(fmt.Sprintf("cannot read the docker context %q: %v", name, err))
	}

	var meta struct {
		Endpoints struct {
			Docker struct {
				Host string `json:"Host"`
			} `json:"docker"`
		} `json:"Endpoints"`
	}
	if err := json.Unmarshal(blob, &meta); err != nil {
		return "", d.configError(fmt.Sprintf("the docker context %q has unreadable metadata: %v", name, err))
	}
	if meta.Endpoints.Docker.Host == "" {
		return "", d.configError(fmt.Sprintf("the docker context %q names no docker endpoint", name))
	}
	return meta.Endpoints.Docker.Host, nil
}

func (d Detector) configError(msg string) error {
	return exitcode.New(exitcode.CodeEngineUnavailable, msg,
		"fix it with 'docker context use <name>', or set DOCKER_HOST to bypass docker's config")
}

func dockerConfigDir(lookupEnv func(string) (string, bool)) string {
	if dir, ok := lookupEnv("DOCKER_CONFIG"); ok && dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		// No home means no docker config to read; detection falls through to
		// probing, which is the right answer rather than a hard failure.
		return ""
	}
	return filepath.Join(home, ".docker")
}

// endpointExists reports whether a candidate endpoint is present. Both unix
// sockets and Windows named pipes answer to a plain stat, and a stat neither
// connects nor wakes a stopped daemon — tamp wants "the socket is there",
// which is a different fact from "Docker answers" and is reported separately.
func endpointExists(host string) bool {
	path, ok := strings.CutPrefix(host, "unix://")
	if !ok {
		path, ok = strings.CutPrefix(host, "npipe://")
		if !ok {
			return false
		}
		path = filepath.FromSlash(path)
	}
	_, err := os.Stat(path)
	return err == nil
}
