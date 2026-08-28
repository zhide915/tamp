package env

import (
	"strings"
	"testing"
	"time"
)

func TestSecondLockHolderIsToldAnotherCommandIsRunning(t *testing.T) {
	home := t.TempDir()

	first, err := AcquireLock(home)
	if err != nil {
		t.Fatalf("first AcquireLock = %v", err)
	}
	defer func() { _ = first.Release() }()

	start := time.Now()
	second, err := AcquireLock(home)
	if err == nil {
		_ = second.Release()
		t.Fatal("second AcquireLock succeeded while the first was held")
	}
	if !strings.Contains(err.Error(), "another tamp command is running") {
		t.Errorf("error = %q, want it to name the cause", err)
	}
	// A command losing a one-millisecond race should queue behind the winner,
	// not fail instantly.
	if waited := time.Since(start); waited < lockWait {
		t.Errorf("gave up after %v, want at least %v", waited, lockWait)
	}
}
