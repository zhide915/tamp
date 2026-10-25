package gitcred_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/gitcred"
)

// A cancelled fill (Ctrl+C at the sign-in prompt) must surface as the
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

// A URL subsection may hold a space, and the key must keep it.
func TestHTTPPathSettingsKeepAURLThatHoldsASpace(t *testing.T) {
	global := filepath.Join(t.TempDir(), "gitconfig")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	if err := os.WriteFile(global, []byte("[credential \"https://host/my org\"]\n\tuseHttpPath = yes\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	settings, err := gitcred.HTTPPathSettings(context.Background())

	want := gitcred.Setting{Key: "credential.https://host/my org.usehttppath", Value: "true"}
	if err != nil || len(settings) != 1 || settings[0] != want {
		t.Errorf("HTTPPathSettings = %v, %v, want [%v]", settings, err, want)
	}
}

// Git percent-encodes a request path's reserved characters, so a repository
// named with one must still be probed, not misread or refused.
func TestPathScopedProbesARepositoryWhosePathHoldsReservedCharacters(t *testing.T) {
	global := filepath.Join(t.TempDir(), "gitconfig")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
	t.Setenv("GIT_ASKPASS", "true")
	if err := os.WriteFile(global, []byte("[credential]\n\tuseHttpPath = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"org/a=b", "org/a+b@c", "org/a b", "org/a[b]#c", "org/a%b", "org/ünï"} {
		got, err := gitcred.PathScoped(context.Background(), "https", "h.example", path)
		if err != nil || !got {
			t.Errorf("PathScoped(%q) = %t, %v, want true", path, got, err)
		}
	}
}

// The fix for a git that cannot read its config is in git's own words.
func TestReadingTheHTTPPathSettingsQuotesGitsOwnFailure(t *testing.T) {
	global := filepath.Join(t.TempDir(), "gitconfig")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	if err := os.WriteFile(global, []byte("[credential\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := gitcred.HTTPPathSettings(context.Background())

	if err == nil || !strings.Contains(err.Error(), "bad config") {
		t.Errorf("HTTPPathSettings on a broken config = %v, want git's own \"bad config\" line", err)
	}
}
