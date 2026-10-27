package docker

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/exitcode"
)

// The CLI harness runs the fake, so only a unit test reaches the real Ping.
func TestAPingThroughADeadDockerHostNamesDockerHostInItsFix(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCKER_HOST", "tcp://"+addr)

	err = New().Ping(context.Background())

	var e *exitcode.Error
	if !errors.As(err, &e) || e.Code != exitcode.CodeDockerUnavailable || !strings.Contains(e.Fix, "correct DOCKER_HOST, or unset it") {
		t.Errorf("Ping = %#v, want exit 4 with a fix that names DOCKER_HOST", err)
	}
}
