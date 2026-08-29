package env

import (
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/zhide915/tamp/internal/exitcode"
)

// defaultAppOwner is where bench resolves bare app names. tamp records the
// full clone URL so tamp.toml says where every app came from.
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
		// A duplicate would only fail minutes later, at the second fetch,
		// taking the whole create with it.
		if slices.ContainsFunc(apps, func(a App) bool { return a.Name == app.Name }) {
			return nil, exitcode.New(exitcode.CodeFailed,
				fmt.Sprintf("%s appears more than once in --apps", app.Name),
				"list each app once")
		}
		apps = append(apps, app)
	}
	return apps, nil
}

// ParseApp reads one spec: a bare name, a git URL, or either with ":branch".
// No branch means the repo's default branch — tamp never infers one from the
// Frappe version, because many apps have no matching release branch.
func ParseApp(spec string) (App, error) {
	repo, branch := splitBranch(spec)
	if repo == "" {
		return App{}, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("%q names no app", spec),
			"write an app as 'erpnext:version-15', or as a git URL with a branch after it")
	}

	// Sources the credential bridge can never serve fail here, before tamp
	// claims a name or writes anything; every echo is redacted — the spec
	// may embed a secret.
	if https, ssh := httpsFormOfSSH(repo); ssh {
		return App{}, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("%s is an ssh source, and tamp can only bridge git credentials into https fetches", redactedURL(repo)),
			fmt.Sprintf("use the https URL: %s", https))
	}
	if strings.Contains(repo, "://") {
		u, err := url.Parse(repo)
		if err != nil {
			// Refuse rather than let through: an unparseable URL cannot be
			// proven free of an embedded secret.
			return App{}, exitcode.New(exitcode.CodeFailed,
				fmt.Sprintf("tamp cannot read %s as a URL", redactedURL(repo)),
				"check the URL — and if it embeds a token, drop it: tamp asks the host's git credential system instead")
		}
		if u.User != nil {
			u.User = nil
			return App{}, exitcode.New(exitcode.CodeFailed,
				fmt.Sprintf("the URL for %s embeds a credential, and tamp never stores a secret", u.String()),
				"drop the token — tamp asks the host's git credential system when the repository needs one")
		}
	}

	source := repo
	name := repo
	if isRepoURL(repo) {
		name = appNameFromURL(repo)
	} else {
		// bench accepts owner/repo; tamp refuses it — glued onto the frappe
		// organisation's URL it would name a repository that does not exist,
		// failing only after the environment is built.
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

// splitBranch separates a spec into repository and branch. A repository has
// colons of its own — the scheme, a port, the scp-style host separator — so
// the branch search starts after them, letting a branch like feature/x parse.
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

func isRepoURL(repo string) bool {
	return strings.Contains(repo, "://") || strings.Contains(repo, "@")
}

// httpsFormOfSSH reports whether repo is an ssh source — an ssh:// URL or the
// scp form git@host:path — and translates it to the https URL the refusal
// suggests. The ssh port is dropped: it would be wrong for https.
func httpsFormOfSSH(repo string) (https string, ssh bool) {
	lower := strings.ToLower(repo)
	for _, scheme := range []string{"ssh://", "git+ssh://", "ssh+git://"} {
		if strings.HasPrefix(lower, scheme) {
			rest := repo[len(scheme):]
			if at := strings.Index(rest, "@"); at >= 0 {
				rest = rest[at+1:]
			}
			if slash := strings.Index(rest, "/"); slash >= 0 {
				if colon := strings.Index(rest[:slash], ":"); colon >= 0 {
					rest = rest[:colon] + rest[slash:]
				}
			}
			return "https://" + rest, true
		}
	}
	if !strings.Contains(repo, "://") && strings.Contains(repo, "@") {
		rest := repo[strings.Index(repo, "@")+1:]
		return "https://" + strings.Replace(rest, ":", "/", 1), true
	}
	return "", false
}

// redactedURL strips everything before the last "@" textually — parsing may
// have failed, so the authority's true end is unknown and a secret may hold
// any character; over-cutting is the safe direction.
func redactedURL(repo string) string {
	scheme, rest := "", repo
	if i := strings.Index(repo, "://"); i >= 0 {
		scheme, rest = repo[:i+3], repo[i+3:]
	}
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		rest = rest[at+1:]
	}
	return scheme + rest
}

// appNameFromURL takes the last URL segment — the directory bench itself
// clones into.
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

// ParseInstallApps reads `tamp site new`'s --apps: names of apps already on
// the bench. A branch is rejected rather than ignored — installing fetches
// nothing, so a branch here would be a silently dropped pin.
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
		// repo, not field: "erpnext:" carries an empty branch, and the colon
		// would never match a bench directory name.
		names = append(names, repo)
	}
	return names, nil
}
