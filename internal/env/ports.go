package env

import (
	"fmt"
	"net"
	"strconv"

	"github.com/zhide915/tamp/internal/exitcode"
)

// FirstDBPort is where MariaDB host-port allocation starts. It sits just above
// the usual 3306/33060 so tamp never fights a MariaDB the user installed
// natively.
const FirstDBPort = 33061

// lastDBPort bounds the search. A machine with a hundred tamp environments
// has a different problem, and an unbounded scan of a wedged port range would
// look like a hang.
const lastDBPort = FirstDBPort + 99

// AllocateDBPort picks the host port a new environment's MariaDB publishes.
//
// This is the only host port an environment publishes — every other
// service is reached over the environment's network or through the router — so
// it is also the only allocation tamp has to get right.
func AllocateDBPort(reg Registry) (int, error) {
	return allocateDBPort(takenDBPorts(reg), portIsFree)
}

// allocateDBPort takes the lowest port at or above FirstDBPort that no other
// environment claims and nothing else is listening on.
//
// Both checks are needed and neither replaces the other: a stopped environment
// holds its port while nothing listens on it, and something outside tamp
// entirely can be sitting on a port no environment has claimed.
func allocateDBPort(taken map[int]bool, free func(int) bool) (int, error) {
	for port := FirstDBPort; port <= lastDBPort; port++ {
		if !taken[port] && free(port) {
			return port, nil
		}
	}
	return 0, exitcode.New(exitcode.CodeFailed,
		fmt.Sprintf("no free host port for MariaDB between %d and %d", FirstDBPort, lastDBPort),
		"stop an environment you are not using, or free a port in that range")
}

// takenDBPorts collects the ports the registered environments already claim.
//
// It reads the registry and nothing else, which is what makes allocation safe
// under the machine lock. Deriving "taken" from each environment's
// tamp.toml would not be: a create writes its config after releasing the
// lock, so a second create could hold the lock, see no config for the first
// environment, find the port still unbound — nothing is listening yet — and
// hand out the same one.
func takenDBPorts(reg Registry) map[int]bool {
	taken := map[int]bool{}
	for _, entry := range reg {
		if entry.DBPort > 0 {
			taken[entry.DBPort] = true
		}
	}
	return taken
}

// portIsFree reports whether tamp could publish on a port right now. Binding
// and closing is the only honest test: asking the OS for a list of listeners
// is neither portable nor race-free, and this is not race-free either — it is
// simply the check compose itself would fail on a moment later.
func portIsFree(port int) bool {
	l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return false
	}
	_ = l.Close()
	return true
}
