package env

import (
	"strings"
	"testing"
	"time"
)

// The second command waits briefly, then says plainly what is wrong
// rather than corrupting the registry the first one is rewriting.
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
	// It has to actually wait: a command that loses a one-millisecond race
	// should end up queued behind the winner, not rejected.
	if waited := time.Since(start); waited < lockWait {
		t.Errorf("gave up after %v, want at least %v", waited, lockWait)
	}
}

func TestLockIsAvailableAgainAfterRelease(t *testing.T) {
	home := t.TempDir()

	first, err := AcquireLock(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("Release = %v", err)
	}

	second, err := AcquireLock(home)
	if err != nil {
		t.Fatalf("AcquireLock after Release = %v", err)
	}
	if err := second.Release(); err != nil {
		t.Errorf("Release = %v", err)
	}
}
