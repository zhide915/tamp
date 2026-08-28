package toolchain_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/engine/enginetest"
	"github.com/zhide915/tamp/internal/toolchain"
)

// The shared volume is what makes the second environment on a machine cheap:
// a toolchain that is already there is not fetched again. The fake answers
// tamp's "is uv here?" probe with yes, which is what a machine that has
// already created one environment looks like.
func TestAToolchainThatIsAlreadyThereIsNotDownloadedAgain(t *testing.T) {
	fake := enginetest.Running()

	err := toolchain.Provision(t.Context(), fake, toolchain.Request{
		Container: "tamp-demo-ab12cd-frappe-1",
		Python:    "3.11",
		Node:      "18",
	})
	if err != nil {
		t.Fatalf("Provision = %v", err)
	}

	for _, path := range fake.Written() {
		if strings.Contains(path, "/uv/") {
			t.Errorf("tamp installed %s over a uv that was already there", path)
		}
	}
	if fake.Ran("uname") {
		t.Error("tamp worked out the container architecture with nothing to download")
	}
}

// The probe that decides whether to download has two "no"s to tell apart. A
// command that ran and said the binary is absent means fetch one; a command
// tamp could not run at all means something is wrong, and tamp must not
// answer it by downloading a toolchain into a container that is not there.
func TestABrokenProbeIsAFailureRatherThanAnEmptyToolchain(t *testing.T) {
	provision := func(fake *enginetest.Fake) error {
		return toolchain.Provision(t.Context(), fake, toolchain.Request{
			Container: "tamp-demo-ab12cd-frappe-1",
			Python:    "3.11",
			Node:      "18",
		})
	}

	t.Run("a probe that answers no sends tamp looking for a download", func(t *testing.T) {
		fake := enginetest.Running()
		fake.ExecFails = map[string]error{
			"test -x": &engine.ExitError{Container: "tamp-demo-ab12cd-frappe-1", Status: 1},
			// Stopped at the next step on purpose: what comes after it is a
			// real download, which no unit test should reach.
			"uname": &engine.ExitError{Container: "tamp-demo-ab12cd-frappe-1", Status: 1},
		}

		if err := provision(fake); err == nil {
			t.Fatal("Provision = nil, want the architecture probe's failure")
		}
		if !fake.Ran("uname") {
			t.Error("tamp treated an absent uv as an installed one")
		}
	})

	t.Run("a probe tamp could not run stops the whole thing", func(t *testing.T) {
		fake := enginetest.Running()
		broken := errors.New("Docker is not answering")
		fake.ExecFails = map[string]error{"test -x": broken}

		err := provision(fake)
		if !errors.Is(err, broken) {
			t.Fatalf("Provision = %v, want the engine's own failure", err)
		}
		if fake.Ran("uname") {
			t.Error("tamp carried on provisioning through an engine it could not reach")
		}
	})
}

// Everything tamp and the bench run has to find the shared Python and Node,
// and the env script is the one place that says where they are.
func TestProvisioningWritesTheEnvironmentScriptEveryCommandSources(t *testing.T) {
	fake := enginetest.Running()

	err := toolchain.Provision(t.Context(), fake, toolchain.Request{
		Container: "tamp-demo-ab12cd-frappe-1",
		Python:    "3.11",
		Node:      "18",
	})
	if err != nil {
		t.Fatalf("Provision = %v", err)
	}

	script, ok := fake.Wrote(toolchain.EnvScript)
	if !ok {
		t.Fatalf("tamp wrote %v, not %s", fake.Written(), toolchain.EnvScript)
	}
	// Both language runtimes have to be redirected into the shared volume, or
	// what one environment installs the next one cannot see.
	for _, want := range []string{"UV_PYTHON_INSTALL_DIR", "NVM_DIR", toolchain.Dir} {
		if !strings.Contains(script, want) {
			t.Errorf("%s does not set %s:\n%s", toolchain.EnvScript, want, script)
		}
	}
}

// The versions come from tamp's matrix, and both have to actually be asked
// for — a bench built against the wrong Python is the failure tamp exists to
// prevent.
func TestProvisioningInstallsTheRequestedPythonAndNode(t *testing.T) {
	fake := enginetest.Running()

	err := toolchain.Provision(t.Context(), fake, toolchain.Request{
		Container: "tamp-demo-ab12cd-frappe-1",
		Python:    "3.11",
		Node:      "18",
	})
	if err != nil {
		t.Fatalf("Provision = %v", err)
	}

	provisioning := lastExec(t, fake)
	if !strings.Contains(provisioning.Line(), "uv python install") {
		t.Errorf("tamp never installed a Python:\n%s", provisioning.Line())
	}
	if !strings.Contains(provisioning.Line(), "nvm install") {
		t.Errorf("tamp never installed a Node:\n%s", provisioning.Line())
	}
	// The versions travel as arguments rather than inside the script, so that
	// what tamp asked for is visible in the command it ran.
	if got := provisioning.Cmd[len(provisioning.Cmd)-2:]; got[0] != "3.11" || got[1] != "18" {
		t.Errorf("tamp provisioned %v, want python 3.11 and node 18", got)
	}
}

func lastExec(t *testing.T, fake *enginetest.Fake) enginetest.Exec {
	t.Helper()
	if len(fake.Execs) == 0 {
		t.Fatal("tamp ran nothing in the container")
	}
	return fake.Execs[len(fake.Execs)-1]
}
