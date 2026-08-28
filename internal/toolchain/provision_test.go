package toolchain_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/engine/enginetest"
	"github.com/zhide915/tamp/internal/toolchain"
)

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

// A probe that ran and said "absent" means download; one that could not run
// must fail rather than fill a toolchain into a container that is not there.
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
			// Fails on purpose: the step after uname is a real download.
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
	// Both runtimes must resolve into the shared volume, or what one
	// environment installs the next cannot see.
	for _, want := range []string{"UV_PYTHON_INSTALL_DIR", "NVM_DIR", toolchain.Dir} {
		if !strings.Contains(script, want) {
			t.Errorf("%s does not set %s:\n%s", toolchain.EnvScript, want, script)
		}
	}
}

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
	// Versions travel as arguments so the command shows what was asked for.
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
