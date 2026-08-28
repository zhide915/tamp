package engine

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/exitcode"
)

// Internal on purpose: the only public route to the v1 rejection is
// ComposeVersion, which shells out to whatever docker this machine has.
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

func TestRootCauseKeepsOnlyTheInnermostReason(t *testing.T) {
	inner := errors.New("connection refused")
	wrapped := fmt.Errorf("error during connect: %w", fmt.Errorf(`Get "http://host/version": %w`, inner))

	if got := rootCause(wrapped); got != inner {
		t.Errorf("rootCause() = %v, want %v", got, inner)
	}
	if got := rootCause(inner); got != inner {
		t.Errorf("rootCause() = %v, want %v", got, inner)
	}
}
