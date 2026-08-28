package env_test

import (
	"testing"

	"github.com/zhide915/tamp/internal/env"
)

// The registry is the machine's claims ledger, and these tests hold it to the
// claim rules through its own interface — no engine, no CLI: a name is unique,
// a hostname is unique machine-wide whether it is a site or a mail UI, and a
// port is allocated exactly once.

// Reclaim is the rm→init cycle's registry half: the port comes back with the
// environment so a database client's saved connection still works — unless
// another environment took it meanwhile, in which case sharing it would leave
// only one of the two able to start.
func TestReclaimKeepsThePortUnlessAnotherEnvironmentTookIt(t *testing.T) {
	home := t.TempDir()
	recorded, err := env.Claim(home, "demo", "/work/demo", "ab12cd")
	if err != nil {
		t.Fatal(err)
	}
	if err := env.Release(home, "demo"); err != nil {
		t.Fatal(err)
	}

	back, err := env.Reclaim(home, "demo", "/work/demo", "ab12cd", recorded)
	if err != nil {
		t.Fatal(err)
	}
	if back != recorded {
		t.Errorf("reclaim moved the port from %d to %d with nothing else on it", recorded, back)
	}

	taken, err := env.Reclaim(home, "other", "/work/other", "ef34gh", recorded)
	if err != nil {
		t.Fatal(err)
	}
	if taken == recorded {
		t.Errorf("two environments now publish port %d", recorded)
	}
}
