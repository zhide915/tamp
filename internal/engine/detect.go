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

// Source names how a Docker address was found; printed so users can see
// which detection path answered.
type Source string

const (
	SourceEnv     Source = "DOCKER_HOST"
	SourceContext Source = "docker context"
	SourceProbe   Source = "probed socket"
)

// Address is the resolved Docker endpoint and its provenance.
type Address struct {
	// Host is a docker connection string: unix://, npipe://, tcp:// or ssh://.
	Host   string
	Source Source
	// Context is set only when Source is SourceContext.
	Context string
}

func (a Address) String() string {
	if a.Context != "" {
		return fmt.Sprintf("%s (%s %q)", a.Host, a.Source, a.Context)
	}
	return fmt.Sprintf("%s (%s)", a.Host, a.Source)
}

// Both endpoints are probed on every OS: the stat for the wrong platform
// simply fails, keeping the order identical everywhere.
var defaultCandidates = []string{
	`npipe:////./pipe/docker_engine`,
	"unix:///var/run/docker.sock",
}

// Detector resolves the Docker endpoint. Its fields are its entire contact
// with the outside world, so tests can run without Docker installed.
type Detector struct {
	// ConfigDir is docker's config directory (~/.docker, or DOCKER_CONFIG).
	ConfigDir  string
	LookupEnv  func(string) (string, bool)
	Candidates []string
	// Exists reports whether a candidate endpoint is present on this machine.
	Exists func(host string) bool
}

// NewDetector builds the production Detector. Pass os.LookupEnv.
func NewDetector(lookupEnv func(string) (string, bool)) Detector {
	return Detector{
		ConfigDir:  dockerConfigDir(lookupEnv),
		LookupEnv:  lookupEnv,
		Candidates: defaultCandidates,
		Exists:     endpointExists,
	}
}

// Detect resolves the endpoint in order: DOCKER_HOST, the active docker
// context, then the well-known sockets. Finding nothing is an
// engine-unavailable error, never an empty result.
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
// with nil error means no context is selected; probing continues.
func (d Detector) fromContext() (Address, error) {
	name, err := d.activeContext()
	if err != nil {
		return Address{}, err
	}
	// "default" means the platform's built-in socket; it has no metadata on
	// disk.
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
		// Joining onto "" would produce a relative path and read a stray
		// config.json from the working directory.
		return "", nil
	}

	blob, err := os.ReadFile(filepath.Join(d.ConfigDir, "config.json"))
	if errors.Is(err, fs.ErrNotExist) {
		// The docker CLI has never run here; there is no config.
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

// contextEndpoint reads the endpoint from a stored context; the docker CLI
// names each context's directory for the SHA-256 of its name.
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
		// No home, no config to read; detection falls through to probing.
		return ""
	}
	return filepath.Join(home, ".docker")
}

// endpointExists stats a unix socket or Windows named pipe. Deliberately a
// stat, not a connect: "the socket is there" and "Docker answers" are
// reported as separate facts.
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
