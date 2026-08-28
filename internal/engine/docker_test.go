package engine_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/exitcode"
)

// Docker cannot be fully exercised in tests; these point the real client at
// a stub daemon over tcp://, the one transport every OS shares.

// stubDaemon serves the endpoints Ping touches.
func stubDaemon(t *testing.T, version string) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/_ping", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Api-Version", "1.51")
		w.Header().Set("Ostype", "linux")
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/version", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Version":"` + version + `","ApiVersion":"1.51"}`))
	})
	// After version negotiation the client requests /v1.51/version.
	mux.HandleFunc("/v1.51/version", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Version":"` + version + `","ApiVersion":"1.51"}`))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return "tcp://" + strings.TrimPrefix(srv.URL, "http://")
}

func TestPingReportsTheDaemonVersionAndTheAddressItUsed(t *testing.T) {
	host := stubDaemon(t, "29.7.2")
	t.Setenv("DOCKER_HOST", host)

	info, err := engine.New().Ping(context.Background())
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if info.Version != "29.7.2" {
		t.Errorf("Version = %q, want %q", info.Version, "29.7.2")
	}
	if info.Address.Host != host {
		t.Errorf("Address.Host = %q, want %q", info.Address.Host, host)
	}
	if info.Address.Source != engine.SourceEnv {
		t.Errorf("Address.Source = %q, want %q", info.Address.Source, engine.SourceEnv)
	}
}

func TestPingIsEngineUnavailableWhenNothingAnswers(t *testing.T) {
	t.Setenv("DOCKER_HOST", "tcp://"+closedAddress(t))

	_, err := engine.New().Ping(context.Background())
	if err == nil {
		t.Fatal("Ping succeeded against a closed port, want an error")
	}
	if got := exitcode.Of(err); got != exitcode.CodeEngineUnavailable {
		t.Errorf("exit code = %d, want %d", got, exitcode.CodeEngineUnavailable)
	}
	if !strings.Contains(err.Error(), "start Docker") {
		t.Errorf("error %q does not tell the user what to do", err)
	}
}

// closedAddress returns a host:port that just stopped listening — the
// closest thing to a guaranteed-refused connection.
func closedAddress(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

// The real engine must satisfy the interface every test above the boundary
// fakes.
var _ engine.Engine = (*engine.Docker)(nil)
