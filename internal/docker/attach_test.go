package docker

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"testing"
)

// The CLI harness runs the fake, so only a unit test reaches how a real
// docker exit becomes tamp's.
func TestADockerExitIsReadAsTheCommandsCodeEven130(t *testing.T) {
	if code := os.Getenv("TAMP_TEST_EXIT"); code != "" {
		n, _ := strconv.Atoi(code)
		os.Exit(n)
	}
	for _, tc := range []struct {
		exit        int
		code        int
		interrupted bool
	}{
		{exit: 7, code: 7},
		{exit: 130, code: 130},
	} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestADockerExitIsReadAsTheCommandsCodeEven130$")
		cmd.Env = append(os.Environ(), "TAMP_TEST_EXIT="+strconv.Itoa(tc.exit))
		var exit *exec.ExitError
		if err := cmd.Run(); !errors.As(err, &exit) {
			t.Fatalf("the helper exited with %v, want an exit error", err)
		}
		if code, interrupted := exitStatus(exit); code != tc.code || interrupted != tc.interrupted {
			t.Errorf("exit %d read as (%d, %v), want (%d, %v)", tc.exit, code, interrupted, tc.code, tc.interrupted)
		}
	}
}
