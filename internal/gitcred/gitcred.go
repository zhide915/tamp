// Package gitcred speaks the git credential protocol with the host's git —
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

// ErrNoGit means the host has no git to ask — only a private fetch ever
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
		// A cancelled fill — Ctrl+C at the sign-in prompt — is not "no
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
