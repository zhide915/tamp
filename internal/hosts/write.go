package hosts

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/zhide915/tamp/internal/exitcode"
)

// Write replaces the file's content in place. Deliberately not the
// write-a-temp-file-and-rename dance the registry uses: a rename would give
// the hosts file a new inode, and with it the ownership and ACLs the system
// put on the original.
//
// The new content is written over the old and the tail cut afterwards, never
// truncated first: a file tamp promised only ever to add a block to must not
// be empty at any instant, not even the one a power cut lands in. A failed
// write still leaves it part-old and part-new, so the previous content is
// held in memory and put back.
func Write(path, content string) error {
	previous, err := Read(path)
	if err != nil {
		return err
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return writeError(path, err)
	}
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		return restore(path, previous, err)
	}
	if err := file.Truncate(int64(len(content))); err != nil {
		_ = file.Close()
		return restore(path, previous, err)
	}
	if err := file.Close(); err != nil {
		return restore(path, previous, err)
	}
	return nil
}

// restore puts back what the half-finished write overwrote. A failure here is the one
// case where the file is left damaged, so it says exactly that instead of
// hiding behind the original error.
func restore(path, previous string, cause error) error {
	if werr := os.WriteFile(path, []byte(previous), 0o644); werr != nil {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("writing %s failed (%v) and tamp could not put back what was there (%v)", path, cause, werr),
			"restore the file from a backup — tamp only ever writes between its own two markers")
	}
	return writeError(path, cause)
}

// Denied reports whether err is the system refusing for want of privileges —
// the one failure a sync answers by elevating rather than giving up.
func Denied(err error) bool { return errors.Is(err, fs.ErrPermission) }

func writeError(path string, err error) error {
	return &writeFailure{
		reported: exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot write %s: %v", path, err),
			"the hosts file belongs to the system — tamp needs elevated rights to change it"),
		cause: err,
	}
}

// writeFailure keeps the operating system's error beside tamp's, so Denied
// can ask what actually went wrong without reading the message.
type writeFailure struct {
	reported *exitcode.Error
	cause    error
}

func (w *writeFailure) Error() string { return w.reported.Error() }
func (w *writeFailure) Unwrap() error { return w.cause }
