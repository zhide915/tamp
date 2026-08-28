package env

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/zhide915/tamp/internal/exitcode"
)

// LockFile is the single coarse lock taken before mutating machine-global
// state.
const LockFile = "lock"

// lockWait absorbs two terminals racing by a second or two; anything longer
// would look like a hang.
const lockWait = 3 * time.Second

// lockPoll is the retry gap — there is no portable way to wait on an advisory
// lock.
const lockPoll = 50 * time.Millisecond

// Lock is tamp's held machine-global lock. One coarse lock on purpose: the
// registry, Caddyfile and hosts block are all read-modify-write cycles, and
// two concurrent commands would silently lose the first write.
type Lock struct {
	file *os.File
}

// AcquireLock takes ~/.tamp/lock. It is an OS advisory lock on an open file,
// not the file's existence, so a killed tamp leaves no stale lock behind.
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

// Release gives the lock back early — a command with more work to do must not
// keep the machine waiting until exit. Callers defer it.
func (l *Lock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := unlock(l.file)
	_ = l.file.Close()
	l.file = nil
	return err
}
