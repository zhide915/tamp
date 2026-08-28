package exitcode_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/zhide915/tamp/internal/exitcode"
)

func TestCodesMatchTheDocumentedContract(t *testing.T) {
	// These numbers are a public contract: scripts and agents branch on them,
	// so they may never be renumbered.
	for _, tc := range []struct {
		code exitcode.Code
		want int
	}{
		{exitcode.CodeOK, 0},
		{exitcode.CodeFailed, 1},
		{exitcode.CodeUsage, 2},
		{exitcode.CodeNotFound, 3},
		{exitcode.CodeEngineUnavailable, 4},
		{exitcode.CodeConfirmationRequired, 5},
	} {
		if int(tc.code) != tc.want {
			t.Errorf("code = %d, want %d", tc.code, tc.want)
		}
	}
}

func TestErrorMessageIncludesTheFix(t *testing.T) {
	err := exitcode.New(exitcode.CodeNotFound, "environment 'x' not found", "see 'tamp list'")
	if got, want := err.Error(), "environment 'x' not found — see 'tamp list'"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestErrorMessageOmitsTheSeparatorWithoutAFix(t *testing.T) {
	err := exitcode.New(exitcode.CodeFailed, "something broke", "")
	if got, want := err.Error(), "something broke"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestUsageShorthandCarriesTheUsageCode(t *testing.T) {
	err := exitcode.Usage(`unknown command "nope"`, "run 'tamp --help'")
	if err.Code != exitcode.CodeUsage {
		t.Errorf("Code = %d, want %d", err.Code, exitcode.CodeUsage)
	}
}

func TestOfClassifiesErrors(t *testing.T) {
	notFound := exitcode.New(exitcode.CodeNotFound, "gone", "look elsewhere")

	for name, tc := range map[string]struct {
		err  error
		want exitcode.Code
	}{
		"nil is success":              {nil, exitcode.CodeOK},
		"plain error is failure":      {errors.New("boom"), exitcode.CodeFailed},
		"exit error keeps its code":   {notFound, exitcode.CodeNotFound},
		"wrapping preserves the code": {fmt.Errorf("while starting: %w", notFound), exitcode.CodeNotFound},
	} {
		if got := exitcode.Of(tc.err); got != tc.want {
			t.Errorf("%s: Of() = %d, want %d", name, got, tc.want)
		}
	}
}
