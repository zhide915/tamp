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

// Docker is the real engine: the Docker API for inspection, and the docker CLI
// for compose. It is tamp's only implementation of Engine (Docker is the one
// supported engine), and the only code in tamp that a test cannot run.
type Docker struct {
	detector Detector

	// Detection stats the filesystem and reads docker's config, and several
	// checks in one command would otherwise repeat it, so the result — success
	// or failure — is resolved once.
	once sync.Once
	addr Address
	api  *client.Client
	err  error
}

// New returns the engine tamp talks to. Detection is deferred to the first
// call, so building the command tree never touches the filesystem and `tamp
// --help` works on a machine with no Docker at all.
func New() *Docker {
	return &Docker{detector: NewDetector(os.LookupEnv)}
}

func (d *Docker) connect() (Address, *client.Client, error) {
	d.once.Do(func() {
		d.addr, d.err = d.detector.Detect()
		if d.err != nil {
			return
		}
		// No version is pinned: tamp must work against whatever engine the
		// user has, and this client negotiates the API version by default.
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
		// The socket is there but nothing is listening on it — the ordinary
		// "Docker Desktop is not running" case, which deserves its own words
		// rather than the API client's.
		return Info{}, exitcode.New(
			exitcode.CodeEngineUnavailable,
			fmt.Sprintf("Docker is not answering at %s: %v", addr, rootCause(err)),
			"start Docker and try again",
		)
	}

	return Info{Address: addr, Version: v.Version}, nil
}

func (d *Docker) ComposeVersion(ctx context.Context) (string, error) {
	// No DOCKER_HOST is set on this invocation on purpose: reporting the
	// version asks the plugin about itself and must keep working while the
	// daemon is down, which is exactly when doctor is run.
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

// parseComposeVersion turns `docker compose version --short` output into a
// bare version, rejecting anything before v2. tamp generates compose files
// that v1 cannot read, so a v1 install is an unusable engine, not a warning.
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

// rootCause digs out the innermost error.
//
// The Docker client wraps a dial failure in several layers that each restate
// the endpoint ("error during connect: Get "http://…/version": dial tcp …"),
// so tamp's own line would name the address three times over. The innermost
// error is the part tamp cannot say for itself — refused, timed out, denied —
// and it is what distinguishes "Docker is stopped" from "Docker is running and
// you are not in the docker group".
func rootCause(err error) error {
	for {
		inner := errors.Unwrap(err)
		if inner == nil {
			return err
		}
		err = inner
	}
}
