// Package doctor runs tamp's health checks and folds them into a report and
// an exit code. It only diagnoses, so it never stops at the first problem —
// every check reports regardless of the ones before it.
package doctor

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/hosts"
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

// Input is what the caller already read off the machine: the registry, and
// the hosts file tamp keeps a block in. Both arrive as data — including the
// errors — because a check reports, and only a report can say that reading
// the registry is the thing that failed.
type Input struct {
	// RegistryErr is the registry refusing to be read.
	RegistryErr error
	// EnvDirs are the registered environments' directories, for the path
	// check.
	EnvDirs []string
	// Hosts is what tamp's block in the hosts file holds against what it
	// should.
	Hosts HostsState
}

// HostsState is the hosts file as the caller found it.
type HostsState struct {
	// Path is the hosts file tamp reconciles.
	Path string
	// Wanted are the hostnames tamp's block should carry, Present the ones
	// it does.
	Wanted, Present []string
	// Err is the hosts file refusing to be read.
	Err error
}

// Run performs every check tamp knows.
func Run(ctx context.Context, e engine.Engine, r *router.Router, s syncer.Mutagen, in Input) Report {
	return Report{Checks: []Check{
		dockerCheck(ctx, e),
		composeCheck(ctx, e),
		routerCheck(ctx, r),
		syncCheck(ctx, s, runtime.GOOS),
		hostGitCheck(ctx),
		registryCheck(in),
		hostsCheck(in),
		localhostCheck(),
		pathsCheck(in.EnvDirs),
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

// hostGitCheck never fails: only the credential bridge needs host git, and
// only for private repositories (ADR 0002).
func hostGitCheck(ctx context.Context) Check {
	const name = "Host git"
	out, err := exec.CommandContext(ctx, "git", "version").Output()
	if err != nil {
		return Check{
			Name:   name,
			Status: Warn,
			Detail: "not found — tamp needs it only to fetch private app repositories",
			Fix:    "install git and sign in to your git host once; everything else works without it",
		}
	}
	return Check{
		Name:   name,
		Status: Pass,
		Detail: strings.TrimSpace(string(out)) + " — used only for private app repositories",
	}
}

// registryCheck exists because everything below it reads the registry: an
// unreadable one used to abort the report, which is the one thing a
// diagnosis must never do.
func registryCheck(in Input) Check {
	const name = "Registry"
	if in.RegistryErr != nil {
		return failed(name, in.RegistryErr)
	}
	return Check{
		Name:   name,
		Status: Pass,
		Detail: fmt.Sprintf("%d environment(s) registered", len(in.EnvDirs)),
	}
}

// hostsCheck compares tamp's block with the custom domains the machine
// answers to. It never fails: a pending entry costs a name, not the tool.
func hostsCheck(in Input) Check {
	const name = "Hosts file"
	const fix = "run 'tamp hosts sync'"

	if in.RegistryErr != nil {
		return Check{
			Name:   name,
			Status: Warn,
			Detail: "tamp cannot tell which hostnames belong in its block — the registry check above failed",
			Fix:    "fix the registry, then run 'tamp doctor' again",
		}
	}
	if in.Hosts.Err != nil {
		return Check{
			Name:   name,
			Status: Warn,
			Detail: in.Hosts.Err.Error(),
			Fix:    "check the permissions on " + in.Hosts.Path,
		}
	}

	missing := hosts.Missing(in.Hosts.Present, in.Hosts.Wanted)
	stale := hosts.Missing(in.Hosts.Wanted, in.Hosts.Present)
	switch {
	case len(missing) > 0 && len(stale) > 0:
		return Check{Name: name, Status: Warn, Fix: fix, Detail: fmt.Sprintf(
			"tamp's block in %s is out of date: %s missing, %s no longer routed",
			in.Hosts.Path, strings.Join(missing, ", "), strings.Join(stale, ", "))}
	case len(missing) > 0:
		return Check{Name: name, Status: Warn, Fix: fix, Detail: fmt.Sprintf(
			"%s does not resolve yet: no entry in %s", strings.Join(missing, ", "), in.Hosts.Path)}
	case len(stale) > 0:
		return Check{Name: name, Status: Warn, Fix: fix, Detail: fmt.Sprintf(
			"%s is in tamp's block but is no site of any environment", strings.Join(stale, ", "))}
	case len(in.Hosts.Wanted) == 0:
		return Check{Name: name, Status: Pass,
			Detail: "no site outside .localhost, so tamp's block in " + in.Hosts.Path + " is empty"}
	}
	return Check{Name: name, Status: Pass, Detail: fmt.Sprintf(
		"tamp's block in %s is in sync: %s", in.Hosts.Path, strings.Join(in.Hosts.Wanted, ", "))}
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
