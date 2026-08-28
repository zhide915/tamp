// Package doctor is tamp's self-diagnosis: a list of checks, each reporting
// pass, warn or fail with the fix, and the exit code that follows from them.
//
// It answers questions rather than performing actions, so it never stops at
// the first problem — a user whose Docker is down still deserves to hear
// whether compose is installed.
package doctor

import (
	"context"
	"errors"
	"fmt"

	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/router"
)

// Status is a check's verdict.
type Status int

const (
	// Pass — tamp can work with what it found.
	Pass Status = iota
	// Warn — tamp can work, but something will bite the user later.
	Warn
	// Fail — tamp cannot work until this is fixed.
	Fail
)

func (s Status) String() string {
	switch s {
	case Pass:
		return "pass"
	case Warn:
		return "warn"
	case Fail:
		return "fail"
	}
	return "unknown"
}

// Check is one diagnosis: what was checked, what tamp found, and — when that
// is not good enough — what to do about it.
type Check struct {
	Name   string
	Status Status
	// Detail is what tamp found, in the user's terms: a version and an
	// address when things are well, the reason when they are not.
	Detail string
	// Fix is the action to take. Empty on a passing check, never on a failing
	// one — a diagnosis without a remedy is just bad news.
	Fix string
	// Code is the exit code a failure here produces, so tamp reports the
	// specific thing that is broken rather than a blanket "failed".
	Code exitcode.Code
}

// Report is the whole diagnosis.
type Report struct {
	Checks []Check
}

// Run performs every check tamp currently knows how to make.
func Run(ctx context.Context, e engine.Engine, r *router.Router) Report {
	return Report{Checks: []Check{
		dockerCheck(ctx, e),
		composeCheck(ctx, e),
		routerCheck(ctx, r),
	}}
}

// OK reports whether tamp can work. Warnings are deliberately not failures:
// they exist so tamp can mention something without breaking a script.
func (r Report) OK() bool {
	for _, c := range r.Checks {
		if c.Status == Fail {
			return false
		}
	}
	return true
}

// ExitCode is 0 when nothing failed, and otherwise the code carried by the
// first failing check — the first thing the user has to fix.
func (r Report) ExitCode() exitcode.Code {
	for _, c := range r.Checks {
		if c.Status == Fail {
			return c.Code
		}
	}
	return exitcode.CodeOK
}

func dockerCheck(ctx context.Context, e engine.Engine) Check {
	info, err := e.Ping(ctx)
	if err != nil {
		return failed("Docker", err)
	}
	return Check{
		Name:   "Docker",
		Status: Pass,
		Detail: fmt.Sprintf("%s at %s", info.Version, info.Address),
	}
}

func composeCheck(ctx context.Context, e engine.Engine) Check {
	version, err := e.ComposeVersion(ctx)
	if err != nil {
		return failed("Docker Compose", err)
	}
	return Check{
		Name:   "Docker Compose",
		Status: Pass,
		Detail: "v" + version,
	}
}

// routerCheck reports the one container every environment depends on.
//
// A router that is not running is a warning rather than a failure, twice over:
// a machine with no environments has no reason to run one, and tamp starts it
// itself on the next create or start. What it is not is silence — with the
// router down nothing on the machine answers to a hostname, and that is the
// answer to the question the user came to doctor with.
func routerCheck(ctx context.Context, r *router.Router) Check {
	status, err := r.Status(ctx)
	if err != nil {
		// An engine tamp cannot reach is already the Docker check's answer,
		// and repeating it here as a second failure tells the user nothing.
		// Anything else — tamp's own state, unreadable — is tamp's fault and
		// deserves to be a failure of its own.
		if exitcode.Of(err) != exitcode.CodeEngineUnavailable {
			return failed("Router", err)
		}
		return Check{
			Name:   "Router",
			Status: Warn,
			Detail: "tamp cannot tell: " + err.Error(),
			Fix:    "fix the checks above, then run 'tamp doctor' again",
		}
	}
	if !status.Running {
		return Check{
			Name:   "Router",
			Status: Warn,
			Detail: "not running — nothing is reachable by hostname",
			Fix:    "run 'tamp start <env>'; tamp brings the router up with it",
		}
	}
	return Check{
		Name:   "Router",
		Status: Pass,
		Detail: "running on " + status.URL("localhost"),
	}
}

// failed turns an engine error into a check. tamp's errors already carry both
// halves a check needs — what happened, and the fix — so this splits rather
// than reinvents them.
func failed(name string, err error) Check {
	c := Check{Name: name, Status: Fail, Code: exitcode.Of(err), Detail: err.Error()}

	var e *exitcode.Error
	if errors.As(err, &e) {
		c.Detail, c.Fix = e.Msg, e.Fix
	}
	return c
}
