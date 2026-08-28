package gitcred_test

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"

	"github.com/zhide915/tamp/internal/gitcred"
)

// A cancelled fill — Ctrl+C at the sign-in prompt — must surface as the
// cancellation, not as "no credential exists, sign in and retry".
func TestFillReportsACancelledSignInAsCancellationNotAsNoCredential(t *testing.T) {
	// The CLI sandbox's isolation: host git must not reach the developer's
	// own credential configuration, even for a doomed run.
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
	t.Setenv("GIT_ASKPASS", "true")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := gitcred.Fill(ctx, "https", "github.com", "myorg/private", io.Discard)

	if !errors.Is(err, context.Canceled) {
		t.Errorf("Fill under a cancelled context = %v, want context.Canceled", err)
	}
	if errors.Is(err, gitcred.ErrNoCredential) {
		t.Error("a cancelled fill was reported as a missing credential")
	}
}
