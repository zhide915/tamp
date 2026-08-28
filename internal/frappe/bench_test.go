package frappe_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/engine/enginetest"
	"github.com/zhide915/tamp/internal/frappe"
)

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

// initialized has been through bench init — the only state Configure runs in,
// since it merges into the config init wrote.
func initialized(t *testing.T) (*frappe.Bench, *enginetest.Fake) {
	t.Helper()
	b, fake := bench(t)
	if err := b.Init(t.Context()); err != nil {
		t.Fatalf("Init = %v", err)
	}
	return b, fake
}

// Redis runs in containers of its own; a bench-started one would fight over
// the same dataset.
func TestTheProcessFileStartsNoRedis(t *testing.T) {
	if strings.Contains(frappe.Procfile(), "redis") {
		t.Errorf("the Procfile starts a redis:\n%s", frappe.Procfile())
	}
}

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
		// From v15 socketio shares the queue Redis.
		"redis_socketio": "redis://redis-queue:6379",
		"mail_server":    "mailpit",
	}
	for key, value := range want {
		if config[key] != value {
			t.Errorf("%s = %v, want %v", key, config[key], value)
		}
	}
}

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

// Without the auth switch, Frappe builds an outgoing account from these keys
// and then fails every send on an SMTP password nobody set.
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
	// use_tls is the outgoing key; use_ssl belongs to incoming.
	if config["use_tls"] != float64(0) {
		t.Errorf("use_tls = %v, want 0", config["use_tls"])
	}
	if _, ok := config["use_ssl"]; ok {
		t.Error("tamp still writes use_ssl, which says nothing about the connection it is describing")
	}
}

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

// Keys tamp deliberately overwrites.
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

func TestInitLeavesRedisAndTheProcessFileToTamp(t *testing.T) {
	b, fake := bench(t)

	if err := b.Init(t.Context()); err != nil {
		t.Fatalf("Init = %v", err)
	}

	init := lastExec(t, fake)
	for _, flag := range []string{"--skip-redis-config-generation", "--no-procfile", "--frappe-branch"} {
		if !strings.Contains(init.Line(), flag) {
			t.Errorf("bench init was run without %s:\n%s", flag, init.Line())
		}
	}
	// Only uv knows where the pinned interpreter landed.
	if !strings.Contains(init.Line(), "uv python find") {
		t.Errorf("bench init did not take the Python tamp provisioned:\n%s", init.Line())
	}
	if got := init.Cmd[len(init.Cmd)-2:]; got[0] != "version-15" || got[1] != "3.11" {
		t.Errorf("bench init was asked for %v, want version-15 on python 3.11", got)
	}
}

// The volumes pre-create the bench directory; without --ignore-exist bench
// init exits successfully having done nothing.
func TestInitIsToldTheBenchDirectoryAlreadyExists(t *testing.T) {
	b, fake := bench(t)

	if err := b.Init(t.Context()); err != nil {
		t.Fatalf("Init = %v", err)
	}

	if !strings.Contains(lastExec(t, fake).Line(), "--ignore-exist") {
		t.Errorf("bench init was run without --ignore-exist:\n%s", lastExec(t, fake).Line())
	}
}

// Docker creates volume mount points root-owned; the bench user cannot write
// anywhere until they are chowned.
func TestProvisioningMakesTheVolumeMountPointsWritableFirst(t *testing.T) {
	b, fake := bench(t)

	if err := b.Provision(t.Context()); err != nil {
		t.Fatalf("Provision = %v", err)
	}

	first := fake.Execs[0]
	if first.User != "root" {
		t.Fatalf("tamp's first command in the container ran as %q, want root:\n%s", first.User, first.Line())
	}
	for _, dir := range []string{
		frappe.BenchDir, frappe.EnvDir, frappe.SitesDir, frappe.PipCacheDir, frappe.TemplateDir,
	} {
		if !strings.Contains(first.Line(), dir) {
			t.Errorf("%s was left owned by root:\n%s", dir, first.Line())
		}
	}
}

// honcho starting is not serving: the first request imports every app.
func TestWaitingPollsTheWebServerInsideTheContainer(t *testing.T) {
	b, fake := bench(t)

	if err := b.WaitForWeb(t.Context()); err != nil {
		t.Fatalf("WaitForWeb = %v", err)
	}

	wait := lastExec(t, fake)
	if !strings.Contains(wait.Line(), "curl") {
		t.Errorf("tamp did not ask the web server for anything:\n%s", wait.Line())
	}
	// In router mode nothing publishes 8000 to the host.
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

// Each setting answers one way a container-made clone misreads on Windows.
func TestGitIsSettledForAHostThatCannotDescribeWhatLinuxWrote(t *testing.T) {
	b, fake := bench(t)

	if err := b.HandGitToHost(t.Context()); err != nil {
		t.Fatalf("HandGitToHost = %v", err)
	}

	settings := map[string]string{
		// NTFS has no executable bit.
		"core.fileMode false": "the executable bit",
		// Checkout conversion would put CRLF into files the container executes.
		"core.autocrlf false": "line endings",
		// Frappe paths exceed Windows' 260-character default.
		"core.longpaths true": "long paths",
	}
	for setting, what := range settings {
		if !fake.Ran(setting) {
			t.Errorf("tamp left %s for the host's git to misread — expected %q", what, setting)
		}
	}
}

// tamp cannot know which repos are there — some arrive through the exec bridge.
func TestGitIsSettledForEveryAppOnTheBench(t *testing.T) {
	b, fake := bench(t)

	if err := b.HandGitToHost(t.Context()); err != nil {
		t.Fatalf("HandGitToHost = %v", err)
	}
	if !fake.Ran(frappe.AppsDir) {
		t.Error("tamp settled git somewhere other than the bench's apps directory")
	}
}
