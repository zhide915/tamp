package env

import (
	"os"
	"strings"
	"testing"
)

func TestEmptyRegistryOnAMachineThatHasNeverRunCreate(t *testing.T) {
	reg, err := LoadRegistry(t.TempDir())

	if err != nil {
		t.Fatalf("LoadRegistry = %v", err)
	}
	if len(reg) != 0 {
		t.Errorf("registry = %v, want empty", reg)
	}
}

func TestRegistryRoundTripsThroughAnUpdate(t *testing.T) {
	home := t.TempDir()

	err := UpdateRegistry(home, func(reg Registry) error {
		reg["erp15"] = Entry{Path: "/work/erp15", Hash: "ab12cd", Sites: []string{"shop.localhost"}}
		return nil
	})
	if err != nil {
		t.Fatalf("UpdateRegistry = %v", err)
	}

	reg, err := LoadRegistry(home)
	if err != nil {
		t.Fatalf("LoadRegistry = %v", err)
	}
	got, ok := reg["erp15"]
	if !ok {
		t.Fatalf("registry = %v, want an erp15 entry", reg)
	}
	if got.Path != "/work/erp15" || got.Hash != "ab12cd" || len(got.Sites) != 1 {
		t.Errorf("entry = %+v, want the one that was written", got)
	}
}

// A change that fails must leave the registry exactly as it was — half a
// create is not half a registration.
func TestAFailedUpdateWritesNothing(t *testing.T) {
	home := t.TempDir()
	if err := UpdateRegistry(home, func(reg Registry) error {
		reg["erp15"] = Entry{Path: "/work/erp15"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	want := os.ErrPermission
	if err := UpdateRegistry(home, func(reg Registry) error {
		reg["next16"] = Entry{Path: "/work/next16"}
		return want
	}); err != want {
		t.Fatalf("UpdateRegistry = %v, want the change's own error", err)
	}

	reg, err := LoadRegistry(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, found := reg["next16"]; found {
		t.Error("the failed update was written anyway")
	}
	if _, found := reg["erp15"]; !found {
		t.Error("the failed update lost the environment that was already there")
	}
}

func TestNamesAreSortedSoListOutputIsStable(t *testing.T) {
	reg := Registry{"next16": {}, "erp15": {}, "aaa": {}}

	if got, want := strings.Join(reg.Names(), ","), "aaa,erp15,next16"; got != want {
		t.Errorf("Names() = %q, want %q", got, want)
	}
}

func TestCorruptRegistryIsReportedWithARepairHint(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(RegistryPath(home), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadRegistry(home)
	if err == nil {
		t.Fatal("LoadRegistry = nil, want an error")
	}
	assertFailedWithFix(t, err)
}
