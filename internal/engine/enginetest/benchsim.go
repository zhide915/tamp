package enginetest

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/zhide915/tamp/internal/engine"
	"github.com/zhide915/tamp/internal/frappe"
)

// benchSim models the Frappe bench behind the fake: apps, sites, per-site
// installs. Separate from the recorder because tamp writes to the bench and
// then reads it back — recording alone would let a broken round trip pass.
type benchSim struct {
	sites    map[string]bool
	apps     map[string]bool
	siteApps map[string][]string

	// Hooks back into the Fake: the alias, private and missing tables are
	// the test's to script, and site configs land in the shared container
	// filesystem.
	aliases func() map[string]string
	private func() map[string]string
	missing func() map[string]bool
	put     func(path, body string)
	drop    func(path string)
}

// reset models volume removal: everything the bench held lived there.
func (s *benchSim) reset() {
	s.sites, s.apps, s.siteApps = nil, nil, nil
}

// answer updates the model for the command just run and replies to the ones
// that ask. Script arguments sit beside the script at fixed positions.
func (s *benchSim) answer(exec Exec, stdout, stderr io.Writer) error {
	if stderr == nil {
		stderr = io.Discard
	}

	// The preflight probe: reachable answers, missing and locked refuse the
	// way real git does, writing why to stderr.
	if strings.Contains(exec.Line(), "git ls-remote") {
		return s.remoteRefusal(exec, scriptArg(exec.Cmd, 0), stderr)
	}

	if strings.Contains(exec.Line(), "bench init") {
		s.put(frappe.CommonSiteConfigPath, BenchInitConfig)
		// bench init clones frappe; a second run against the same bench is a
		// different code path.
		s.addApp(frappe.FrappeApp)
		return nil
	}

	// The source-tree probe must answer no for a never-initialized bench, or
	// every create would take the rebuild path.
	if strings.Contains(exec.Line(), `test -d "`+frappe.AppsDir) {
		if !s.apps[scriptArg(exec.Cmd, 0)] {
			return &engine.ExitError{Container: exec.Container, Cmd: exec.Cmd, Status: 1}
		}
		return nil
	}

	switch {
	case strings.Contains(exec.Line(), "bench new-site"):
		s.addSite(siteArg(exec.Cmd, "new-site"))
	case strings.Contains(exec.Line(), "bench drop-site"):
		host := siteArg(exec.Cmd, "drop-site")
		delete(s.sites, host)
		s.drop(frappe.SiteConfigPath(host))
	// The listing script identifies sites by the config file every site has.
	case strings.Contains(exec.Line(), "site_config.json"):
		for _, host := range s.sitesSorted() {
			fmt.Fprintln(stdout, host)
		}
	case strings.Contains(exec.Line(), "bench get-app"):
		source := scriptArg(exec.Cmd, 0)
		if err := s.remoteRefusal(exec, source, stderr); err != nil {
			return err
		}
		name := appNameFromSource(source)
		if declared, ok := s.aliases()[name]; ok {
			name = declared
		}
		s.addApp(name)
	case strings.Contains(exec.Line(), "install-app"):
		host, app := scriptArg(exec.Cmd, 0), scriptArg(exec.Cmd, 1)
		if s.siteApps == nil {
			s.siteApps = map[string][]string{}
		}
		s.siteApps[host] = append(s.siteApps[host], app)
	case strings.Contains(exec.Line(), "list-apps"):
		// Every site has frappe; the rest arrived via install-app.
		fmt.Fprintln(stdout, "frappe")
		for _, app := range s.siteApps[siteArg(exec.Cmd, "--site")] {
			fmt.Fprintln(stdout, app)
		}
	case strings.Contains(exec.Line(), "cd "+frappe.AppsDir):
		for _, app := range s.appsSorted() {
			fmt.Fprintln(stdout, app)
		}
	}
	return nil
}

// remoteRefusal is git meeting the scripted repository tables. The words on
// stderr are the real git's, because that is what tamp classifies.
func (s *benchSim) remoteRefusal(exec Exec, source string, stderr io.Writer) error {
	if s.missing()[source] {
		fmt.Fprintf(stderr, "fatal: repository '%s' not found\n", source)
		return &engine.ExitError{Container: exec.Container, Cmd: exec.Cmd, Status: 128}
	}
	if _, private := s.private()[source]; private {
		fmt.Fprintf(stderr, "fatal: could not read Username for '%s': terminal prompts disabled\n", source)
		return &engine.ExitError{Container: exec.Container, Cmd: exec.Cmd, Status: 128}
	}
	return nil
}

func (s *benchSim) addApp(name string) {
	if name == "" {
		return
	}
	if s.apps == nil {
		s.apps = map[string]bool{}
	}
	s.apps[name] = true
}

func (s *benchSim) addSite(host string) {
	if host == "" {
		return
	}
	if s.sites == nil {
		s.sites = map[string]bool{}
	}
	s.sites[host] = true
	// Site creation writes the site config — the only place the invented
	// db_name can be read from.
	s.put(frappe.SiteConfigPath(host), fmt.Sprintf(`{"db_name": %q}`, "_"+strings.ReplaceAll(host, ".", "_")))
}

func (s *benchSim) appsSorted() []string  { return slices.Sorted(maps.Keys(s.apps)) }
func (s *benchSim) sitesSorted() []string { return slices.Sorted(maps.Keys(s.sites)) }

// Script args start after: bash -c <script> tamp.
const firstScriptArg = 4

// scriptArg is the nth argument passed beside a script.
func scriptArg(cmd []string, n int) string {
	if len(cmd) > firstScriptArg+n {
		return cmd[firstScriptArg+n]
	}
	return ""
}

// siteArg is the hostname a bench site command targets. Both spellings
// occur: tamp's scripts pass it as the first script argument, while a user
// via 'tamp exec' types it right after the subcommand.
func siteArg(cmd []string, subcommand string) string {
	if len(cmd) > 0 && cmd[0] == "bash" {
		return scriptArg(cmd, 0)
	}
	for i, word := range cmd {
		if word == subcommand && i+1 < len(cmd) {
			return cmd[i+1]
		}
	}
	return ""
}

// appNameFromSource is the directory a clone URL produces: the last path
// segment.
func appNameFromSource(source string) string {
	source = strings.TrimSuffix(strings.TrimSuffix(source, "/"), ".git")
	if i := strings.LastIndexAny(source, "/:"); i >= 0 {
		source = source[i+1:]
	}
	return source
}
