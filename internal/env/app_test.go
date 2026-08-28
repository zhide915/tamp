package env_test

import (
	"testing"

	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/exitcode"
)

// An app spec says three things at most — which app, from where, on which
// branch — and tamp never invents the third. What is tested here is that the
// branch tamp fetches is the one the user wrote, or nothing at all.

func TestAnAppSpecSaysWhichBranch(t *testing.T) {
	cases := map[string]env.App{
		"erpnext": {
			Name: "erpnext", Source: "https://github.com/frappe/erpnext",
		},
		"erpnext:version-15": {
			Name: "erpnext", Source: "https://github.com/frappe/erpnext", Branch: "version-15",
		},
		"https://github.com/frappe/hrms": {
			Name: "hrms", Source: "https://github.com/frappe/hrms",
		},
		"https://github.com/frappe/hrms.git:version-15": {
			Name: "hrms", Source: "https://github.com/frappe/hrms.git", Branch: "version-15",
		},
		"https://git.example.com:8443/team/shop": {
			Name: "shop", Source: "https://git.example.com:8443/team/shop",
		},
		"https://git.example.com:8443/team/shop:main": {
			Name: "shop", Source: "https://git.example.com:8443/team/shop", Branch: "main",
		},
		"git@github.com:frappe/hrms.git": {
			Name: "hrms", Source: "git@github.com:frappe/hrms.git",
		},
		"git@github.com:frappe/hrms.git:develop": {
			Name: "hrms", Source: "git@github.com:frappe/hrms.git", Branch: "develop",
		},
		// A branch is allowed to look like a path; the colon that separates it
		// is the one after the repository, not the one inside the URL.
		"https://github.com/frappe/hrms:feature/x": {
			Name: "hrms", Source: "https://github.com/frappe/hrms", Branch: "feature/x",
		},
	}

	for spec, want := range cases {
		t.Run(spec, func(t *testing.T) {
			got, err := env.ParseApp(spec)
			if err != nil {
				t.Fatalf("ParseApp(%q) = %v", spec, err)
			}
			if got != want {
				t.Errorf("ParseApp(%q) = %+v, want %+v", spec, got, want)
			}
		})
	}
}

func TestAnAppSpecTampRefuses(t *testing.T) {
	// "frappe/erpnext" is a spelling bench accepts and tamp does not: glued
	// onto the frappe organisation's URL it names a repository that does not
	// exist, and the clone would only fail after the environment is built.
	for _, spec := range []string{"", ":version-15", "frappe/erpnext", "frappe/erpnext:version-15"} {
		if _, err := env.ParseApp(spec); err == nil {
			t.Errorf("ParseApp(%q) = nil, want an error", spec)
		} else if exitcode.Of(err) != exitcode.CodeFailed {
			t.Errorf("ParseApp(%q) exit code = %d, want %d", spec, exitcode.Of(err), exitcode.CodeFailed)
		}
	}
}

// The second fetch of a duplicated app would fail against the app the first
// one put on the bench — minutes in, taking the whole create with it.
func TestParseAppsRefusesADuplicate(t *testing.T) {
	for _, spec := range []string{
		"erpnext:version-15,erpnext",
		"erpnext,https://github.com/frappe/erpnext",
	} {
		if _, err := env.ParseApps(spec); err == nil {
			t.Errorf("ParseApps(%q) = nil, want an error", spec)
		}
	}
}

// "erpnext:" carries an empty branch, so the branch rejection has no reason to
// fire — but the colon must not reach the comparison against bench directory
// names, where it can never match.
func TestParseInstallAppsDropsATrailingColon(t *testing.T) {
	names, err := env.ParseInstallApps("erpnext:")
	if err != nil {
		t.Fatalf("ParseInstallApps(\"erpnext:\") = %v", err)
	}
	if len(names) != 1 || names[0] != "erpnext" {
		t.Errorf("names = %v, want [erpnext]", names)
	}
}

func TestParseAppsSplitsTheFlagAndKeepsItsOrder(t *testing.T) {
	apps, err := env.ParseApps("erpnext:version-15, hrms:version-15")
	if err != nil {
		t.Fatalf("ParseApps = %v", err)
	}
	names := env.AppNames(apps)
	if len(names) != 2 || names[0] != "erpnext" || names[1] != "hrms" {
		t.Fatalf("ParseApps names = %v, want [erpnext hrms]", names)
	}
	if apps[1].Branch != "version-15" {
		t.Errorf("second app branch = %q, want version-15", apps[1].Branch)
	}
}

// An empty --apps is no apps, not one app with no name: the flag defaults to
// the empty string and every create would otherwise fail on it.
func TestParseAppsAcceptsNothing(t *testing.T) {
	apps, err := env.ParseApps("")
	if err != nil {
		t.Fatalf("ParseApps(\"\") = %v", err)
	}
	if len(apps) != 0 {
		t.Errorf("ParseApps(\"\") = %v, want none", apps)
	}
}
