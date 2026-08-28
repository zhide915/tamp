package engine_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/exitcode"
)

// Detection is the whole reason tamp can claim to find Docker and tell the
// truth about what it found, so these tests pin the documented order —
// DOCKER_HOST, then the active docker context, then the known sockets — and
// the source tamp reports for each, since a user debugging a broken setup
// needs to know which of the three answered.

// detector builds a Detector over a temp docker config dir. present names the
// candidate endpoints that exist on this imaginary machine.
func detector(t *testing.T, env map[string]string, present ...string) engine.Detector {
	t.Helper()
	exists := make(map[string]bool, len(present))
	for _, p := range present {
		exists[p] = true
	}
	return engine.Detector{
		ConfigDir:  t.TempDir(),
		LookupEnv:  func(k string) (string, bool) { v, ok := env[k]; return v, ok },
		Candidates: []string{"npipe:////./pipe/docker_engine", "unix:///var/run/docker.sock"},
		Exists:     func(host string) bool { return exists[host] },
	}
}

// writeContext lays out a docker context the way the docker CLI does: the
// metadata lives under a directory named for the SHA-256 of the context name.
func writeContext(t *testing.T, configDir, name, host string) {
	t.Helper()
	sum := sha256.Sum256([]byte(name))
	dir := filepath.Join(configDir, "contexts", "meta", hex.EncodeToString(sum[:]))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := map[string]any{
		"Name":      name,
		"Endpoints": map[string]any{"docker": map[string]any{"Host": host}},
	}
	write(t, filepath.Join(dir, "meta.json"), meta)
}

func writeConfig(t *testing.T, configDir, currentContext string) {
	t.Helper()
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(configDir, "config.json"), map[string]any{"currentContext": currentContext})
}

func write(t *testing.T, path string, v any) {
	t.Helper()
	blob, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, blob, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDockerHostWinsOverEverythingElse(t *testing.T) {
	d := detector(t, map[string]string{"DOCKER_HOST": "tcp://10.0.0.5:2375"}, "unix:///var/run/docker.sock")
	writeConfig(t, d.ConfigDir, "desktop-linux")
	writeContext(t, d.ConfigDir, "desktop-linux", "npipe:////./pipe/dockerDesktopLinuxEngine")

	addr, err := d.Detect()
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if addr.Host != "tcp://10.0.0.5:2375" {
		t.Errorf("Host = %q, want the DOCKER_HOST value", addr.Host)
	}
	if addr.Source != engine.SourceEnv {
		t.Errorf("Source = %q, want %q", addr.Source, engine.SourceEnv)
	}
}

// An exported-but-empty DOCKER_HOST is how a shell leaves a variable that was
// cleared by assigning ""; it must not shadow a working context.
func TestEmptyDockerHostIsIgnored(t *testing.T) {
	d := detector(t, map[string]string{"DOCKER_HOST": ""}, "unix:///var/run/docker.sock")

	addr, err := d.Detect()
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if addr.Source != engine.SourceProbe {
		t.Errorf("Source = %q, want %q", addr.Source, engine.SourceProbe)
	}
}

func TestActiveContextIsUsedWhenDockerHostIsUnset(t *testing.T) {
	d := detector(t, nil, "unix:///var/run/docker.sock")
	writeConfig(t, d.ConfigDir, "desktop-linux")
	writeContext(t, d.ConfigDir, "desktop-linux", "npipe:////./pipe/dockerDesktopLinuxEngine")

	addr, err := d.Detect()
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if addr.Host != "npipe:////./pipe/dockerDesktopLinuxEngine" {
		t.Errorf("Host = %q, want the context endpoint", addr.Host)
	}
	if addr.Source != engine.SourceContext {
		t.Errorf("Source = %q, want %q", addr.Source, engine.SourceContext)
	}
	// doctor prints the name, because "which context?" is the first question a
	// user with two engines asks.
	if addr.Context != "desktop-linux" {
		t.Errorf("Context = %q, want %q", addr.Context, "desktop-linux")
	}
}

func TestDockerContextEnvOverridesTheConfiguredContext(t *testing.T) {
	d := detector(t, map[string]string{"DOCKER_CONTEXT": "remote"})
	writeConfig(t, d.ConfigDir, "desktop-linux")
	writeContext(t, d.ConfigDir, "desktop-linux", "npipe:////./pipe/dockerDesktopLinuxEngine")
	writeContext(t, d.ConfigDir, "remote", "ssh://build.example")

	addr, err := d.Detect()
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if addr.Host != "ssh://build.example" {
		t.Errorf("Host = %q, want the DOCKER_CONTEXT endpoint", addr.Host)
	}
	if addr.Context != "remote" {
		t.Errorf("Context = %q, want %q", addr.Context, "remote")
	}
}

// "default" is docker's own name for "no context — use the platform socket",
// and it has no metadata directory. Treating it as a missing context would
// break every machine that has never run `docker context use`.
func TestDefaultContextFallsThroughToProbing(t *testing.T) {
	d := detector(t, nil, "unix:///var/run/docker.sock")
	writeConfig(t, d.ConfigDir, "default")

	addr, err := d.Detect()
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if addr.Host != "unix:///var/run/docker.sock" {
		t.Errorf("Host = %q, want the probed socket", addr.Host)
	}
	if addr.Source != engine.SourceProbe {
		t.Errorf("Source = %q, want %q", addr.Source, engine.SourceProbe)
	}
}

// The named pipe is probed before the unix socket, so a machine that somehow
// answers on both is reported as the Docker Desktop engine it is.
func TestProbingHonoursCandidateOrder(t *testing.T) {
	d := detector(t, nil, "unix:///var/run/docker.sock", "npipe:////./pipe/docker_engine")

	addr, err := d.Detect()
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if addr.Host != "npipe:////./pipe/docker_engine" {
		t.Errorf("Host = %q, want the first candidate", addr.Host)
	}
}

// Nothing found is the exit-4 case the whole engine boundary exists to report.
func TestNoEngineAnywhereIsAnEngineUnavailableError(t *testing.T) {
	d := detector(t, nil)

	_, err := d.Detect()
	if err == nil {
		t.Fatal("Detect succeeded, want an error")
	}
	if got := exitcode.Of(err); got != exitcode.CodeEngineUnavailable {
		t.Errorf("exit code = %d, want %d", got, exitcode.CodeEngineUnavailable)
	}
	var e *exitcode.Error
	if !errors.As(err, &e) {
		t.Fatalf("error %v is not an *exitcode.Error", err)
	}
	if e.Fix == "" {
		t.Error("error carries no fix; tamp errors always name the fix")
	}
	// The message must name where tamp looked — otherwise "no engine found"
	// is unactionable on a machine with an unusual socket path.
	for _, want := range []string{"DOCKER_HOST", "unix:///var/run/docker.sock"} {
		if !strings.Contains(e.Msg, want) {
			t.Errorf("message %q does not mention %q", e.Msg, want)
		}
	}
}

// A context that config.json points at but that no longer exists is a real
// misconfiguration: say so rather than silently probing something else.
func TestUnknownContextIsReportedRatherThanIgnored(t *testing.T) {
	d := detector(t, nil, "unix:///var/run/docker.sock")
	writeConfig(t, d.ConfigDir, "gone")

	_, err := d.Detect()
	if err == nil {
		t.Fatal("Detect succeeded, want an error naming the missing context")
	}
	if !strings.Contains(err.Error(), "gone") {
		t.Errorf("error %q does not name the missing context", err)
	}
	if got := exitcode.Of(err); got != exitcode.CodeEngineUnavailable {
		t.Errorf("exit code = %d, want %d", got, exitcode.CodeEngineUnavailable)
	}
}

// NewDetector is what production uses, so its two environment overrides need
// pinning too: without them a user with a non-standard DOCKER_CONFIG would be
// diagnosed against the wrong contexts.
func TestNewDetectorHonoursDockerConfigEnv(t *testing.T) {
	dir := t.TempDir()
	d := engine.NewDetector(func(k string) (string, bool) {
		if k == "DOCKER_CONFIG" {
			return dir, true
		}
		return "", false
	})

	if d.ConfigDir != dir {
		t.Errorf("ConfigDir = %q, want %q", d.ConfigDir, dir)
	}
	if len(d.Candidates) == 0 {
		t.Error("Candidates is empty; there would be nothing to probe")
	}
}

// Every other test injects its own candidate list, so without this one nothing
// holds production to the documented probe order — the Docker Desktop pipe
// first, then the unix socket.
func TestNewDetectorProbesTheDocumentedSocketsInOrder(t *testing.T) {
	d := engine.NewDetector(func(string) (string, bool) { return "", false })

	want := []string{`npipe:////./pipe/docker_engine`, "unix:///var/run/docker.sock"}
	if !slices.Equal(d.Candidates, want) {
		t.Errorf("Candidates = %v, want %v", d.Candidates, want)
	}
}

// Exists is the one part of detection that differs per OS, and every test
// above replaces it. These pin the real thing: tamp must strip the scheme,
// and must answer for an endpoint that is absent as surely as for one that is
// there. A stat is deliberately all it does — "the socket exists" is a
// different fact from "Docker answers", and tamp reports them separately.
func TestRealExistsStripsTheSchemeAndStatsThePath(t *testing.T) {
	exists := engine.NewDetector(os.LookupEnv).Exists

	present := filepath.Join(t.TempDir(), "docker.sock")
	if err := os.WriteFile(present, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		host string
		want bool
	}{
		{"unix://" + filepath.ToSlash(present), true},
		{"unix:///no/such/socket/anywhere", false},
		{`npipe:////./pipe/tamp_no_such_pipe`, false},
		// tamp only knows how to look for a socket on this machine; a remote
		// endpoint cannot be probed, only connected to.
		{"tcp://10.0.0.5:2375", false},
		{"ssh://build.example", false},
	} {
		if got := exists(tc.host); got != tc.want {
			t.Errorf("Exists(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}

// With no home directory and no DOCKER_CONFIG there is nowhere to look for
// docker's config. Joining onto an empty dir would make the path relative and
// read whatever config.json happened to be in the working directory — tamp
// would then diagnose against a file it has no business reading.
func TestEmptyConfigDirDoesNotReadTheWorkingDirectory(t *testing.T) {
	d := detector(t, nil, "unix:///var/run/docker.sock")
	d.ConfigDir = ""
	t.Chdir(t.TempDir())
	write(t, "config.json", map[string]any{"currentContext": "planted"})

	addr, err := d.Detect()
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if addr.Source != engine.SourceProbe {
		t.Errorf("Source = %q, want %q — tamp read a stray config.json", addr.Source, engine.SourceProbe)
	}
}
