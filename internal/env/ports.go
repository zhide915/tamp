package env

import (
	"fmt"
	"net"
	"strconv"

	"github.com/zhide915/tamp/internal/exitcode"
)

// FirstDBPort sits just above the usual 3306/33060, clear of any natively
// installed MariaDB.
const FirstDBPort = 33061

// lastDBPort bounds the scan so a wedged range fails instead of looking like
// a hang.
const lastDBPort = FirstDBPort + 99

// AllocateDBPort picks the MariaDB host port — the only port an environment
// publishes, so the only allocation tamp does.
func AllocateDBPort(reg Registry) (int, error) {
	return allocateDBPort(takenDBPorts(reg), portIsFree)
}

// allocateDBPort needs both checks: a stopped environment claims its port
// with nothing listening, and something outside tamp can listen on an
// unclaimed port.
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

// takenDBPorts reads the registry and nothing else — the only source safe
// under the machine lock. Each environment's tamp.toml is written after the
// lock releases, so reading configs could hand out one port twice.
func takenDBPorts(reg Registry) map[int]bool {
	taken := map[int]bool{}
	for _, entry := range reg {
		if entry.DBPort > 0 {
			taken[entry.DBPort] = true
		}
	}
	return taken
}

// portIsFree binds and closes: not race-free, but it is the same check
// compose itself would fail a moment later, and listing listeners portably is
// not possible.
func portIsFree(port int) bool {
	l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return false
	}
	_ = l.Close()
	return true
}
