// Package frappe is tamp's bench choreography: the bench commands tamp runs
// inside an environment's container, and the two files tamp owns on the bench
// — the process file and the shared site config.
//
// Nothing here knows about tamp.toml, the registry or compose. It is handed a
// container name and the versions to build for, which is what lets the whole
// of it run in a test against the recording engine fake.
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

// Where a bench lives inside its container.
//
// The bench sits one level below the workspace because that is what `bench
// init <name>` produces, and because the directory above it is where a second
// thing — a second bench, a scratch clone — would go if tamp ever allowed
// one. It does not: one environment is one bench.
const (
	WorkspaceDir = "/workspace"
	BenchDir     = WorkspaceDir + "/frappe-bench"

	// EnvDir is the Python virtualenv — the deps layer, thrown away and
	// rebuilt rather than backed up.
	EnvDir = BenchDir + "/env"
	// SitesDir holds every site's files and the config below.
	SitesDir = BenchDir + "/sites"

	// ProcfilePath is the process file tamp owns. honcho reads it.
	ProcfilePath = BenchDir + "/Procfile"
	// CommonSiteConfigPath is the config every site on the bench inherits.
	CommonSiteConfigPath = SitesDir + "/common_site_config.json"
)

// The caches shared by every environment on the machine. Wheels and node
// packages are the same bytes for every bench, and fetching them once per
// machine rather than once per environment is most of what makes a second
// create fast.
const (
	PipCacheVolume = "tamp-pip-cache"
	PipCacheDir    = "/home/frappe/.cache/pip"

	YarnCacheVolume = "tamp-yarn-cache"
	YarnCacheDir    = "/home/frappe/.cache/yarn"
)

// The ports a bench's processes listen on inside the container.
//
// They are the same in every environment: nothing is published to the host, so
// there is nothing to collide, and the router reaches each bench over its own
// network by container name.
const (
	WebPort         = 8000
	SocketIOPort    = 9000
	FileWatcherPort = 6787
)

// MailSMTPPort is where Mailpit accepts the mail a site sends.
const MailSMTPPort = 1025

// redisPort is the stock port each Redis container listens on. Native benches
// use 13000 and 11000 to keep two servers apart on one host; tamp gives each
// of them a container, so both can have the ordinary one.
const redisPort = 6379

// Bench is one environment's bench, addressed through its container.
type Bench struct {
	Engine    engine.Engine
	Container string

	// Branch is the Frappe branch to initialize from — "version-15".
	Branch string
	// Python and Node are the matrix versions this Frappe release wants.
	Python string
	Node   string

	// The environment's other containers, by the names they answer to on its
	// network.
	DBHost     string
	RedisCache string
	RedisQueue string
	MailHost   string

	// Out receives everything bench, uv and nvm say for themselves. A bench
	// init is minutes of real work, and tamp narrating "please wait" over the
	// top of it would be worse than useless.
	Out io.Writer
}

// Provision makes the container's shared directories usable and installs the
// Python and Node this bench's Frappe version pins.
//
// It is the step that is slow once per machine and instant afterwards: the
// toolchain and the package caches live in volumes shared by every
// environment, so the second bench on the same versions finds them ready.
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

// prepareDirs makes every volume mount point writable by the user the bench
// runs as.
//
// Docker creates the mount point for a named volume when the image has no
// directory there, and what it creates is owned by root. The bench image runs
// as frappe, so without this first pass as root nothing tamp does afterwards
// can write a single file. The chown is not recursive on purpose: these
// directories are empty when this runs, and walking a populated toolchain
// volume on every create would cost real time for nothing.
func (b *Bench) prepareDirs(ctx context.Context) error {
	dirs := []string{
		WorkspaceDir, BenchDir, EnvDir, SitesDir,
		toolchain.Dir, PipCacheDir, YarnCacheDir,
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

// Init materializes the bench itself: the virtualenv, the frappe app, and the
// directory layout every later command assumes.
func (b *Bench) Init(ctx context.Context) error {
	return b.run(ctx, initScript, b.Branch, b.Python)
}

// initScript runs bench init against the toolchain tamp provisioned.
//
// Three of its flags are tamp's whole relationship with bench init.
// --skip-redis-config-generation and --no-procfile because Redis runs in
// containers and tamp writes the process file itself, so bench must not
// generate either. --ignore-exist because the volumes tamp mounts have
// already created the bench directory, and without the flag bench reports
// "already exists" and returns success without doing anything at all.
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

// Configure writes the two files tamp owns on the bench: the process file
// honcho runs, and the config every site inherits.
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

// Procfile is the process file tamp writes onto every bench.
//
// It differs from bench's own template in two ways, both deliberate. There are
// no redis lines: tamp runs Redis as containers, so a bench that started its
// own would be two servers fighting over one dataset. And the worker does not
// redirect into logs/: everything a process says goes to honcho's output, so
// that one place shows what the environment is doing.
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

// writeCommonSiteConfig merges tamp's keys into the config bench init wrote.
//
// It is a merge rather than a rewrite because bench puts things there that
// tamp has no opinion about — which user owns the bench, how many background
// workers to run — and replacing the file wholesale would silently discard
// them.
func (b *Bench) writeCommonSiteConfig(ctx context.Context) error {
	existing, err := b.Engine.ReadFile(ctx, b.Container, CommonSiteConfigPath)
	if err != nil {
		return err
	}

	config := map[string]any{}
	if err := json.Unmarshal(existing, &config); err != nil {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("%s in %s is not valid JSON: %v", CommonSiteConfigPath, b.Container, err),
			"remove the environment and create it again")
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
		// From v15 the socketio server shares the queue instance: bench merged
		// the third Redis into the second, and pointing this key anywhere else
		// would start a server nothing reads.
		"redis_socketio": redisURL(b.RedisQueue),

		"socketio_port":     SocketIOPort,
		"webserver_port":    WebPort,
		"file_watcher_port": FileWatcherPort,

		// This is a development environment and says so: it is what makes
		// Frappe reload changed Python and rebuild changed assets.
		"developer_mode": 1,

		// Every mail the environment sends is caught rather than delivered.
		"mail_server": b.MailHost,
		"mail_port":   MailSMTPPort,
		"use_ssl":     0,
	}
}

func redisURL(host string) string {
	return fmt.Sprintf("redis://%s:%d", host, redisPort)
}

// WaitForWeb blocks until the bench's web server answers inside the container.
//
// It is what makes "created" mean "working". honcho starting is not the same
// as Werkzeug serving — the first request has to import every app on the bench
// — and a create that returned before that would hand the user an environment
// that is not ready for the next thing they type.
func (b *Bench) WaitForWeb(ctx context.Context) error {
	return b.run(ctx, waitScript, fmt.Sprint(WebPort), fmt.Sprint(webAttempts), fmt.Sprint(webInterval))
}

// How long tamp waits for the first response. A cold bench spends most of it
// importing apps, and the ceiling is generous because the alternative — giving
// up on a working environment — costs the user the whole create.
const (
	webAttempts = 90
	webInterval = 2
)

// waitScript polls the web port from inside the container, which is the only
// place it is reachable: in router mode nothing publishes 8000 to the host.
//
// Any HTTP answer counts, including an error page. The question here is
// whether Werkzeug is serving, and a bench with no sites yet has every right
// to refuse the request it is serving.
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

// run executes a script inside the bench container, from the bench directory,
// narrating it to the caller.
func (b *Bench) run(ctx context.Context, script string, args ...string) error {
	return b.Engine.Exec(ctx, engine.ExecRequest{
		Container: b.Container,
		Cmd:       engine.Script(script, args...),
		WorkDir:   BenchDir,
		Stdout:    b.Out,
		Stderr:    b.Out,
	})
}
