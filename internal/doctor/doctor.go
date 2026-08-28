// Package doctor runs tamp's health checks and folds them into a report and
// an exit code. It only diagnoses, so it never stops at the first problem —
// every check reports regardless of the ones before it.
package doctor

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"

	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/router"
	"github.com/zhide915/tamp/internal/syncer"
)

// Status is a check's verdict.
type Status int

const (
	Pass Status = iota // tamp can work
	Warn               // tamp can work, but something will bite later
	Fail               // tamp cannot work until this is fixed
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

// Check is one diagnosis.
type Check struct {
	Name   string
	Status Status
	Detail string
	// Fix is empty on a pass and required on a fail.
	Fix string
	// Code is the exit code a failure here produces.
	Code exitcode.Code
}

// Report is the whole diagnosis.
type Report struct {
	Checks []Check
}

// Run performs every check tamp knows. envDirs are the registered
// environments' directories, for the path check.
func Run(ctx context.Context, e engine.Engine, r *router.Router, s syncer.Mutagen, envDirs []string) Report {
	return Report{Checks: []Check{
		dockerCheck(ctx, e),
		composeCheck(ctx, e),
		routerCheck(ctx, r),
		syncCheck(ctx, s, runtime.GOOS),
		localhostCheck(),
		pathsCheck(envDirs),
	}}
}

// OK reports no failures. Warnings never fail a script.
func (r Report) OK() bool {
	for _, c := range r.Checks {
		if c.Status == Fail {
			return false
		}
	}
	return true
}

// ExitCode is CodeOK, or the code of the first failing check.
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

// routerCheck warns rather than fails when the router is down: a machine with
// no environments needs none, and tamp starts it on demand. It still speaks
// up — with the router down nothing answers to a hostname.
func routerCheck(ctx context.Context, r *router.Router) Check {
	status, err := r.Status(ctx)
	if err != nil {
		// An unreachable engine is already the Docker check's failure; only
		// tamp's own unreadable state fails here.
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

// syncCheck never fails: Linux bind-mounts the source and has no Mutagen to
// miss, and elsewhere a missing binary is downloaded on first sync — or, if
// that is blocked, replaced by a bind mount.
func syncCheck(ctx context.Context, s syncer.Mutagen, goos string) Check {
	const name = "Sync"

	if syncer.Resolve(syncer.ModeAuto, goos) != syncer.UseMutagen {
		return Check{
			Name:   name,
			Status: Pass,
			Detail: "bind mount — this platform needs no Mutagen",
		}
	}

	binary, err := s.Find(ctx)
	if err != nil {
		return Check{
			Name:   name,
			Status: Warn,
			Detail: fmt.Sprintf("no Mutagen %s yet — tamp downloads it the first time it syncs", syncer.Version),
			Fix:    "nothing to do; if the download is blocked, tamp falls back to a bind mount and says so",
		}
	}

	where := "on PATH"
	if binary.Managed {
		where = "managed by tamp"
	}
	return Check{
		Name:   name,
		Status: Pass,
		Detail: fmt.Sprintf("Mutagen %s, %s, at %s", binary.Version, where, binary.Path),
	}
}

// localhostCheck is information rather than a diagnosis: *.localhost needs no
// setup in a browser, and that surprises people testing with plain resolvers.
func localhostCheck() Check {
	return Check{
		Name:   "Hostnames",
		Status: Pass,
		Detail: "*.localhost resolves in browsers with no setup; command-line tools may need 'curl --resolve'",
	}
}

// pathsCheck re-runs the create-time path warnings for every registered
// environment: a directory can start syncing to the cloud long after create.
func pathsCheck(envDirs []string) Check {
	const name = "Paths"
	var problems []string
	for _, dir := range envDirs {
		problems = append(problems, syncer.PathWarnings(dir)...)
	}
	if len(problems) > 0 {
		return Check{
			Name:   name,
			Status: Warn,
			Detail: strings.Join(problems, "; "),
			Fix:    "move the environment directory, or expect sync conflicts and quoting trouble",
		}
	}
	return Check{
		Name:   name,
		Status: Pass,
		Detail: "no environment lives in a cloud-synced or space-containing path",
	}
}

// failed splits an *exitcode.Error's message and fix into a check's fields.
func failed(name string, err error) Check {
	c := Check{Name: name, Status: Fail, Code: exitcode.Of(err), Detail: err.Error()}

	var e *exitcode.Error
	if errors.As(err, &e) {
		c.Detail, c.Fix = e.Msg, e.Fix
	}
	return c
}
