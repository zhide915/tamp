//go:build !windows

package env

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// tryLock takes a non-blocking exclusive flock. A second attempt from within
// the same process still conflicts — flock is held per open file description —
// which is what lets the lock be tested without spawning a second tamp.
func tryLock(f *os.File) (bool, error) {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return false, nil
	}
	return err == nil, err
}

func unlock(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
