// Package gitcred speaks the git credential protocol with the host's git,
// the credential bridge's host half (ADR 0002). A conduit, never a store:
// fill delegates to the user's own helper (which may prompt), approve and
// reject report how the credential fared, and nothing is written anywhere.
// Tests keep this boundary real, steered at a canned helper.
package gitcred

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// ErrNoGit means the host has no git to ask. Only a private fetch ever
// needs one.
var ErrNoGit = errors.New("no git on this machine")

// ErrNoCredential means host git ran but produced no usable credential: no
// helper configured, an empty answer, or a declined prompt.
var ErrNoCredential = errors.New("the host's git credential system provided no credential")

// Credential is one filled host credential, held in memory for the run.
// attrs is everything fill answered, echoed back verbatim on approve and
// reject so the helper updates the exact credential it issued.
type Credential struct {
	Protocol string
	Host     string
	Username string
	Password string

	attrs map[string]string
}

// Fill asks the host's git credential system for the given URL parts. The
// helper's own interactive prompts are allowed; the caller narrates the wait.
func Fill(ctx context.Context, protocol, host, path string, stderr io.Writer) (Credential, error) {
	git, err := exec.LookPath("git")
	if err != nil {
		return Credential{}, ErrNoGit
	}

	in := fmt.Sprintf("protocol=%s\nhost=%s\npath=%s\n", protocol, host, path)
	cmd := exec.CommandContext(ctx, git, "credential", "fill")
	cmd.Stdin = strings.NewReader(in)
	cmd.Stderr = stderr
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		// A cancelled fill (Ctrl+C at the sign-in prompt) is not "no
		// credential exists"; telling the user to sign in would mislead.
		if ctx.Err() != nil {
			return Credential{}, ctx.Err()
		}
		return Credential{}, ErrNoCredential
	}

	attrs := map[string]string{}
	for line := range strings.SplitSeq(strings.TrimSpace(out.String()), "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			attrs[key] = value
		}
	}
	if attrs["username"] == "" || attrs["password"] == "" {
		return Credential{}, ErrNoCredential
	}
	return Credential{
		Protocol: attrs["protocol"],
		Host:     attrs["host"],
		Username: attrs["username"],
		Password: attrs["password"],
		attrs:    attrs,
	}, nil
}

// PathScoped asks git's own fill, a probe standing in for the user's helpers,
// whether git sends the URL's path (credential.useHttpPath).
func PathScoped(ctx context.Context, protocol, host, path string) (bool, error) {
	git, err := exec.LookPath("git")
	if err != nil {
		return false, ErrNoGit
	}
	in := fmt.Sprintf("protocol=%s\nhost=%s\npath=%s\n", protocol, host, path)
	// The probe answers a dummy credential, so git never prompts. At the URL
	// itself it outranks the user's helpers, even on a best-match-only git.
	at := "credential." + ConfigURL(protocol+"://"+host+"/"+path) + ".helper="
	cmd := exec.CommandContext(ctx, git, "-c", "credential.helper=", "-c", at, "-c", at+probeHelper, "credential", "fill")
	cmd.Stdin = strings.NewReader(in)
	var seen bytes.Buffer
	cmd.Stderr = &seen
	if err := cmd.Run(); err != nil {
		return false, fmt.Errorf("git credential fill, probing how %s scopes credentials: %w: %s", host, err, gitComplaint(seen.String()))
	}
	return strings.Contains("\n"+seen.String(), "\npath="), nil
}

// gitComplaint is git's own fatal or error line, without the probe's echo.
func gitComplaint(stderr string) string {
	for line := range strings.SplitSeq(stderr, "\n") {
		if strings.HasPrefix(line, "fatal: ") || strings.HasPrefix(line, "error: ") {
			return strings.TrimSpace(line)
		}
	}
	return "git gave no reason"
}

// ConfigURL spells url as a credential.<url> key must: git percent-encodes
// every path byte but '/' and the unreserved ones before matching a key.
func ConfigURL(url string) string {
	scheme, rest, _ := strings.Cut(url, "://")
	host, path, found := strings.Cut(rest, "/")
	if !found {
		return url
	}
	var b strings.Builder
	for i := 0; i < len(path); i++ {
		if c := path[i]; c == '/' || unreserved(c) {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return scheme + "://" + host + "/" + b.String()
}

// unreserved is RFC 3986's set, the bytes git leaves raw in a request path.
func unreserved(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || strings.IndexByte("-._~", c) >= 0
}

// probeHelper echoes what git asks it to stderr.
const probeHelper = `!f() { cat >&2; echo username=probe; echo password=probe; }; f`

// Setting is one entry of the host's git config.
type Setting struct{ Key, Value string }

// HTTPPathSettings is every credential.useHttpPath entry the host's git
// holds, global or per URL pattern, in config order.
func HTTPPathSettings(ctx context.Context) ([]Setting, error) {
	git, err := exec.LookPath("git")
	if err != nil {
		return nil, ErrNoGit
	}
	// --bool, not --type=bool: the latter needs git 2.18.
	out, err := exec.CommandContext(ctx, git, "config", "--bool", "--get-regexp", `^credential(\..*)?\.usehttppath$`).Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return nil, nil // no entry at all
	}
	if errors.As(err, &exit) {
		return nil, fmt.Errorf("git config, reading credential.useHttpPath: %w: %s", err, gitComplaint(string(exit.Stderr)))
	}
	if err != nil {
		return nil, fmt.Errorf("git config, reading credential.useHttpPath: %w", err)
	}
	var settings []Setting
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		// The last space: a URL subsection may hold one, a --bool value never.
		i := strings.LastIndex(line, " ")
		if i < 0 {
			continue
		}
		settings = append(settings, Setting{line[:i], line[i+1:]})
	}
	return settings, nil
}

// Approve tells the host's helper the credential worked, so it caches it.
func Approve(ctx context.Context, c Credential, stderr io.Writer) error {
	return complete(ctx, "approve", c, stderr)
}

// Reject tells the host's helper the credential was refused, so it drops it
// instead of serving the same stale secret on the next run.
func Reject(ctx context.Context, c Credential, stderr io.Writer) error {
	return complete(ctx, "reject", c, stderr)
}

func complete(ctx context.Context, verb string, c Credential, stderr io.Writer) error {
	git, err := exec.LookPath("git")
	if err != nil {
		return ErrNoGit
	}
	var in strings.Builder
	for key, value := range c.attrs {
		fmt.Fprintf(&in, "%s=%s\n", key, value)
	}
	cmd := exec.CommandContext(ctx, git, "credential", verb)
	cmd.Stdin = strings.NewReader(in.String())
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git credential %s: %w", verb, err)
	}
	return nil
}
