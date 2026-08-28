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

// benchSim models the Frappe bench behind the recorded engine: which apps a
// fetch produced, which sites exist, and what each site has installed. It is
// a module of its own so that the recording half of the Fake and the bench
// model change for different reasons — the recorder tracks the engine's
// interface, this tracks tamp's bench choreography.
//
// A recording alone would not do: tamp fetches an app or creates a site and
// then reads the bench back to find out what it now holds, so a fake that
// forgot the write would let a broken round trip pass.
type benchSim struct {
	sites    map[string]bool
	apps     map[string]bool
	siteApps map[string][]string

	// aliases, put and drop reach back into the Fake: the alias table is the
	// test's to script, and site configs land in the same container
	// filesystem every other write does.
	aliases func() map[string]string
	put     func(path, body string)
	drop    func(path string)
}

// reset is what removing the volumes does to a bench: everything it held
// lived in them.
func (s *benchSim) reset() {
	s.sites, s.apps, s.siteApps = nil, nil, nil
}

// answer keeps the bench model in step with the command tamp just ran, and
// answers the ones that ask. Arguments travel beside the script rather than
// inside it, which is what makes the hostname and the app name readable at a
// fixed position.
func (s *benchSim) answer(exec Exec, stdout io.Writer) error {
	if strings.Contains(exec.Line(), "bench init") {
		s.put(frappe.CommonSiteConfigPath, BenchInitConfig)
		// bench init clones frappe, which is what makes a second run of it
		// against the same bench a different code path.
		s.addApp(frappe.FrappeApp)
		return nil
	}

	// The probe tamp uses to decide whether a bench already has a source
	// tree. It has to answer no for a bench the fake has never initialized,
	// or every create would take the rebuild path.
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
	// The listing script names a site by the config file every site has.
	case strings.Contains(exec.Line(), "site_config.json"):
		for _, host := range s.sitesSorted() {
			fmt.Fprintln(stdout, host)
		}
	case strings.Contains(exec.Line(), "bench get-app"):
		name := appNameFromSource(scriptArg(exec.Cmd, 0))
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
		// Every Frappe site has frappe installed; anything else got there
		// through an install-app above.
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
	// Creating a site writes its own config, which is where the database name
	// Frappe invented is recorded — and the only place anything can read it.
	s.put(frappe.SiteConfigPath(host), fmt.Sprintf(`{"db_name": %q}`, "_"+strings.ReplaceAll(host, ".", "_")))
}

func (s *benchSim) appsSorted() []string  { return slices.Sorted(maps.Keys(s.apps)) }
func (s *benchSim) sitesSorted() []string { return slices.Sorted(maps.Keys(s.sites)) }

// firstScriptArg is where the arguments tamp passes beside a script start:
// bash -c <script> tamp <arg>...
const firstScriptArg = 4

// scriptArg is the nth argument tamp passed beside a script it ran.
func scriptArg(cmd []string, n int) string {
	if len(cmd) > firstScriptArg+n {
		return cmd[firstScriptArg+n]
	}
	return ""
}

// siteArg is the hostname a bench site command was pointed at.
//
// It reads both spellings the fake sees. tamp's own scripts carry the
// hostname beside the script as its first argument; a user reaching the same
// bench command through 'tamp exec' types it straight after the subcommand,
// and a fake that only understood the first would make a site created that way
// invisible to the code that goes looking for it.
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

// appNameFromSource is the app directory a clone URL produces, which is the
// last segment of its path.
func appNameFromSource(source string) string {
	source = strings.TrimSuffix(strings.TrimSuffix(source, "/"), ".git")
	if i := strings.LastIndexAny(source, "/:"); i >= 0 {
		source = source[i+1:]
	}
	return source
}
