package env

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/zhide915/tamp/internal/exitcode"
)

// LockFile is the single coarse lock every command takes before mutating
// machine-global state.
const LockFile = "lock"

// lockWait is how long tamp tries before giving up. Contention on a
// one-developer machine means two terminals raced by a second or two, so a
// short wait absorbs the ordinary case; anything longer would just be a
// command that appears to hang.
const lockWait = 3 * time.Second

// lockPoll is the gap between attempts. The OS gives tamp no way to wait on
// an advisory lock portably, so it polls.
const lockPoll = 50 * time.Millisecond

// Lock is tamp's held machine-global lock.
//
// It is one coarse lock rather than one per file on purpose: the registry, the
// assembled Caddyfile and the hosts block are all read-modify-write cycles,
// and two tamp commands running at once would otherwise silently lose the
// first write.
type Lock struct {
	file *os.File
}

// AcquireLock takes ~/.tamp/lock, waiting briefly for a command that already
// holds it. The lock is an OS advisory lock on an open file rather than a
// file's mere existence, so a tamp that is killed mid-command releases it —
// there is no stale lock for the next run to clear.
func AcquireLock(home string) (*Lock, error) {
	path := filepath.Join(home, LockFile)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot open %s: %v", path, err),
			"check the permissions on your ~/.tamp directory")
	}

	deadline := time.Now().Add(lockWait)
	for {
		ok, err := tryLock(f)
		if err != nil {
			// Closing on the way out of a failure tamp is already reporting:
			// there is no second error worth telling the user about here.
			_ = f.Close()
			return nil, exitcode.New(exitcode.CodeFailed,
				fmt.Sprintf("cannot lock %s: %v", path, err),
				"check the permissions on your ~/.tamp directory")
		}
		if ok {
			return &Lock{file: f}, nil
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, exitcode.New(exitcode.CodeFailed,
				"another tamp command is running",
				"wait for it to finish, then try again")
		}
		time.Sleep(lockPoll)
	}
}

// Release gives the lock back. Callers defer it; the OS would release it at
// exit anyway, but a command that goes on to do more work after mutating the
// registry must not keep the whole machine waiting on it.
func (l *Lock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := unlock(l.file)
	// Whether the lock was given back is the answer callers want; whether the
	// descriptor also closed cleanly is not something they could act on.
	_ = l.file.Close()
	l.file = nil
	return err
}
