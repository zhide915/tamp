package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"github.com/moby/moby/client"
	"github.com/zhide915/tamp/internal/exitcode"
)

// Docker is the real Engine: the Docker API for inspection, the docker CLI
// for compose. It is the only Engine implementation and the only code tests
// cannot run.
type Docker struct {
	detector Detector

	// Detection is resolved once — success or failure — so multiple checks
	// in one command do not repeat the filesystem and config reads.
	once sync.Once
	addr Address
	api  *client.Client
	err  error
}

// New returns the production engine. Detection is deferred to first use so
// `tamp --help` works on a machine with no Docker.
func New() *Docker {
	return &Docker{detector: NewDetector(os.LookupEnv)}
}

func (d *Docker) connect() (Address, *client.Client, error) {
	d.once.Do(func() {
		d.addr, d.err = d.detector.Detect()
		if d.err != nil {
			return
		}
		// No pinned API version: the client negotiates with whatever engine
		// the user has.
		d.api, d.err = client.New(client.WithHost(d.addr.Host))
		if d.err != nil {
			d.err = exitcode.New(
				exitcode.CodeEngineUnavailable,
				fmt.Sprintf("cannot use the Docker endpoint %s: %v", d.addr, d.err),
				"check DOCKER_HOST and 'docker context ls' name an endpoint tamp can reach",
			)
		}
	})
	return d.addr, d.api, d.err
}

func (d *Docker) Ping(ctx context.Context) (Info, error) {
	addr, api, err := d.connect()
	if err != nil {
		return Info{}, err
	}

	v, err := api.ServerVersion(ctx, client.ServerVersionOptions{})
	if err != nil {
		// Socket present, daemon not answering: the everyday "Docker Desktop
		// is not running" case.
		return Info{}, exitcode.New(
			exitcode.CodeEngineUnavailable,
			fmt.Sprintf("Docker is not answering at %s: %v", addr, rootCause(err)),
			"start Docker and try again",
		)
	}

	return Info{Address: addr, Version: v.Version}, nil
}

func (d *Docker) ComposeVersion(ctx context.Context) (string, error) {
	// No DOCKER_HOST here on purpose: the plugin reports its own version and
	// must keep working while the daemon is down.
	out, err := exec.CommandContext(ctx, "docker", "compose", "version", "--short").Output()
	if err != nil {
		return "", exitcode.New(
			exitcode.CodeEngineUnavailable,
			fmt.Sprintf("'docker compose' is not available: %v", err),
			"install Docker Desktop, or add the compose v2 plugin to your docker CLI",
		)
	}
	return parseComposeVersion(string(out))
}

// parseComposeVersion extracts a bare version and rejects anything before
// v2: tamp's generated files are unreadable by compose v1.
func parseComposeVersion(out string) (string, error) {
	v := strings.TrimPrefix(strings.TrimSpace(out), "v")

	major, _, _ := strings.Cut(v, ".")
	n, err := strconv.Atoi(major)
	if err != nil {
		return "", exitcode.New(
			exitcode.CodeEngineUnavailable,
			fmt.Sprintf("cannot read the compose version from %q", strings.TrimSpace(out)),
			"check that 'docker compose version' prints a version",
		)
	}
	if n < 2 {
		return "", exitcode.New(
			exitcode.CodeEngineUnavailable,
			fmt.Sprintf("compose %s is too old; tamp needs compose v2", v),
			"upgrade Docker Desktop, or install the compose v2 plugin",
		)
	}
	return v, nil
}

// rootCause returns the innermost error. The Docker client's wrapping
// restates the endpoint at every layer; only the innermost reason —
// refused, timed out, denied — is worth repeating.
func rootCause(err error) error {
	for {
		inner := errors.Unwrap(err)
		if inner == nil {
			return err
		}
		err = inner
	}
}
