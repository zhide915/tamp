package env_test

import (
	"slices"
	"testing"

	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/exitcode"
)

// The registry is the machine's claims ledger, and these tests hold it to the
// claim rules through its own interface — no engine, no CLI: a name is unique,
// a hostname is unique machine-wide whether it is a site or a mail UI, and a
// port is allocated exactly once.

func TestClaimRefusesANameTheMachineAlreadyGaveOut(t *testing.T) {
	home := t.TempDir()
	if _, err := env.Claim(home, "demo", "/work/demo", "ab12cd"); err != nil {
		t.Fatal(err)
	}

	_, err := env.Claim(home, "demo", "/work/elsewhere", "ef34gh")

	if exitcode.Of(err) != exitcode.CodeFailed {
		t.Fatalf("second claim of %q = %v, want a failure", "demo", err)
	}
}

func TestClaimsAllocateDistinctPorts(t *testing.T) {
	home := t.TempDir()
	a, err := env.Claim(home, "one", "/work/one", "aaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	b, err := env.Claim(home, "two", "/work/two", "bbbbbb")
	if err != nil {
		t.Fatal(err)
	}

	if a == b {
		t.Errorf("both environments were given port %d", a)
	}
}

// An environment's name decides its mail hostname, so a name whose mail UI
// address is already someone's site would put one address in the Caddyfile
// twice — a configuration the router refuses to load.
func TestClaimRefusesANameWhoseMailHostnameIsTaken(t *testing.T) {
	home := t.TempDir()
	if _, err := env.Claim(home, "demo", "/work/demo", "ab12cd"); err != nil {
		t.Fatal(err)
	}
	if err := env.ClaimHost(home, "demo", "mail.other.localhost"); err != nil {
		t.Fatal(err)
	}

	_, err := env.Claim(home, "other", "/work/other", "ef34gh")

	if exitcode.Of(err) != exitcode.CodeFailed {
		t.Fatalf("claim of %q = %v, want a failure — its mail hostname is a site already", "other", err)
	}
}

func TestAHostnameIsAMachineWideClaim(t *testing.T) {
	home := t.TempDir()
	for _, name := range []string{"demo", "other"} {
		if _, err := env.Claim(home, env.Name(name), "/work/"+name, name+"00"); err != nil {
			t.Fatal(err)
		}
	}
	if err := env.ClaimHost(home, "demo", "shop.localhost"); err != nil {
		t.Fatal(err)
	}

	if err := env.ClaimHost(home, "other", "shop.localhost"); exitcode.Of(err) != exitcode.CodeFailed {
		t.Errorf("a second environment claimed shop.localhost: %v", err)
	}
	// A mail UI is as much a claim as a site: one address, one block.
	if err := env.ClaimHost(home, "demo", "mail.other.localhost"); exitcode.Of(err) != exitcode.CodeFailed {
		t.Errorf("an environment claimed another's mail hostname: %v", err)
	}
}

func TestAReleasedHostnameCanBeClaimedAgain(t *testing.T) {
	home := t.TempDir()
	if _, err := env.Claim(home, "demo", "/work/demo", "ab12cd"); err != nil {
		t.Fatal(err)
	}
	if err := env.ClaimHost(home, "demo", "shop.localhost"); err != nil {
		t.Fatal(err)
	}

	if err := env.ReleaseHost(home, "demo", "shop.localhost"); err != nil {
		t.Fatal(err)
	}
	if err := env.ClaimHost(home, "demo", "shop.localhost"); err != nil {
		t.Errorf("the released hostname is still held: %v", err)
	}
}

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

// A bench can hold a hostname the machine has already given to something else
// — tamp did not create every site on it. Recording must keep it out of the
// routes and say so, not route one address twice.
func TestRecordSitesKeepsOutAHostnameSomethingElseHolds(t *testing.T) {
	home := t.TempDir()
	for _, name := range []string{"demo", "other"} {
		if _, err := env.Claim(home, env.Name(name), "/work/"+name, name+"00"); err != nil {
			t.Fatal(err)
		}
	}
	if err := env.ClaimHost(home, "other", "shop.localhost"); err != nil {
		t.Fatal(err)
	}

	skipped, err := env.RecordSites(home, "demo", []string{"shop.localhost", "books.localhost"})
	if err != nil {
		t.Fatal(err)
	}

	if len(skipped) != 1 || skipped[0].Host != "shop.localhost" || skipped[0].Owner != "other" {
		t.Errorf("skipped = %+v, want shop.localhost owned by other", skipped)
	}
	reg, err := env.LoadRegistry(home)
	if err != nil {
		t.Fatal(err)
	}
	if got := reg["demo"].Sites; !slices.Equal(got, []string{"books.localhost"}) {
		t.Errorf("demo's recorded sites = %v, want [books.localhost]", got)
	}
}
