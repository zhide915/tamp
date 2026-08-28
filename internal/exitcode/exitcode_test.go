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

func TestErrorMessageOmitsTheSeparatorWithoutAFix(t *testing.T) {
	err := exitcode.New(exitcode.CodeFailed, "something broke", "")
	if got, want := err.Error(), "something broke"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
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

// A reported failure still exits with its code, but tamp prints nothing more:
// the command that returned it has already explained itself.
func TestReportedCarriesTheCodeAndIsSilent(t *testing.T) {
	err := exitcode.Reported(exitcode.CodeEngineUnavailable)

	if got := exitcode.Of(err); got != exitcode.CodeEngineUnavailable {
		t.Errorf("Of() = %d, want %d", got, exitcode.CodeEngineUnavailable)
	}
	if !exitcode.Silent(err) {
		t.Error("Silent() = false, want true")
	}
}

// Silence is a deliberate mark, not the absence of a message. An ordinary
// Error that happens to carry no text must still be printed, or a future
// caller's failure would vanish without a word.
func TestOnlyReportedErrorsAreSilent(t *testing.T) {
	for _, err := range []error{
		exitcode.New(exitcode.CodeFailed, "", ""),
		exitcode.New(exitcode.CodeFailed, "something broke", "retry"),
		errors.New("boom"),
		nil,
	} {
		if exitcode.Silent(err) {
			t.Errorf("Silent(%v) = true, want false", err)
		}
	}
}
