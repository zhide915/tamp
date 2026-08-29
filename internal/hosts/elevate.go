package hosts

import (
	"fmt"
	"os"

	"github.com/zhide915/tamp/internal/exitcode"
)

// Elevating is the one place tamp asks for privileges: the hosts file belongs
// to the system, and nothing else tamp does touches anything of the system's.
// The elevated process runs one command — writing the file — and exits.

// Self is the tamp binary to re-run elevated.
func Self() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot find the tamp binary to re-run with elevated rights: %v", err),
			"run the write yourself: edit the hosts file by hand, or run tamp as an administrator")
	}
	return exe, nil
}

func elevationFailed(err error) error {
	return exitcode.New(exitcode.CodeFailed,
		fmt.Sprintf("the elevated write did not go through: %v", err),
		"approve the prompt, or edit the hosts file by hand, using the lines printed above")
}
