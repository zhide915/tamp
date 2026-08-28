// Package toolchain provisions the Python and Node that tamp's version matrix
// pins for a Frappe release.
//
// Both land in one volume shared by every environment on the machine, so the
// second environment that wants Python 3.11 finds it already there. Nothing in
// here is per-environment: the same call from two environments with the same
// versions does the work once and is a no-op the second time.
package toolchain

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/zhide915/tamp/internal/engine"
)

const (
	// Volume holds uv, its Pythons, and nvm's Nodes. Every environment's bench
	// container mounts this one volume.
	Volume = "tamp-toolchain"

	// Dir is where that volume is mounted. It is under the bench image's own
	// user's home because that user has to be able to write to it.
	Dir = "/home/frappe/.tamp-toolchain"

	// EnvScript is sourced by every command tamp runs in a bench container,
	// and by the bench's own processes. It is the single place that says where
	// this machine's Pythons and Nodes are, so nothing else has to guess a
	// version-shaped path.
	EnvScript = Dir + "/env.sh"
)

// Request is one environment's toolchain, as its Frappe version pinned it.
type Request struct {
	// Container is the bench container to provision through.
	Container string
	// Python and Node are matrix versions — "3.11", "18" — not full ones.
	// uv and nvm resolve them to the newest patch release they know.
	Python string
	Node   string
	// Out receives uv's and nvm's own output, which is the only thing that
	// makes a several-minute first run legible.
	Out io.Writer
}

// Provision makes the requested Python and Node available in the shared
// volume, downloading only what is not already there.
func Provision(ctx context.Context, eng engine.Engine, req Request) error {
	if err := installUV(ctx, eng, req); err != nil {
		return err
	}
	if err := eng.WriteFile(ctx, req.Container, engine.FileSpec{
		Path: EnvScript,
		Data: []byte(envScript()),
		Mode: 0o644,
		UID:  UID,
		GID:  GID,
	}); err != nil {
		return err
	}
	return run(ctx, eng, req, provisionScript, req.Python, req.Node)
}

// installUV puts the pinned uv release in the shared volume unless that exact
// version is already there.
//
// The binary is downloaded by tamp on the host and copied in, rather than
// fetched by a shell inside the container: that is what makes the checksum
// check tamp's own rather than a pipe's, and it is why tamp never runs the
// `curl | sh` installer.
func installUV(ctx context.Context, eng engine.Engine, req Request) error {
	binary := uvPath()
	installed, err := isInstalled(ctx, eng, req.Container, binary)
	if err != nil {
		return err
	}
	if installed {
		return nil
	}

	arch, err := containerArch(ctx, eng, req.Container)
	if err != nil {
		return err
	}
	body, err := fetchUV(ctx, releaseBaseURL, arch)
	if err != nil {
		return err
	}

	// docker's copy extracts into a directory that already exists, so the
	// version's own directory has to be made before the binary can land in it.
	if err := run(ctx, eng, req, `set -e; mkdir -p "$(dirname "$1")"`, binary); err != nil {
		return err
	}
	return eng.WriteFile(ctx, req.Container, engine.FileSpec{
		Path: binary,
		Data: body,
		Mode: 0o755,
		UID:  UID,
		GID:  GID,
	})
}

// isInstalled reports whether an executable is already in the shared volume.
// A probe tamp could not run at all must not read as an empty toolchain that
// tamp would then try to fill in a container that is not there — which is
// exactly the distinction engine.Probe draws.
func isInstalled(ctx context.Context, eng engine.Engine, container, path string) (bool, error) {
	return engine.Probe(ctx, eng, engine.ExecRequest{
		Container: container,
		Cmd:       engine.Script(`test -x "$1"`, path),
	})
}

// containerArch asks the container what it is, because that — not the host —
// is what decides which uv build to fetch. A Mac on Apple silicon runs arm64
// Linux containers; the same Mac under Rosetta emulation runs amd64 ones.
func containerArch(ctx context.Context, eng engine.Engine, container string) (string, error) {
	var out bytes.Buffer
	err := eng.Exec(ctx, engine.ExecRequest{
		Container: container,
		Cmd:       []string{"uname", "-m"},
		Stdout:    &out,
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out.String()), nil
}

// run executes a script in the bench container, narrating it to the caller.
// Scripts that need the toolchain source EnvScript themselves, where the
// ordering is visible.
func run(ctx context.Context, eng engine.Engine, req Request, script string, args ...string) error {
	return eng.Exec(ctx, engine.ExecRequest{
		Container: req.Container,
		Cmd:       engine.Script(script, args...),
		Stdout:    req.Out,
		Stderr:    req.Out,
	})
}

// provisionScript installs the requested Python and Node into the shared
// volume. Every step is a no-op when what it installs is already there, so a
// second environment on the same versions runs it in seconds.
//
// nvm cannot simply be pointed at the shared volume: it is a shell script that
// lives in the directory it manages. So tamp seeds the shared copy from the
// one the bench image already carries, once, and installs into that from then
// on — which is what makes a Node survive the container it was installed from.
const provisionScript = `
set -eo pipefail
image_nvm="${NVM_DIR:-/home/frappe/.nvm}"
. ` + EnvScript + `

if [ ! -s "$NVM_DIR/nvm.sh" ]; then
  mkdir -p "$NVM_DIR"
  cp "$image_nvm/nvm.sh" "$image_nvm/nvm-exec" "$NVM_DIR/"
fi
. "$NVM_DIR/nvm.sh"

uv python install "$1"
nvm install "$2"
nvm alias default "$2"

# Asking npm rather than PATH: the bench image ships a yarn of its own, for the
# Node it was built with, and finding that one would leave this Node without.
npm ls --global --depth 0 yarn >/dev/null 2>&1 || npm install --global yarn
`

// envScript renders the file every bench container sources. It is regenerated
// on every create so that a tamp upgrade which bumps the pinned uv is picked
// up without anyone editing a file inside a volume.
func envScript() string {
	return fmt.Sprintf(`# Generated by tamp — do not edit.
#
# Sourced by every command tamp runs in this container, and by the bench's own
# processes. It points Python and Node at the machine-wide shared volume, so
# what one environment installs the next one finds.

export TAMP_TOOLCHAIN=%q
export UV_PYTHON_INSTALL_DIR="$TAMP_TOOLCHAIN/python"
export UV_CACHE_DIR="$TAMP_TOOLCHAIN/uv-cache"
export NVM_DIR="$TAMP_TOOLCHAIN/nvm"
export PATH=%q:"$PATH"

# Puts the default Node on PATH. Absent until the toolchain is provisioned,
# which is the one moment this file is sourced before nvm is there.
if [ -s "$NVM_DIR/nvm.sh" ]; then
  . "$NVM_DIR/nvm.sh"
fi
`, Dir, uvDir())
}

// The bench image runs as this user, deliberately at uid 1000 so that a bind
// mount from a Linux host lines up with the person who owns the files.
const (
	User = "frappe"
	UID  = 1000
	GID  = 1000
)
