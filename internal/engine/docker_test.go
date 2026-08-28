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

// Docker is the one type in tamp that a test cannot fully run, so what can be
// exercised is: point the real client at a stub daemon over tcp:// — the one
// transport every OS shares — and check tamp reports what the daemon said,
// through the address it actually resolved.

// stubDaemon serves the two endpoints Ping touches: the version negotiation
// ping, and /version itself.
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
	// The client requests /v1.51/version once negotiation has happened.
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
	// The address travels with the result because doctor prints it; a Ping
	// that reported only a version would leave the user guessing which of
	// their two engines answered.
	if info.Address.Host != host {
		t.Errorf("Address.Host = %q, want %q", info.Address.Host, host)
	}
	if info.Address.Source != engine.SourceEnv {
		t.Errorf("Address.Source = %q, want %q", info.Address.Source, engine.SourceEnv)
	}
}

// A socket tamp can name but nothing answers on is the everyday "Docker
// Desktop is not running" case, and it must be exit 4 with a fix.
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

// closedAddress returns a host:port that was listening a moment ago and is not
// any more, which is the closest thing to a guaranteed-refused connection.
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

// The interface is tamp's single fake point, so the real engine must satisfy
// it — otherwise every test above the boundary would be faking a shape that
// production does not have.
var _ engine.Engine = (*engine.Docker)(nil)
