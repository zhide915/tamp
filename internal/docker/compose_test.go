package docker

import (
	"errors"
	"testing"

	"github.com/zhide915/tamp/internal/exitcode"
)

// The CLI harness runs the fake, so only a unit test reaches how a real
// compose failure is read.
func TestOnlyAProjectThatPublishesPortsReadsItsFailureAsATakenPort(t *testing.T) {
	failed := &cliError{
		err:    exitcode.New(exitcode.CodeFailed, "docker compose failed: exit status 1", "fix what the docker output above names"),
		output: "Error response from daemon: listen tcp 0.0.0.0:80: bind: address already in use\n",
	}

	if err := upError(failed, true); !errors.Is(err, ErrPortTaken) {
		t.Errorf("a publishing project's failure = %v, want ErrPortTaken", err)
	}
	err := upError(failed, false)
	if errors.Is(err, ErrPortTaken) {
		t.Errorf("a project that publishes nothing reads its failure as a taken port: %v", err)
	}
	if exitcode.Of(err) != exitcode.CodeFailed || err.Error() != failed.Error() {
		t.Errorf("a project that publishes nothing = %v, want the docker failure as is", err)
	}
}
