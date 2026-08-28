package env

import (
	"fmt"
	"slices"
	"strings"

	"github.com/zhide915/tamp/internal/exitcode"
)

// defaultAppOwner is the GitHub organisation bench resolves a bare app name
// against. tamp records the URL that name will actually be cloned from rather
// than the name alone, so tamp.toml says where an app came from even for the
// spelling that leaves it out.
const defaultAppOwner = "https://github.com/frappe/"

// ParseApps reads the --apps value: a comma-separated list of app specs.
func ParseApps(spec string) ([]App, error) {
	apps := []App{}
	for field := range strings.SplitSeq(spec, ",") {
		if field = strings.TrimSpace(field); field == "" {
			continue
		}
		app, err := ParseApp(field)
		if err != nil {
			return nil, err
		}
		// A duplicate would surface minutes later, when the second fetch fails
		// against the app the first one put on the bench — and take the whole
		// create down with it.
		if slices.ContainsFunc(apps, func(a App) bool { return a.Name == app.Name }) {
			return nil, exitcode.New(exitcode.CodeFailed,
				fmt.Sprintf("%s appears more than once in --apps", app.Name),
				"list each app once")
		}
		apps = append(apps, app)
	}
	return apps, nil
}

// ParseApp reads one app spec: a bare name, a git URL, or either with a branch
// after a colon.
//
// A spec without a branch is not an error and not a guess — it is the repo's
// default branch, which is the only rule that holds for every app. tamp says
// so out loud rather than inferring a branch from the bench's Frappe version,
// because plenty of community apps have no version-15 branch to infer.
func ParseApp(spec string) (App, error) {
	repo, branch := splitBranch(spec)
	if repo == "" {
		return App{}, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("%q names no app", spec),
			"write an app as 'erpnext:version-15', or as a git URL with a branch after it")
	}

	source := repo
	name := repo
	if isRepoURL(repo) {
		name = appNameFromURL(repo)
	} else {
		// bench accepts owner/repo spellings; tamp does not, because gluing
		// one onto the frappe organisation's URL would fetch from a repository
		// that does not exist — and only after the whole environment is built.
		if strings.Contains(repo, "/") {
			return App{}, exitcode.New(exitcode.CodeFailed,
				fmt.Sprintf("%q is neither an app name nor a git URL", spec),
				fmt.Sprintf("write the name alone (%s) or the full URL (https://github.com/%s)",
					repo[strings.LastIndex(repo, "/")+1:], repo))
		}
		source = defaultAppOwner + repo
	}
	if name == "" {
		return App{}, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("tamp cannot tell which app %q is", spec),
			"give the app its name too: 'tamp exec <env> -- bench get-app <name> <url>'")
	}
	return App{Name: name, Source: source, Branch: branch}, nil
}

// splitBranch separates an app spec into its repository and its branch.
//
// The subtlety is that a repository reference has colons of its own. In a URL
// they belong to the scheme and to the port, both of which sit before the
// first slash of the path; in the scp-like form git@host:path the first colon
// separates the host. So the search for a branch starts after whichever of
// those the spec has, which is what lets a branch called feature/x be told
// apart from the path it would otherwise look like.
func splitBranch(spec string) (repo, branch string) {
	from := 0
	if scheme := strings.Index(spec, "://"); scheme >= 0 {
		if slash := strings.Index(spec[scheme+3:], "/"); slash >= 0 {
			from = scheme + 3 + slash
		} else {
			from = len(spec)
		}
	} else if colon := strings.Index(spec, ":"); colon >= 0 && strings.Contains(spec[:colon], "@") {
		from = colon + 1
	}

	if from >= len(spec) {
		return spec, ""
	}
	colon := strings.LastIndex(spec[from:], ":")
	if colon < 0 {
		return spec, ""
	}
	return spec[:from+colon], spec[from+colon+1:]
}

// isRepoURL reports whether a spec names a repository by address rather than
// by the short name bench resolves itself.
func isRepoURL(repo string) bool {
	return strings.Contains(repo, "://") || strings.Contains(repo, "@")
}

// appNameFromURL takes the app's name from the last segment of its clone URL,
// which is what bench itself names the directory after.
func appNameFromURL(url string) string {
	url = strings.TrimSuffix(strings.TrimSuffix(url, "/"), ".git")
	if slash := strings.LastIndexAny(url, "/:"); slash >= 0 {
		url = url[slash+1:]
	}
	return strings.TrimSuffix(url, ".git")
}

// AppNames lists the apps' names in the order they were given.
func AppNames(apps []App) []string {
	names := make([]string, len(apps))
	for i, app := range apps {
		names[i] = app.Name
	}
	return names
}

// ParseInstallApps reads the --apps value of `tamp site new`: the names of
// apps that are already on the bench.
//
// A branch is rejected rather than ignored. Installing an app onto a site does
// not fetch anything, so a branch here would be a pin tamp silently dropped —
// and the branch a bench holds was decided when the app was fetched onto it.
func ParseInstallApps(spec string) ([]string, error) {
	names := []string{}
	for field := range strings.SplitSeq(spec, ",") {
		if field = strings.TrimSpace(field); field == "" {
			continue
		}
		repo, branch := splitBranch(field)
		if branch != "" {
			return nil, exitcode.New(exitcode.CodeFailed,
				fmt.Sprintf("%q pins a branch, and installing an app onto a site fetches nothing", field),
				fmt.Sprintf("name the app alone: --apps %s", repo))
		}
		// repo rather than field: "erpnext:" carries an empty branch, and the
		// colon would otherwise be compared against bench directory names.
		names = append(names, repo)
	}
	return names, nil
}
