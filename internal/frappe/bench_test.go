package frappe_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/engine/enginetest"
	"github.com/zhide915/tamp/internal/frappe"
)

// bench builds the bench under test against a healthy engine fake, wired to
// the container names a real environment's compose project produces.
func bench(t *testing.T) (*frappe.Bench, *enginetest.Fake) {
	t.Helper()
	fake := enginetest.Running()
	return &frappe.Bench{
		Engine:     fake,
		Container:  "tamp-demo-ab12cd-frappe-1",
		Branch:     "version-15",
		Python:     "3.11",
		Node:       "18",
		DBHost:     "mariadb",
		RedisCache: "redis-cache",
		RedisQueue: "redis-queue",
		MailHost:   "mailpit",
	}, fake
}

// initialized is a bench that has been through bench init, which is the only
// state Configure is ever called in: it merges into the config bench wrote.
func initialized(t *testing.T) (*frappe.Bench, *enginetest.Fake) {
	t.Helper()
	b, fake := bench(t)
	if _, err := b.Materialize(t.Context()); err != nil {
		t.Fatalf("Init = %v", err)
	}
	return b, fake
}

// --- the process file ------------------------------------------------------

// Redis runs in containers of its own, so a bench that started its own would
// be a second server nothing reads and a first one nothing writes to.
func TestTheProcessFileStartsNoRedis(t *testing.T) {
	if strings.Contains(frappe.Procfile(), "redis") {
		t.Errorf("the Procfile starts a redis:\n%s", frappe.Procfile())
	}
}

// The five processes a dev bench is: serve, sockets, assets, scheduler,
// worker. Dropping any one of them breaks something the user will blame on
// Frappe.
func TestTheProcessFileRunsEveryBenchProcess(t *testing.T) {
	for _, process := range []string{"web:", "socketio:", "watch:", "schedule:", "worker:"} {
		if !strings.Contains(frappe.Procfile(), process) {
			t.Errorf("the Procfile has no %s line:\n%s", process, frappe.Procfile())
		}
	}
	if !strings.Contains(frappe.Procfile(), "--port 8000") {
		t.Errorf("the web process does not serve on 8000:\n%s", frappe.Procfile())
	}
}

func TestConfiguringWritesTheProcessFileOntoTheBench(t *testing.T) {
	b, fake := initialized(t)

	if err := b.Configure(t.Context()); err != nil {
		t.Fatalf("Configure = %v", err)
	}

	got, ok := fake.Wrote(frappe.ProcfilePath)
	if !ok {
		t.Fatalf("tamp wrote %v, not %s", fake.Written(), frappe.ProcfilePath)
	}
	if got != frappe.Procfile() {
		t.Errorf("the Procfile on the bench is not the one tamp generates:\n%s", got)
	}
}

// --- the shared site config ------------------------------------------------

// The keys that make a bench talk to this environment's containers rather than
// to the localhost services a native bench would expect.
func TestConfiguringPointsTheBenchAtTheEnvironmentsContainers(t *testing.T) {
	b, fake := initialized(t)

	if err := b.Configure(t.Context()); err != nil {
		t.Fatalf("Configure = %v", err)
	}
	config := siteConfig(t, fake)

	want := map[string]any{
		"db_host":     "mariadb",
		"redis_cache": "redis://redis-cache:6379",
		"redis_queue": "redis://redis-queue:6379",
		// From v15 the socketio server shares the queue instance; pointing
		// this at a third address would start a server nothing reads.
		"redis_socketio": "redis://redis-queue:6379",
		"mail_server":    "mailpit",
	}
	for key, value := range want {
		if config[key] != value {
			t.Errorf("%s = %v, want %v", key, config[key], value)
		}
	}
}

// A development environment says so: this is what makes Frappe reload changed
// Python and rebuild changed assets, which is the whole point of tamp.
func TestConfiguringPutsTheBenchInDeveloperMode(t *testing.T) {
	b, fake := initialized(t)

	if err := b.Configure(t.Context()); err != nil {
		t.Fatalf("Configure = %v", err)
	}
	config := siteConfig(t, fake)

	numbers := map[string]float64{
		"developer_mode":    1,
		"webserver_port":    8000,
		"socketio_port":     9000,
		"file_watcher_port": 6787,
		"mail_port":         1025,
	}
	for key, value := range numbers {
		if config[key] != value {
			t.Errorf("%s = %v, want %v", key, config[key], value)
		}
	}
}

// Pointing the bench at the catcher is not enough on its own. Frappe builds an
// outgoing account out of these keys when a site has none of its own, and then
// looks up an SMTP password nobody ever set — so without the switch below every
// mail a site sends fails on a credential the catcher does not want.
func TestConfiguringLetsTheBenchSendToACatcherThatWantsNoPassword(t *testing.T) {
	b, fake := initialized(t)

	if err := b.Configure(t.Context()); err != nil {
		t.Fatalf("Configure = %v", err)
	}
	config := siteConfig(t, fake)

	if config["disable_mail_smtp_authentication"] != float64(1) {
		t.Errorf("disable_mail_smtp_authentication = %v, so every send asks for a password that does not exist",
			config["disable_mail_smtp_authentication"])
	}
	// use_tls is the key Frappe reads for an outgoing account. use_ssl is the
	// incoming one, and tamp configures no incoming account at all.
	if config["use_tls"] != float64(0) {
		t.Errorf("use_tls = %v, want 0", config["use_tls"])
	}
	if _, ok := config["use_ssl"]; ok {
		t.Error("tamp still writes use_ssl, which says nothing about the connection it is describing")
	}
}

// bench init writes things tamp has no opinion about, and tamp adding its
// own keys must not cost the bench them.
func TestConfiguringKeepsTheKeysBenchInitWrote(t *testing.T) {
	b, fake := initialized(t)

	if err := b.Configure(t.Context()); err != nil {
		t.Fatalf("Configure = %v", err)
	}
	config := siteConfig(t, fake)

	var initial map[string]any
	if err := json.Unmarshal([]byte(enginetest.BenchInitConfig), &initial); err != nil {
		t.Fatalf("the fake's bench init config is not JSON: %v", err)
	}
	for key, value := range initial {
		if _, tamps := tampOwns[key]; tamps {
			continue
		}
		if config[key] != value {
			t.Errorf("%s = %v after configuring, want bench's own %v", key, config[key], value)
		}
	}
}

// The keys tamp deliberately overwrites, which is why they are excluded from
// the check above.
var tampOwns = map[string]struct{}{"db_host": {}, "redis_cache": {}}

func siteConfig(t *testing.T, fake *enginetest.Fake) map[string]any {
	t.Helper()
	body, ok := fake.Wrote(frappe.CommonSiteConfigPath)
	if !ok {
		t.Fatalf("tamp wrote %v, not %s", fake.Written(), frappe.CommonSiteConfigPath)
	}
	config := map[string]any{}
	if err := json.Unmarshal([]byte(body), &config); err != nil {
		t.Fatalf("%s is not valid JSON: %v\n%s", frappe.CommonSiteConfigPath, err, body)
	}
	return config
}

// --- initializing ----------------------------------------------------------

// The flags that make bench init produce a bench tamp can run: no Redis
// config and no Procfile, because tamp owns both, and the branch and Python
// its version matrix chose.
func TestInitLeavesRedisAndTheProcessFileToTamp(t *testing.T) {
	b, fake := bench(t)

	if _, err := b.Materialize(t.Context()); err != nil {
		t.Fatalf("Init = %v", err)
	}

	init := lastExec(t, fake)
	for _, flag := range []string{"--skip-redis-config-generation", "--no-procfile", "--frappe-branch"} {
		if !strings.Contains(init.Line(), flag) {
			t.Errorf("bench init was run without %s:\n%s", flag, init.Line())
		}
	}
	// The interpreter is whichever one uv installed, resolved in the
	// container: tamp pins a version, and only uv knows the path.
	if !strings.Contains(init.Line(), "uv python find") {
		t.Errorf("bench init did not take the Python tamp provisioned:\n%s", init.Line())
	}
	if got := init.Cmd[len(init.Cmd)-2:]; got[0] != "version-15" || got[1] != "3.11" {
		t.Errorf("bench init was asked for %v, want version-15 on python 3.11", got)
	}
}

// The bench directory already exists — tamp's volumes made it — and without
// --ignore-exist bench init reports that and returns success without doing
// anything, which would leave tamp with an empty bench it believes in.
func TestInitIsToldTheBenchDirectoryAlreadyExists(t *testing.T) {
	b, fake := bench(t)

	if _, err := b.Materialize(t.Context()); err != nil {
		t.Fatalf("Init = %v", err)
	}

	if !strings.Contains(lastExec(t, fake).Line(), "--ignore-exist") {
		t.Errorf("bench init was run without --ignore-exist:\n%s", lastExec(t, fake).Line())
	}
}

// --- provisioning ----------------------------------------------------------

// Docker creates a named volume's mount point owned by root, and the bench
// runs as someone else. Without this first pass nothing tamp does afterwards
// can write a single file.
func TestProvisioningMakesTheVolumeMountPointsWritableFirst(t *testing.T) {
	b, fake := bench(t)

	if err := b.Provision(t.Context()); err != nil {
		t.Fatalf("Provision = %v", err)
	}

	first := fake.Execs[0]
	if first.User != "root" {
		t.Fatalf("tamp's first command in the container ran as %q, want root:\n%s", first.User, first.Line())
	}
	for _, dir := range []string{frappe.BenchDir, frappe.EnvDir, frappe.SitesDir, frappe.PipCacheDir} {
		if !strings.Contains(first.Line(), dir) {
			t.Errorf("%s was left owned by root:\n%s", dir, first.Line())
		}
	}
}

// --- waiting ---------------------------------------------------------------

// "Created" has to mean "serving". honcho starting is not the same thing: the
// first request imports every app on the bench, and a create that returned
// before that would hand back an environment that is not ready.
func TestWaitingPollsTheWebServerInsideTheContainer(t *testing.T) {
	b, fake := bench(t)

	if err := b.WaitForWeb(t.Context()); err != nil {
		t.Fatalf("WaitForWeb = %v", err)
	}

	wait := lastExec(t, fake)
	if !strings.Contains(wait.Line(), "curl") {
		t.Errorf("tamp did not ask the web server for anything:\n%s", wait.Line())
	}
	// Inside the container, because in router mode nothing publishes 8000 to
	// the host — there is nowhere else to ask from.
	if !strings.Contains(wait.Line(), "127.0.0.1") {
		t.Errorf("tamp polled something other than the container itself:\n%s", wait.Line())
	}
	if wait.Cmd[len(wait.Cmd)-3] != "8000" {
		t.Errorf("tamp polled port %s, want 8000", wait.Cmd[len(wait.Cmd)-3])
	}
}

func lastExec(t *testing.T, fake *enginetest.Fake) enginetest.Exec {
	t.Helper()
	if len(fake.Execs) == 0 {
		t.Fatal("tamp ran nothing in the container")
	}
	return fake.Execs[len(fake.Execs)-1]
}

// --- handing git to the host -----------------------------------------------

// A repository cloned in the container and read on Windows is misread three
// ways, all of them tamp's doing. Each setting answers one, and the three of
// them together are the difference between a clean host-side git status and a
// tree that looks modified everywhere it is not.
func TestGitIsSettledForAHostThatCannotDescribeWhatLinuxWrote(t *testing.T) {
	b, fake := bench(t)

	if err := b.HandGitToHost(t.Context()); err != nil {
		t.Fatalf("HandGitToHost = %v", err)
	}

	settings := map[string]string{
		// NTFS cannot store the executable bit, so comparing it calls every
		// such file modified with nothing changed in it.
		"core.fileMode false": "the executable bit",
		// Git for Windows rewrites line endings on checkout, which would put
		// CRLF into files the Linux container is about to execute.
		"core.autocrlf false": "line endings",
		// Frappe's own paths go past the 260 characters Windows allows, and
		// git reports what it cannot read as changed.
		"core.longpaths true": "long paths",
	}
	for setting, what := range settings {
		if !fake.Ran(setting) {
			t.Errorf("tamp left %s for the host's git to misread — expected %q", what, setting)
		}
	}
}

// It runs over the apps directory rather than one named app, because tamp
// does not know which repositories are there: one arrived with bench init and
// the rest were fetched, some of them through the exec bridge.
func TestGitIsSettledForEveryAppOnTheBench(t *testing.T) {
	b, fake := bench(t)

	if err := b.HandGitToHost(t.Context()); err != nil {
		t.Fatalf("HandGitToHost = %v", err)
	}
	if !fake.Ran(frappe.AppsDir) {
		t.Error("tamp settled git somewhere other than the bench's apps directory")
	}
}
