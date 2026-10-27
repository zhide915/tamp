package env

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	neturl "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/zhide915/tamp/internal/exitcode"
)

// FrappeURL is where Frappe is cloned from.
const FrappeURL = "https://github.com/frappe/frappe"

// frappeOrg is where a bare app name points, as in bench get-app.
const frappeOrg = "https://github.com/frappe/"

// AppsDir is the folder of an environment's apps, on the host.
const AppsDir = "apps"

// cloneApp clones url at branch into apps/<name> with host git, as bench
// get-app clones, unless the folder exists: tamp never touches a clone.
func (m *Manager) cloneApp(ctx context.Context, dir, name, url, branch string) error {
	target := filepath.Join(dir, AppsDir, name)
	if _, err := os.Stat(target); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return exitcode.New(exitcode.CodeFailed, fmt.Sprintf("cannot read %s: %v", target, err),
			"check that you can read the directory")
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return exitcode.New(exitcode.CodeFailed, fmt.Sprintf("cannot create %s: %v", filepath.Dir(target), err),
			"check that you can write to the directory")
	}
	// Cloned into a dot folder first: the sync skips those, so it never holds
	// a file open that git is still writing, which Windows refuses.
	tmp, err := os.MkdirTemp(filepath.Dir(target), "."+name+"-")
	if err != nil {
		return exitcode.New(exitcode.CodeFailed, fmt.Sprintf("cannot create a folder in %s: %v", filepath.Dir(target), err),
			"check that you can write to the directory")
	}
	m.p.Step("cloning " + url)
	if err := gitClone(ctx, url, branch, tmp, m.p.Progress()); err != nil {
		// The temp clone is tamp's own; nothing else refers to it yet.
		_ = os.RemoveAll(tmp)
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		return exitcode.New(exitcode.CodeFailed, fmt.Sprintf("cannot move the clone of %s to %s: %v", url, target, err),
			"check that nothing holds files open in "+filepath.Dir(target))
	}
	return nil
}

// gitClone clones url into target, which may be an empty folder. Without a
// branch, git clones the repository's default branch, as bench does.
func gitClone(ctx context.Context, url, branch, target string, out io.Writer) error {
	args := []string{"clone", "--depth", "1", "--origin", "upstream"}
	if branch != "" {
		args = append(args, "--branch", branch)
	}
	if runtime.GOOS == "windows" {
		// Kept in the clone's config, so line endings survive the trip into
		// the Linux container on every later checkout too.
		args = append(args, "--config", "core.autocrlf=false")
	}
	source := url
	if filepath.IsAbs(url) {
		// git ignores --depth on a plain local path.
		source = fileURL(url)
	}
	args = append(args, "--", source, target)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Stdout, cmd.Stderr = out, out
	err := cmd.Run()
	if err == nil && source != url {
		// The remote keeps the path as given, which adoption compares with.
		if said, err := exec.CommandContext(ctx, "git", "-C", target, "remote", "set-url", "upstream", url).CombinedOutput(); err != nil {
			// A clone left with its file:// upstream would be kept as it is.
			if rmErr := os.RemoveAll(target); rmErr != nil {
				return upstreamFailed{exitcode.New(exitcode.CodeFailed,
					fmt.Sprintf("cannot set the upstream of %s to %s, nor remove the clone: %v", target, url, rmErr),
					"delete "+target)}
			}
			return upstreamFailed{exitcode.New(exitcode.CodeFailed,
				fmt.Sprintf("cannot set the upstream of %s to %s: %s", target, url, strings.TrimSpace(string(said))),
				"check that git can write to "+filepath.Dir(target))}
		}
	}
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return exitcode.New(exitcode.CodeFailed, "git is not on PATH",
				"install git")
		}
		at := ""
		if branch != "" {
			at = " at " + branch
		}
		return cloneFailed{exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot clone %s%s: %v", url, at, err),
			"fix what git says above")}
	}
	return nil
}

// fileURL is the file:// URL of an absolute local path, a Windows one too,
// percent-encoded, since git decodes it.
func fileURL(path string) string {
	slashed := filepath.ToSlash(path)
	if rest, ok := strings.CutPrefix(slashed, "//"); ok {
		// A UNC path names its server first.
		host, p, _ := strings.Cut(rest, "/")
		return (&neturl.URL{Scheme: "file", Host: host, Path: "/" + p}).String()
	}
	return (&neturl.URL{Scheme: "file", Path: "/" + strings.TrimPrefix(slashed, "/")}).String()
}

// localPath reports whether an app spec is a path on this machine: absolute,
// or relative, as in `../acme` or `src/acme`. A URL holds `:`, and a name no separator.
func localPath(spec string) bool {
	if filepath.IsAbs(spec) || spec == "." || spec == ".." {
		return true
	}
	separators := "/"
	if filepath.Separator == '\\' {
		separators = `/\`
	}
	return !strings.Contains(spec, ":") && strings.ContainsAny(spec, separators)
}

// cloneFailed is a failed git clone, whose fix depends on where the url came
// from.
type cloneFailed struct{ err *exitcode.Error }

func (c cloneFailed) Error() string { return c.err.Error() }
func (c cloneFailed) Unwrap() error { return c.err }

// upstreamFailed is a clone whose upstream git could not set: the command that
// cloned clones again.
type upstreamFailed struct{ err *exitcode.Error }

func (u upstreamFailed) Error() string { return u.err.Error() }
func (u upstreamFailed) Unwrap() error { return u.err }

// currentBranch is the branch a clone has checked out, or "" when it has
// none, as in a detached HEAD or a folder that is not a git repository.
func currentBranch(clone string) (string, error) {
	if !isRepo(clone) {
		return "", nil
	}
	// Exit 1 with --quiet is a detached HEAD.
	return gitRead(clone, "symbolic-ref", "--quiet", "--short", "HEAD")
}

// gitRead runs a read-only git command in a clone and returns its output, or
// "" when git exits 1, which these commands use for "none".
func gitRead(clone string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", clone}, args...)...).Output()
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		return "", nil
	case err != nil:
		said := err.Error()
		if errors.As(err, &exit) && len(bytes.TrimSpace(exit.Stderr)) > 0 {
			said = string(bytes.TrimSpace(exit.Stderr))
		}
		return "", exitcode.New(exitcode.CodeFailed, fmt.Sprintf("git cannot read %s: %s", clone, said),
			"check the repository with `git -C "+clone+" status`")
	}
	return strings.TrimSpace(string(out)), nil
}

// isRepo reports whether a folder is a git repository of its own. Git would
// otherwise answer for a repository around it, such as the team's.
func isRepo(folder string) bool {
	_, err := os.Stat(filepath.Join(folder, ".git"))
	return err == nil
}

// remoteURL is the URL a clone fetches from, upstream as tamp and bench name
// it or else origin, or "" when it has neither.
func remoteURL(clone string) (string, error) {
	if !isRepo(clone) {
		return "", nil
	}
	for _, remote := range []string{"upstream", "origin"} {
		// get-url would apply insteadOf rewrites; the config holds what was cloned.
		url, err := gitRead(clone, "config", "--local", "--get", "remote."+remote+".url")
		if err != nil || url != "" {
			return url, err
		}
	}
	return "", nil
}

// parseAppSpec reads `<url|name>[#branch]`. The branch follows `#` because
// an SSH URL already holds `@` and `:`.
func parseAppSpec(spec string) (url, branch string, err error) {
	const fix = "give the app as `<url|name>[#branch]`, such as `hrms` or `git@github.com:acme/acme.git#main`"
	url, branch, hasBranch := strings.Cut(spec, "#")
	switch {
	case url == "":
		return "", "", exitcode.Usage(fmt.Sprintf("%q names no app", spec), fix)
	case hasBranch && branch == "":
		return "", "", exitcode.Usage(fmt.Sprintf("%q has no branch after `#`", spec), fix)
	case strings.HasPrefix(url, "-"):
		return "", "", exitcode.Usage(fmt.Sprintf("%q is not an app URL or name", spec), fix)
	}
	switch {
	case localPath(url):
		// Absolute, so the recipe names the same repository from any directory.
		abs, err := filepath.Abs(url)
		if err != nil {
			return "", "", err
		}
		url = abs
	case !strings.ContainsAny(url, "/:"):
		if !bareName.MatchString(url) {
			return "", "", exitcode.Usage(fmt.Sprintf("%q is not an app URL or name", spec), fix)
		}
		url = frappeOrg + url
	}
	if repoName(url) == "frappe" {
		return "", "", exitcode.Usage("Frappe is not an app to add: every bench has it",
			"leave Frappe out: the recipe's `frappe` key names its branch")
	}
	return url, branch, nil
}

var bareName = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// repoName is the last part of a repository URL, without `.git`.
func repoName(url string) string {
	url = strings.TrimSuffix(strings.TrimRight(url, `/\`), ".git")
	// A Windows path separates with a backslash.
	if i := strings.LastIndexAny(url, `/\:`); i >= 0 {
		url = url[i+1:]
	}
	return url
}

// setupPyName finds `name="..."` in a setup.py, as bench reads a literal one.
var setupPyName = regexp.MustCompile(`\bname\s*=\s*["']([^"']+)["']`)

// packageName reads an app's name from its package, as bench get-app does:
// pyproject.toml first, then setup.py.
func packageName(clone string) (string, error) {
	var pyproject struct {
		Project struct {
			Name string `toml:"name"`
		} `toml:"project"`
	}
	_, err := toml.DecodeFile(filepath.Join(clone, "pyproject.toml"), &pyproject)
	switch {
	case err == nil && pyproject.Project.Name != "":
		return pyproject.Project.Name, nil
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return "", fmt.Errorf("cannot read pyproject.toml: %v", err)
	}
	setup, err := os.ReadFile(filepath.Join(clone, "setup.py"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("cannot read setup.py: %v", err)
	}
	if m := setupPyName.FindSubmatch(setup); m != nil {
		return string(m[1]), nil
	}
	return "", errors.New("neither pyproject.toml nor setup.py names its package")
}

var (
	requiredAppsList = regexp.MustCompile(`(?m)^required_apps\s*=\s*\[([^\]]*)\]`)
	quoted           = regexp.MustCompile(`["']([^"']+)["']`)
)

// requiredApps reads the required_apps of an app's hooks.py: each entry is a
// bare name, `org/repo`, or a URL.
func requiredApps(clone, name string) ([]string, error) {
	hooks, err := os.ReadFile(filepath.Join(clone, name, "hooks.py"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	m := requiredAppsList.FindSubmatch(hooks)
	if m == nil {
		return nil, nil
	}
	var apps []string
	for _, q := range quoted.FindAllSubmatch(m[1], -1) {
		apps = append(apps, string(q[1]))
	}
	return apps, nil
}

// requiredAppSpec is the `tamp app add` argument for a required_apps entry.
func requiredAppSpec(entry string) string {
	if strings.Contains(entry, "/") && !strings.Contains(entry, ":") {
		return "https://github.com/" + entry
	}
	return entry
}

// tempClone is the folder name os.MkdirTemp gives a clone in progress, which
// a killed run or a failed rename leaves behind.
var tempClone = regexp.MustCompile(`^\.[A-Za-z0-9_.-]+-[0-9]+$`)
