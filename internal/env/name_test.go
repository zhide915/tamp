package env

import (
	"errors"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/exitcode"
)

func TestParseNameAcceptsValidDNSLabels(t *testing.T) {
	for _, s := range []string{"a", "erp15", "next-16", "0", strings.Repeat("x", 32)} {
		if _, err := ParseName(s); err != nil {
			t.Errorf("ParseName(%q) = %v, want no error", s, err)
		}
	}
}

// Every rejection below is a name that would otherwise reach a hostname or a
// Docker resource name and fail there instead.
func TestParseNameRejectsNamesThatCannotBeHostnames(t *testing.T) {
	cases := map[string]string{
		"empty":            "",
		"uppercase":        "ERP15",
		"underscore":       "erp_15",
		"dot":              "erp.15",
		"leading hyphen":   "-erp15",
		"too long":         strings.Repeat("x", 33),
		"space":            "erp 15",
		"trailing newline": "erp15\n",
	}
	for what, s := range cases {
		t.Run(what, func(t *testing.T) {
			_, err := ParseName(s)
			if err == nil {
				t.Fatalf("ParseName(%q) = nil, want an error", s)
			}
			assertFailedWithFix(t, err)
		})
	}
}

// The command words are reserved so that grammar like `tamp snapshot restore`
// stays parseable however many environments exist.
func TestParseNameRejectsCommandWords(t *testing.T) {
	for _, s := range []string{"create", "list", "ls", "rm", "site", "mail", "help"} {
		_, err := ParseName(s)
		if err == nil {
			t.Fatalf("ParseName(%q) = nil, want an error", s)
		}
		if !strings.Contains(err.Error(), "command word") {
			t.Errorf("ParseName(%q) error = %q, want it to say why", s, err)
		}
		assertFailedWithFix(t, err)
	}
}

// tamp's error contract: an invalid name is exit 1, and always names the fix.
func assertFailedWithFix(t *testing.T, err error) {
	t.Helper()
	if got := exitcode.Of(err); got != exitcode.CodeFailed {
		t.Errorf("exit code = %d, want %d", got, exitcode.CodeFailed)
	}
	var e *exitcode.Error
	if !errors.As(err, &e) || e.Fix == "" {
		t.Errorf("error %q carries no fix", err)
	}
}
