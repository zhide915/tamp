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

// Allocation must be decidable from the registry alone: tamp.toml is written
// after the lock releases, so a reader of configs could hand out one port
// twice. The path below deliberately does not exist.
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

func TestAnExhaustedRangeIsReported(t *testing.T) {
	_, err := allocateDBPort(nil, func(int) bool { return false })

	if err == nil {
		t.Fatal("allocateDBPort = nil, want an error")
	}
	assertFailedWithFix(t, err)
}

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
