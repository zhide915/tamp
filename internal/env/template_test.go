package env

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zhide915/tamp/internal/exitcode"
)

// The cache's whole decision is one pure function, so its rules are pinned
// here against a clock the test holds rather than the one the machine has.

func stored(age time.Duration, now time.Time) templateManifest {
	return templateManifest{
		Schema:  templateSchema,
		Frappe:  Version15,
		Image:   BenchImage,
		Python:  "3.11",
		Node:    "18",
		Created: now.Add(-age),
	}
}

func wantV15() templateManifest {
	return templateManifest{
		Schema: templateSchema,
		Frappe: Version15,
		Image:  BenchImage,
		Python: "3.11",
		Node:   "18",
	}
}

func TestATemplateIsUsableUntilItsTTLRunsOut(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	ttl := 14 * 24 * time.Hour

	cases := map[string]struct {
		age  time.Duration
		want templateVerdict
	}{
		"minutes old":     {age: time.Minute, want: verdictHit},
		"a day short":     {age: 13 * 24 * time.Hour, want: verdictHit},
		"exactly the TTL": {age: ttl, want: verdictExpired},
		"a day past":      {age: 15 * 24 * time.Hour, want: verdictExpired},
	}
	for name, tc := range cases {
		if got := stored(tc.age, now).usable(wantV15(), ttl, now); got != tc.want {
			t.Errorf("a template %s is %q, want %q", name, got, tc.want)
		}
	}
}

// A template from a bench image tamp no longer pins is not this bench.
func TestATemplateFromAnotherBenchImageIsNotUsed(t *testing.T) {
	now := time.Now()
	old := stored(time.Minute, now)
	old.Image = "frappe/bench:v1.0.0"

	if got := old.usable(wantV15(), time.Hour, now); got != verdictStale {
		t.Errorf("a template from another bench image is %q, want %q", got, verdictStale)
	}
}

// A manifest tamp cannot read describes a bench it cannot vouch for.
func TestATemplateFromAnotherManifestSchemaIsNotUsed(t *testing.T) {
	now := time.Now()
	old := stored(time.Minute, now)
	old.Schema = templateSchema + 1

	if got := old.usable(wantV15(), time.Hour, now); got != verdictStale {
		t.Errorf("a template with an unknown schema is %q, want %q", got, verdictStale)
	}
}

// The key is the Frappe version and nothing else: everything else about a
// bench is added after the template is taken.
func TestEverySupportedVersionKeysItsOwnTemplate(t *testing.T) {
	seen := map[string]bool{}
	for _, v := range supportedVersions {
		key := templateKey(v)
		if key == "" {
			t.Errorf("%s keys an empty template name", v)
		}
		if seen[key] {
			t.Errorf("%s shares a template with another version", v)
		}
		seen[key] = true
	}
}

// The virtualenv inside a template was built against the versions it records.
func TestAMovedToolchainMarksATemplateAsDrifted(t *testing.T) {
	want := wantV15()

	older := stored(time.Minute, time.Now())
	older.Python = "3.10"
	if !older.drifted(want) {
		t.Error("a template built on another Python does not read as drifted")
	}

	sameAge := stored(time.Minute, time.Now())
	if sameAge.drifted(want) {
		t.Error("a template matching the matrix reads as drifted")
	}
}

// --- the machine's settings ----------------------------------------------

func TestAMissingGlobalConfigIsTheDefaults(t *testing.T) {
	cfg, err := LoadGlobalConfig(t.TempDir())
	if err != nil {
		t.Fatalf("LoadGlobalConfig with no file = %v", err)
	}
	if got, want := cfg.TemplateTTL(), time.Duration(DefaultTemplateTTLDays)*24*time.Hour; got != want {
		t.Errorf("the default template TTL is %v, want %v", got, want)
	}
}

func TestTheTemplateTTLComesFromTheGlobalConfig(t *testing.T) {
	cases := map[string]time.Duration{
		"[cache]\ntemplate_ttl_days = 30\n": 30 * 24 * time.Hour,
		"[cache]\ntemplate_ttl_days = 0\n":  0,
		// Time never runs backwards; a negative setting reads as "expire
		// everything", which is what it was reaching for.
		"[cache]\ntemplate_ttl_days = -3\n": 0,
	}
	for body, want := range cases {
		home := t.TempDir()
		writeGlobalConfig(t, home, body)
		cfg, err := LoadGlobalConfig(home)
		if err != nil {
			t.Fatalf("LoadGlobalConfig(%q) = %v", body, err)
		}
		if got := cfg.TemplateTTL(); got != want {
			t.Errorf("%q gives a TTL of %v, want %v", body, got, want)
		}
	}
}

// A silently ignored setting is worse than a refusal: the user would believe
// it took effect.
func TestAnUnknownGlobalSettingIsRefused(t *testing.T) {
	home := t.TempDir()
	writeGlobalConfig(t, home, "[cache]\ntemplate_ttl_dayz = 30\n")

	_, err := LoadGlobalConfig(home)

	if err == nil {
		t.Fatal("LoadGlobalConfig accepted a setting tamp does not have")
	}
	if got := exitcode.Of(err); got != exitcode.CodeFailed {
		t.Errorf("exit code = %d, want %d", got, exitcode.CodeFailed)
	}
}

func writeGlobalConfig(t *testing.T, home, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, GlobalConfigFile), []byte(body), 0o644); err != nil {
		t.Fatalf("cannot write the global config: %v", err)
	}
}
