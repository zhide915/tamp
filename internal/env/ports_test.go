package env

import (
	"path/filepath"
	"testing"
)

func allFree(int) bool { return true }

func TestFirstEnvironmentGetsTheFirstPort(t *testing.T) {
	port, err := allocateDBPort(nil, allFree)

	if err != nil {
		t.Fatalf("allocateDBPort = %v", err)
	}
	if port != FirstDBPort {
		t.Errorf("port = %d, want %d", port, FirstDBPort)
	}
}

// A stopped environment still owns its port — nothing is listening on it, and
// it must not be handed to a second environment that would then collide the
// moment both are started.
func TestAPortClaimedByAnotherEnvironmentIsSkippedEvenWhenNothingListens(t *testing.T) {
	taken := map[int]bool{FirstDBPort: true, FirstDBPort + 1: true}

	port, err := allocateDBPort(taken, allFree)
	if err != nil {
		t.Fatalf("allocateDBPort = %v", err)
	}
	if port != FirstDBPort+2 {
		t.Errorf("port = %d, want %d", port, FirstDBPort+2)
	}
}

// Allocation must be decidable from the registry alone.
//
// The registry entry is written under the machine lock; the environment's
// tamp.toml is written after the lock is released. If "taken" were derived
// from those files, a second create could hold the lock during that window,
// see no config for the first environment, find the port unbound — nothing is
// listening yet — and hand out the same one. The path below deliberately does
// not exist, so a reader of tamp.toml would allocate 33061 twice.
func TestAllocationDoesNotDependOnAnyFileOnDisk(t *testing.T) {
	reg := Registry{"erp15": {Path: filepath.Join(t.TempDir(), "never-written"), DBPort: FirstDBPort}}

	port, err := AllocateDBPort(reg)
	if err != nil {
		t.Fatalf("AllocateDBPort = %v", err)
	}
	if port == FirstDBPort {
		t.Errorf("port = %d, which the registry says erp15 already holds", port)
	}
}

// Something outside tamp can be sitting on a port no environment claims.
func TestAPortInUseByAnythingElseIsSkipped(t *testing.T) {
	free := func(p int) bool { return p != FirstDBPort }

	port, err := allocateDBPort(nil, free)
	if err != nil {
		t.Fatalf("allocateDBPort = %v", err)
	}
	if port != FirstDBPort+1 {
		t.Errorf("port = %d, want %d", port, FirstDBPort+1)
	}
}

// Exhausting the range is a failure with a fix, not a hang or a zero port.
func TestAnExhaustedRangeIsReported(t *testing.T) {
	_, err := allocateDBPort(nil, func(int) bool { return false })

	if err == nil {
		t.Fatal("allocateDBPort = nil, want an error")
	}
	assertFailedWithFix(t, err)
}

// The two rejections have to compose, or a busy machine hands out a port that
// is already claimed.
func TestClaimedAndBusyPortsAreBothSkipped(t *testing.T) {
	taken := map[int]bool{FirstDBPort: true}
	free := func(p int) bool { return p != FirstDBPort+1 }

	port, err := allocateDBPort(taken, free)
	if err != nil {
		t.Fatalf("allocateDBPort = %v", err)
	}
	if port != FirstDBPort+2 {
		t.Errorf("port = %d, want %d", port, FirstDBPort+2)
	}
}
