package router

import (
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/zhide915/tamp/internal/exitcode"
)

// dialWait bounds the check below. It is a connection to loopback, which
// either completes or is refused immediately; the timeout is only there so
// that a port being silently dropped by a firewall cannot hang a create.
const dialWait = 300 * time.Millisecond

// choosePort settles which host port the router publishes on.
//
// Port 80 is the product, not a preference: a URL with a port in it is a URL
// the user has to remember. tamp takes it when it can, and falls back rather
// than leaving the machine with no router at all.
func choosePort(free func(int) bool) (int, error) {
	for _, port := range []int{DefaultPort, FallbackPort} {
		if free(port) {
			return port, nil
		}
	}
	return 0, exitcode.New(exitcode.CodeFailed,
		fmt.Sprintf("ports %d and %d are both taken, so tamp cannot publish the router",
			DefaultPort, FallbackPort),
		"stop whatever is listening on them, then try again")
}

// portIsFree reports whether the router could be published on a host port.
//
// It connects rather than binds, which is the whole subtlety. Binding is the
// obvious test and the wrong one here: the process that would actually take
// the port is the Docker daemon, which runs as root while tamp does not, and
// on Unix an unprivileged bind below 1024 is refused whether or not anything
// holds the port. A bind test could therefore never tell tamp that port 80
// was busy — the fallback would be dead everywhere but Windows.
//
// A refused connection means nothing is serving there, which is the question
// worth asking. It is not race-free, and nothing portable is: it is the same
// check Docker itself will fail a moment later if the answer changes.
func portIsFree(port int) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), dialWait)
	if err != nil {
		return true
	}
	_ = conn.Close()
	return false
}
