package engine

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/exitcode"
)

// This is tamp's one internal test package, and the reason is worth stating:
// the rule below — tamp refuses compose v1 — is real behaviour that must not
// regress, but its only public route is ComposeVersion, which shells out to
// the real docker on the very machine whose compose version is in question. A
// test there would assert whatever that machine happens to have, which is
// nothing. So the rule is tested where it lives instead.
func TestComposeVersionAcceptsV2AndLater(t *testing.T) {
	for _, out := range []string{"v2.39.1\n", "2.39.1", "5.4.0\n", "v2.0.0-rc.3\n"} {
		got, err := parseComposeVersion(out)
		if err != nil {
			t.Errorf("parseComposeVersion(%q): %v", out, err)
			continue
		}
		if strings.HasPrefix(got, "v") || strings.TrimSpace(got) != got {
			t.Errorf("parseComposeVersion(%q) = %q, want it bare and trimmed", out, got)
		}
	}
}

func TestComposeVersionRejectsV1(t *testing.T) {
	_, err := parseComposeVersion("1.29.2\n")
	if err == nil {
		t.Fatal("parseComposeVersion accepted compose v1, want an engine-unavailable error")
	}
	if got := exitcode.Of(err); got != exitcode.CodeEngineUnavailable {
		t.Errorf("exit code = %d, want %d", got, exitcode.CodeEngineUnavailable)
	}
}

// The Docker client restates the endpoint at every layer it wraps, so tamp
// keeps only the innermost reason — the one thing it cannot say for itself.
func TestRootCauseKeepsOnlyTheInnermostReason(t *testing.T) {
	inner := errors.New("connection refused")
	wrapped := fmt.Errorf("error during connect: %w", fmt.Errorf(`Get "http://host/version": %w`, inner))

	if got := rootCause(wrapped); got != inner {
		t.Errorf("rootCause() = %v, want %v", got, inner)
	}
	// An error that wraps nothing is already its own cause.
	if got := rootCause(inner); got != inner {
		t.Errorf("rootCause() = %v, want %v", got, inner)
	}
}
