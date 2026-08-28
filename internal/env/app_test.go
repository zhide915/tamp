package env_test

import (
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/env"
	"github.com/zhide915/tamp/internal/exitcode"
)

// tamp never invents a branch: what is fetched is the branch the user wrote,
// or the repo's default.

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
		// The branch colon is the one after the repository, so a branch may
		// itself contain a slash.
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
	// bench accepts owner/repo; tamp refuses it — glued onto the frappe org
	// URL it would name a repository that does not exist.
	for _, spec := range []string{"", ":version-15", "frappe/erpnext", "frappe/erpnext:version-15"} {
		if _, err := env.ParseApp(spec); err == nil {
			t.Errorf("ParseApp(%q) = nil, want an error", spec)
		} else if exitcode.Of(err) != exitcode.CodeFailed {
			t.Errorf("ParseApp(%q) exit code = %d, want %d", spec, exitcode.Of(err), exitcode.CodeFailed)
		}
	}
}

// An ssh source cannot go through the credential bridge, and the https form
// can; the error must hand the user that form.
func TestAnSSHAppSpecIsRefusedWithItsHTTPSForm(t *testing.T) {
	cases := map[string]string{
		"git@github.com:frappe/hrms.git":         "https://github.com/frappe/hrms.git",
		"git@github.com:frappe/hrms.git:develop": "https://github.com/frappe/hrms.git",
		"ssh://git@github.com/frappe/hrms.git":   "https://github.com/frappe/hrms.git",
		// The ssh port must not survive into the https suggestion.
		"ssh://git@git.example.com:2222/team/app.git": "https://git.example.com/team/app.git",
	}
	for spec, https := range cases {
		t.Run(spec, func(t *testing.T) {
			_, err := env.ParseApp(spec)
			if err == nil {
				t.Fatalf("ParseApp(%q) = nil, want a refusal", spec)
			}
			if exitcode.Of(err) != exitcode.CodeFailed {
				t.Errorf("exit code = %d, want %d", exitcode.Of(err), exitcode.CodeFailed)
			}
			if !strings.Contains(err.Error(), https) {
				t.Errorf("the error does not suggest %s:\n%v", https, err)
			}
		})
	}
}

// tamp never records a secret: a token pasted into the URL must be refused,
// and the refusal itself must not repeat it.
func TestATokenInAnAppURLIsRefusedWithoutEchoingTheToken(t *testing.T) {
	const token = "x-token-4Xyz"
	for _, spec := range []string{
		"https://" + token + "@github.com/myorg/private.git",
		"https://" + token + "@github.com/myorg/private.git:version-15",
		// Unparseable (the %zz escape): the refusal must fail closed, and
		// still without echoing what came before the @.
		"https://" + token + "%zz@github.com/myorg/private.git",
		// An ssh source with userinfo takes the ssh refusal; that echo must
		// be redacted too.
		"ssh://user:" + token + "@github.com/myorg/private.git",
	} {
		t.Run(spec, func(t *testing.T) {
			_, err := env.ParseApp(spec)
			if err == nil {
				t.Fatalf("ParseApp(%q) = nil, want a refusal", spec)
			}
			if exitcode.Of(err) != exitcode.CodeFailed {
				t.Errorf("exit code = %d, want %d", exitcode.Of(err), exitcode.CodeFailed)
			}
			if strings.Contains(err.Error(), token) {
				t.Errorf("the refusal repeats the token:\n%v", err)
			}
		})
	}
}

// A duplicate would only fail at the second fetch, minutes in.
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

// "erpnext:" carries an empty branch; the colon must not reach the comparison
// against bench directory names.
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

// The flag defaults to "": that is no apps, not one nameless app.
func TestParseAppsAcceptsNothing(t *testing.T) {
	apps, err := env.ParseApps("")
	if err != nil {
		t.Fatalf("ParseApps(\"\") = %v", err)
	}
	if len(apps) != 0 {
		t.Errorf("ParseApps(\"\") = %v, want none", apps)
	}
}
