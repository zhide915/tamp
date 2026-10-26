package env

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/exitcode"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := ConfigPath(t.TempDir())
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// What tamp writes it must read back unchanged, or an upgrade orphans
// environments.
func TestConfigRoundTrips(t *testing.T) {
	_, tc, err := ParseFrappeVersion("version-16")
	if err != nil {
		t.Fatal(err)
	}
	want := NewConfig("next16", Version16, nil, tc, 33062)
	path := ConfigPath(t.TempDir())

	if err := want.Save(path); err != nil {
		t.Fatal(err)
	}
	got, warnings, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig = %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("tamp's own file warned about %v", warnings)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip changed the config:\n got %+v\nwant %+v", got, want)
	}
}

func TestUnknownSchemaTellsTheUserToUpgrade(t *testing.T) {
	path := writeConfig(t, "schema = 2\nname = \"erp15\"\n")

	_, _, err := LoadConfig(path)
	if err == nil {
		t.Fatal("LoadConfig = nil, want an error")
	}
	if !strings.Contains(err.Error(), "upgrade tamp") {
		t.Errorf("error = %q, want it to say to upgrade tamp", err)
	}
}

// A config from a newer patch release still has to load.
func TestUnknownKeysWarnRatherThanFail(t *testing.T) {
	path := writeConfig(t, validConfig+"\nsparkles = true\n")

	cfg, warnings, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig = %v", err)
	}
	if cfg.Name != "erp15" {
		t.Errorf("Name = %q, want erp15", cfg.Name)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "sparkles") {
		t.Errorf("warnings = %v, want one naming the unknown key", warnings)
	}
}

func TestInvalidConfigsAreRejectedWithAFix(t *testing.T) {
	cases := map[string]string{
		`profile = "prod"`:           strings.Replace(validConfig, `profile = "dev"`, `profile = "prod"`, 1),
		"unsupported frappe":         strings.Replace(validConfig, `version = "version-15"`, `version = "version-14"`, 1),
		"engine that is not docker":  strings.Replace(validConfig, `kind = "docker"`, `kind = "podman"`, 1),
		"name that cannot be a host": strings.Replace(validConfig, `name = "erp15"`, `name = "ERP 15"`, 1),
		"no db port":                 strings.Replace(validConfig, "db = 33061", "db = 0", 1),
		"router mode tamp does not have": strings.Replace(validConfig,
			"[router]\nmode = \"auto\"", "[router]\nmode = \"ports\"", 1),
	}
	for what, body := range cases {
		t.Run(what, func(t *testing.T) {
			_, _, err := LoadConfig(writeConfig(t, body))
			if err == nil {
				t.Fatal("LoadConfig = nil, want an error")
			}
			assertFailedWithFix(t, err)
		})
	}
}

// Exit 3, the same as an unknown name: "no such environment" either way.
func TestMissingConfigIsNotFound(t *testing.T) {
	_, _, err := LoadConfig(ConfigPath(t.TempDir()))

	if got := exitcode.Of(err); got != exitcode.CodeNotFound {
		t.Errorf("exit code = %d, want %d (%v)", got, exitcode.CodeNotFound, err)
	}
}

func TestUnparseableConfigIsAFailureNotACrash(t *testing.T) {
	_, _, err := LoadConfig(writeConfig(t, "schema = = 1"))

	if got := exitcode.Of(err); got != exitcode.CodeFailed {
		t.Errorf("exit code = %d, want %d (%v)", got, exitcode.CodeFailed, err)
	}
}

const validConfig = `schema = 1
name = "erp15"
profile = "dev"

[frappe]
version = "version-15"

[toolchain]
python = "3.11"
node = "18"
mariadb = "10.11"

[engine]
kind = "docker"

[sync]
mode = "auto"

[router]
mode = "auto"

[ports]
db = 33061
`
