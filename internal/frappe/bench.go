// Package frappe runs bench commands inside an environment's container and
// owns two files on the bench: the Procfile and the common site config. It
// knows nothing of tamp.toml, the registry, or compose — it is handed a
// container name and versions, which is what lets it test against the engine
// fake.
package frappe

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/toolchain"
)

// The bench sits one level below the workspace, as `bench init <name>` lays
// it out. One environment is one bench.
const (
	WorkspaceDir = "/workspace"
	BenchDir     = WorkspaceDir + "/frappe-bench"

	// EnvDir is the Python virtualenv: rebuilt when needed, never backed up.
	EnvDir = BenchDir + "/env"
	// SitesDir holds every site's files plus the shared config.
	SitesDir = BenchDir + "/sites"

	// ProcfilePath is tamp-owned; honcho runs it.
	ProcfilePath = BenchDir + "/Procfile"
	// CommonSiteConfigPath is inherited by every site on the bench.
	CommonSiteConfigPath = SitesDir + "/common_site_config.json"
)

// Machine-wide caches shared by every environment; their reuse is most of
// what makes a second create fast.
const (
	PipCacheVolume = "tamp-pip-cache"
	PipCacheDir    = "/home/frappe/.cache/pip"

	YarnCacheVolume = "tamp-yarn-cache"
	YarnCacheDir    = "/home/frappe/.cache/yarn"
)

// In-container ports, identical across environments: nothing is published to
// the host, so nothing can collide.
const (
	WebPort         = 8000
	SocketIOPort    = 9000
	FileWatcherPort = 6787
)

// BindAddr is the interface the bench's web server must listen on: the
// router reaches it across a Docker network, so loopback would answer nobody.
// Frappe's develop branch defaults to loopback and reads this from
// FRAPPE_BIND_ADDR; the release branches bind here anyway.
const BindAddr = "0.0.0.0"

// MailSMTPPort is Mailpit's SMTP listener.
const MailSMTPPort = 1025

// redisPort is Redis' stock port: each server gets its own container, so the
// offset ports a native bench uses are unnecessary.
const redisPort = 6379

// Bench is one environment's bench, addressed through its container.
type Bench struct {
	Engine    engine.Engine
	Container string

	// Branch is the Frappe branch to initialize from, e.g. "version-15".
	Branch string
	// Python and Node are the matrix versions for this Frappe release.
	Python string
	Node   string

	// Container names the environment's other services answer to on its network.
	DBHost     string
	RedisCache string
	RedisQueue string
	MailHost   string

	// Out streams bench, uv and nvm output — the only progress a minutes-long
	// init shows.
	Out io.Writer
}

// Provision prepares the shared directories and installs the pinned Python
// and Node. Toolchain and caches live in machine-wide volumes, so it is slow
// once per machine and near-instant after.
func (b *Bench) Provision(ctx context.Context) error {
	if err := b.prepareDirs(ctx); err != nil {
		return err
	}
	return toolchain.Provision(ctx, b.Engine, toolchain.Request{
		Container: b.Container,
		Python:    b.Python,
		Node:      b.Node,
		Out:       b.Out,
	})
}

// prepareDirs chowns the volume mount points to the bench user: Docker
// creates them root-owned, and the image runs as frappe. Deliberately
// non-recursive — the directories are empty at this point, and walking a
// populated toolchain volume would be slow.
func (b *Bench) prepareDirs(ctx context.Context) error {
	dirs := []string{
		WorkspaceDir, BenchDir, EnvDir, SitesDir, AppsDir,
		toolchain.Dir, PipCacheDir, YarnCacheDir, TemplateDir,
	}
	script := fmt.Sprintf("set -e; mkdir -p %[1]s; chown %[2]s:%[2]s %[1]s",
		strings.Join(dirs, " "), toolchain.User)
	return b.Engine.Exec(ctx, engine.ExecRequest{
		Container: b.Container,
		Cmd:       []string{"sh", "-c", script},
		User:      "root",
		Stdout:    b.Out,
		Stderr:    b.Out,
	})
}

// FrappeApp is on every bench; its presence marks an existing source tree.
const FrappeApp = "frappe"

// HasApp reports whether the app is in apps/. An unreachable container is an
// error, not "absent" — engine.Probe draws that line.
func (b *Bench) HasApp(ctx context.Context, name string) (bool, error) {
	return engine.Probe(ctx, b.Engine, engine.ExecRequest{
		Container: b.Container,
		Cmd:       engine.Script(`test -d "`+AppsDir+`/$1"`, name),
		WorkDir:   BenchDir,
	})
}

// Init clones Frappe and builds the bench around it — virtualenv, layout,
// apps.txt. It aborts when apps/frappe already exists, so a surviving source
// tree goes through Rebuild instead; which of the two a bench needs is the
// caller's to decide, because only the caller knows about the template store.
func (b *Bench) Init(ctx context.Context) error {
	return b.run(ctx, initScript, b.Branch, b.Python)
}

// Rebuild reconstructs a bench around apps already on disk: what bench init
// produces, minus the clone. It writes sites/apps.txt itself, since bench
// only writes it while cloning. Exported because re-adoption under Mutagen
// needs a second pass after the session mirrors the host's apps in.
func (b *Bench) Rebuild(ctx context.Context) error {
	return b.run(ctx, rebuildScript, b.Python, FrappeApp)
}

const rebuildScript = `
set -eo pipefail
. ` + toolchain.EnvScript + `
cd ` + BenchDir + `
mkdir -p sites logs config

# frappe first, because that is the order bench itself writes and the order
# Frappe loads hooks in.
{
  printf '%s\n' "$2"
  for entry in apps/*/; do
    name="${entry#apps/}"
    name="${name%/}"
    if [ -d "$entry" ] && [ "$name" != "$2" ]; then
      printf '%s\n' "$name"
    fi
  done
} > sites/apps.txt

bench setup env --python "$(uv python find "$1")"
bench setup requirements
`

// --skip-redis-config-generation and --no-procfile: tamp owns both.
// --ignore-exist: the volume mounts pre-create the bench directory, and
// without the flag bench init reports "already exists" and exits successfully
// having done nothing.
const initScript = `
set -eo pipefail
. ` + toolchain.EnvScript + `
cd ` + WorkspaceDir + `
bench init \
  --skip-redis-config-generation \
  --no-procfile \
  --ignore-exist \
  --frappe-branch "$1" \
  --python "$(uv python find "$2")" \
  ` + BenchDir + `
`

// SetupRequirements reinstalls the apps' Python and Node dependencies, from
// the machine-wide pip and yarn caches. It recreates the virtualenv first
// when there is none — after a deps clean there is not.
func (b *Bench) SetupRequirements(ctx context.Context) error {
	return b.run(ctx, setupRequirementsScript, b.Python)
}

const setupRequirementsScript = `
set -eo pipefail
. ` + toolchain.EnvScript + `
cd ` + BenchDir + `
if [ ! -x env/bin/python ]; then
  bench setup env --python "$(uv python find "$1")"
fi
bench setup requirements
`

// Configure writes the two tamp-owned files: the Procfile and the common
// site config.
func (b *Bench) Configure(ctx context.Context) error {
	if err := b.Engine.WriteFile(ctx, b.Container, engine.FileSpec{
		Path: ProcfilePath,
		Data: []byte(Procfile()),
		Mode: 0o644,
		UID:  toolchain.UID,
		GID:  toolchain.GID,
	}); err != nil {
		return err
	}
	return b.writeCommonSiteConfig(ctx)
}

// Procfile differs from bench's template on purpose: no redis lines (Redis
// runs in containers of its own) and no log redirection (everything goes to
// honcho's output, one place to watch).
func Procfile() string {
	return fmt.Sprintf(`# Generated by tamp — do not edit.
#
# tamp rewrites this file when it configures the bench. Redis is deliberately
# absent: this environment runs it in containers of its own.

web: bench serve --port %d
socketio: node apps/frappe/socketio.js
watch: bench watch
schedule: bench schedule
worker: bench worker
`, WebPort)
}

// writeCommonSiteConfig merges tamp's keys into the existing config: bench
// init writes keys tamp has no opinion about and must not discard.
func (b *Bench) writeCommonSiteConfig(ctx context.Context) error {
	config := map[string]any{}

	// A bench rebuilt around surviving source has no shared config yet;
	// nothing to preserve is fine.
	if existing, err := b.Engine.ReadFile(ctx, b.Container, CommonSiteConfigPath); err == nil {
		if err := json.Unmarshal(existing, &config); err != nil {
			return exitcode.New(exitcode.CodeFailed,
				fmt.Sprintf("%s in %s is not valid JSON: %v", CommonSiteConfigPath, b.Container, err),
				"remove the environment and create it again")
		}
	}
	for key, value := range b.siteConfig() {
		config[key] = value
	}

	body, err := json.MarshalIndent(config, "", " ")
	if err != nil {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot render %s: %v", CommonSiteConfigPath, err), "")
	}
	return b.Engine.WriteFile(ctx, b.Container, engine.FileSpec{
		Path: CommonSiteConfigPath,
		Data: append(body, '\n'),
		Mode: 0o644,
		UID:  toolchain.UID,
		GID:  toolchain.GID,
	})
}

// siteConfig is the set of keys tamp owns on a bench.
func (b *Bench) siteConfig() map[string]any {
	return map[string]any{
		"db_host":     b.DBHost,
		"redis_cache": redisURL(b.RedisCache),
		"redis_queue": redisURL(b.RedisQueue),
		// From v15 socketio shares the queue Redis; a third address would
		// start a server nothing reads.
		"redis_socketio": redisURL(b.RedisQueue),

		"socketio_port":     SocketIOPort,
		"webserver_port":    WebPort,
		"file_watcher_port": FileWatcherPort,

		// Makes Frappe reload changed Python and rebuild changed assets.
		"developer_mode": 1,

		// Route all outgoing mail to the catcher. When a site has no account,
		// Frappe builds one from these keys and then demands an SMTP password
		// nobody set — hence the auth switch; Mailpit accepts unauthenticated
		// mail. use_tls is the outgoing key; use_ssl belongs to incoming.
		"mail_server":                      b.MailHost,
		"mail_port":                        MailSMTPPort,
		"use_tls":                          0,
		"disable_mail_smtp_authentication": 1,
	}
}

func redisURL(host string) string {
	return fmt.Sprintf("redis://%s:%d", host, redisPort)
}

// WaitForWeb blocks until Werkzeug answers inside the container. honcho
// starting is not serving — the first request imports every app — and a
// create must not return before the environment can take one.
func (b *Bench) WaitForWeb(ctx context.Context) error {
	return b.run(ctx, waitScript, fmt.Sprint(WebPort), fmt.Sprint(webAttempts), fmt.Sprint(webInterval))
}

// Generous ceiling: a cold bench spends most of it importing apps, and giving
// up would cost the user the whole create.
const (
	webAttempts = 90
	webInterval = 2
)

// Polled from inside the container: in router mode port 8000 is not published
// to the host. Any HTTP response counts, error pages included — the question
// is whether Werkzeug is serving.
const waitScript = `
for _ in $(seq 1 "$2"); do
  if curl -s -o /dev/null --max-time 5 "http://127.0.0.1:$1/"; then
    exit 0
  fi
  sleep "$3"
done
echo "the bench web server did not answer on port $1" >&2
exit 1
`

func (b *Bench) run(ctx context.Context, script string, args ...string) error {
	return b.Engine.Exec(ctx, engine.ExecRequest{
		Container: b.Container,
		Cmd:       engine.Script(script, args...),
		WorkDir:   BenchDir,
		Stdout:    b.Out,
		Stderr:    b.Out,
	})
}
