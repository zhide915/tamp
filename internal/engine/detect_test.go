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

// detector builds a Detector over a temp config dir; present names the
// candidate endpoints that exist on the imaginary machine.
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

// writeContext mirrors the docker CLI's layout: context metadata lives under
// a directory named for the SHA-256 of the context name.
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
	// doctor prints the context name.
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

// "default" is not a stored context and has no metadata directory; treating
// it as missing would break every machine that never ran `docker context use`.
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
	// The message must say where detection looked.
	for _, want := range []string{"DOCKER_HOST", "unix:///var/run/docker.sock"} {
		if !strings.Contains(e.Msg, want) {
			t.Errorf("message %q does not mention %q", e.Msg, want)
		}
	}
}

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

// Every other test injects candidates; only this pins production's probe
// order.
func TestNewDetectorProbesTheDocumentedSocketsInOrder(t *testing.T) {
	d := engine.NewDetector(func(string) (string, bool) { return "", false })

	want := []string{`npipe:////./pipe/docker_engine`, "unix:///var/run/docker.sock"}
	if !slices.Equal(d.Candidates, want) {
		t.Errorf("Candidates = %v, want %v", d.Candidates, want)
	}
}

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
		// Remote endpoints cannot be probed, only connected to.
		{"tcp://10.0.0.5:2375", false},
		{"ssh://build.example", false},
	} {
		if got := exists(tc.host); got != tc.want {
			t.Errorf("Exists(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}

// An empty ConfigDir must not become a relative path that picks up a stray
// config.json from the working directory.
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
