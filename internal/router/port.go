package router

import (
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/zhide915/tamp/internal/exitcode"
)

// dialWait only guards against a firewall silently dropping the probe;
// loopback otherwise answers immediately.
const dialWait = 300 * time.Millisecond

// choosePort prefers 80 — a port-free URL — and falls back rather than leave
// the machine with no router.
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

// portIsFree probes by connecting, not binding: the eventual bind is done by
// the Docker daemon as root, and on Unix an unprivileged bind below 1024 fails
// whether or not the port is busy — a bind test could never call 80 taken.
// A refused connection means nothing is serving there. Racy, like any port
// probe; Docker fails the same way a moment later if the answer changes.
func portIsFree(port int) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), dialWait)
	if err != nil {
		return true
	}
	_ = conn.Close()
	return false
}
