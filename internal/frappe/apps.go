package frappe

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// AppsTxt lists the apps the bench has installed, in the order Frappe loads
// their hooks.
const AppsTxt = SitesDir + "/apps.txt"

// benchPython is the bench's own Python environment, which bench init makes.
const benchPython = BenchDir + "/env/bin/python"

// AppInstalled is tamp's marker that an app is installed into the bench's
// Python environment. It lives in the environment, so a new one has none.
func AppInstalled(app string) string { return BenchDir + "/env/.tamp-app-" + app }

// NodeInstalled is tamp's marker that an app's node packages are installed.
// It lives in node_modules, so it vouches for the packages there, which the
// sync keeps in the container when the host clones the app anew.
func NodeInstalled(app string) string { return AppsDir + "/" + app + "/node_modules/.tamp-installed" }

// AssetsBuilt is tamp's marker that the bench volume holds an app's built
// assets, in sites/assets, so a new volume has none.
func AssetsBuilt(app string) string { return SitesDir + "/.tamp-assets-" + app }

// InstallApp installs an app into the bench's Python environment, editable,
// as bench get-app does.
func InstallApp(app string) []string {
	return []string{"uv", "pip", "install", "--quiet", "-e", AppsDir + "/" + app, "--python", benchPython}
}

// UninstallApp removes an app from the bench's Python environment.
func UninstallApp(app string) []string {
	return []string{"uv", "pip", "uninstall", app, "--python", benchPython}
}

// NodePackages installs an app's node packages, as bench get-app does. Yarn
// makes no node_modules for an app without dependencies, and NodeInstalled goes there.
func NodePackages(app string) []string {
	return []string{"bash", "-c", `cd "$1" && yarn install --check-files && mkdir -p node_modules`, "tamp", AppsDir + "/" + app}
}

// BuildAssets builds an app's assets, as bench get-app does.
func BuildAssets(app string) []string {
	return []string{"env", "BENCH_DEVELOPER=1", "bench", "build", "--app", app}
}

// SiteApps prints a JSON object that maps the site to the apps installed on it.
func SiteApps(host string) []string {
	return []string{"bench", "--site", host, "list-apps", "--format", "json"}
}

// InstallSiteApp installs an app on a site.
func InstallSiteApp(host, app string) []string {
	return []string{"bench", "--site", host, "install-app", app}
}

// UninstallSiteApp removes an app and its data from a site.
func UninstallSiteApp(host, app string) []string {
	return []string{"bench", "--site", host, "uninstall-app", app, "--yes", "--no-backup"}
}

// RestartProcesses stops honcho, which the frappe container runs again 5 s
// later: a running process never loads an app installed after it started.
func RestartProcesses() []string {
	return []string{"bash", "-c",
		`for p in /proc/[0-9]*; do if [ "$(cat "$p/comm" 2>/dev/null)" = honcho ]; then kill -TERM "${p#/proc/}"; fi; done`}
}

// ParseSiteApps reads the apps of host from SiteApps's output.
func ParseSiteApps(out []byte, host string) ([]string, error) {
	// Frappe may print notices before the JSON.
	start := bytes.IndexByte(out, '{')
	if start < 0 {
		return nil, fmt.Errorf("bench list-apps printed no JSON: %q", out)
	}
	var apps map[string][]string
	if err := json.Unmarshal(out[start:], &apps); err != nil {
		return nil, fmt.Errorf("bench list-apps printed bad JSON: %w", err)
	}
	return apps[host], nil
}

// AssetsInClone is tamp's marker that an app's clone holds its built assets,
// which bench build writes into the app's public/dist. In a repository it
// goes inside .git, so git status never shows it.
func AssetsInClone(app string, repo bool) string { return inClone(app, "tamp-assets", repo) }

// NodeInClone is tamp's marker that an app's clone had its node packages
// installed, so a fresh clone gets them anew even where node_modules was kept.
func NodeInClone(app string, repo bool) string { return inClone(app, "tamp-node", repo) }

func inClone(app, marker string, repo bool) string {
	if repo {
		return AppsDir + "/" + app + "/.git/" + marker
	}
	return AppsDir + "/" + app + "/." + marker
}

// Paused is tamp's: while it exists, the frappe container starts no bench
// processes, even after a stop and start.
const Paused = BenchDir + "/.tamp-paused"

// StopProcesses stops honcho and waits up to 60 s for it and every node
// process to exit, so nothing keeps files open in an app's node_modules.
// Paused keeps the container from starting them again. On a timeout it names
// each process left, such as a node one started through `tamp shell`.
func StopProcesses() []string {
	return []string{"bash", "-c", `
running() {
  for p in /proc/[0-9]*; do
    case "$(cat "$p/comm" 2>/dev/null)" in honcho | node | esbuild) return 0 ;; esac
  done
  return 1
}
left() {
  for p in /proc/[0-9]*; do
    case "$(cat "$p/comm" 2>/dev/null)" in
    honcho | node | esbuild) echo "  pid ${p#/proc/}: $(tr '\0' ' ' 2>/dev/null <"$p/cmdline")" >&2 ;;
    esac
  done
}
for p in /proc/[0-9]*; do
  if [ "$(cat "$p/comm" 2>/dev/null)" = honcho ]; then kill -TERM "${p#/proc/}"; fi
done
for _ in $(seq 120); do
  running || exit 0
  sleep 0.5
done
echo "the bench's processes did not stop within 60 s; these still run, and may hold files in node_modules:" >&2
left
echo "end any that you started through tamp shell, such as bench watch" >&2
exit 1`}
}
