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

// benchSim models the benches behind the fake: apps, sites, per-site
// installs. Separate from the recorder because tamp writes to a bench and
// then reads it back — recording alone would let a broken round trip pass.
//
// One bench per container, because that is how tamp draws the line: each
// environment's bench lives in its own volumes, so what one create leaves
// behind must be invisible to the next.
type benchSim struct {
	benches map[string]*benchState

	// Hooks back into the Fake: the alias, private and missing tables are
	// the test's to script, and site configs land in the shared container
	// filesystem.
	aliases func() map[string]string
	private func() map[string]string
	missing func() map[string]bool
	put     func(path, body string)
	drop    func(path string)
	has     func(path string) bool
}

// benchState is one environment's bench.
type benchState struct {
	sites    map[string]bool
	apps     map[string]bool
	siteApps map[string][]string
}

// at is the bench inside a container, created on first mention.
func (s *benchSim) at(container string) *benchState {
	if s.benches == nil {
		s.benches = map[string]*benchState{}
	}
	if b, ok := s.benches[container]; ok {
		return b
	}
	b := &benchState{}
	s.benches[container] = b
	return b
}

// reset models one project's volumes going: everything its bench held lived
// there, and no other environment's did.
func (s *benchSim) reset(project string) {
	for container := range s.benches {
		if strings.HasPrefix(container, project+"-") {
			delete(s.benches, container)
		}
	}
}

// answer updates the model for the command just run and replies to the ones
// that ask. Script arguments sit beside the script at fixed positions.
func (s *benchSim) answer(exec Exec, stdout, stderr io.Writer) error {
	if stderr == nil {
		stderr = io.Discard
	}
	b := s.at(exec.Container)

	// The preflight probe: reachable answers, missing and locked refuse the
	// way real git does, writing why to stderr.
	if strings.Contains(exec.Line(), "git ls-remote") {
		return s.remoteRefusal(exec, scriptArg(exec.Cmd, 0), stderr)
	}

	if strings.Contains(exec.Line(), "bench init") {
		s.initialize(b)
		return nil
	}

	// The template store, modelled as the files it is: a save puts the
	// tarball there, a restore unpacks the bench a save was taken from, and
	// the probe answers from what is actually in the store.
	if strings.Contains(exec.Line(), "gzip -1") {
		s.put(scriptArg(exec.Cmd, 0), "a tarred bench")
		return nil
	}
	if strings.Contains(exec.Line(), "-xzf") {
		s.initialize(b)
		return nil
	}
	if strings.Contains(exec.Line(), `test -f "$1"`) {
		if !s.has(scriptArg(exec.Cmd, 0)) {
			return &engine.ExitError{Container: exec.Container, Cmd: exec.Cmd, Status: 1}
		}
		return nil
	}

	// The source-tree probe must answer no for a never-initialized bench, or
	// every create would take the rebuild path.
	if strings.Contains(exec.Line(), `test -d "`+frappe.AppsDir) {
		if !b.apps[scriptArg(exec.Cmd, 0)] {
			return &engine.ExitError{Container: exec.Container, Cmd: exec.Cmd, Status: 1}
		}
		return nil
	}

	switch {
	case strings.Contains(exec.Line(), "bench new-site"):
		s.addSite(b, siteArg(exec.Cmd, "new-site"))
	case strings.Contains(exec.Line(), "bench drop-site"):
		host := siteArg(exec.Cmd, "drop-site")
		delete(b.sites, host)
		s.drop(frappe.SiteConfigPath(host))
	// The listing script identifies sites by the config file every site has.
	case strings.Contains(exec.Line(), "site_config.json"):
		for _, host := range b.sitesSorted() {
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
		b.addApp(name)
	case strings.Contains(exec.Line(), "install-app"):
		host, app := scriptArg(exec.Cmd, 0), scriptArg(exec.Cmd, 1)
		if b.siteApps == nil {
			b.siteApps = map[string][]string{}
		}
		b.siteApps[host] = append(b.siteApps[host], app)
	case strings.Contains(exec.Line(), "list-apps"):
		// Every site has frappe; the rest arrived via install-app.
		fmt.Fprintln(stdout, "frappe")
		for _, app := range b.siteApps[siteArg(exec.Cmd, "--site")] {
			fmt.Fprintln(stdout, app)
		}
	// The container reads the Procfile at boot to decide whether it has a
	// bench to run, so its coming and going is state, not just a command.
	case strings.Contains(exec.Line(), "rm -f") && scriptArg(exec.Cmd, 0) == frappe.ProcfilePath:
		s.drop(frappe.ProcfilePath)
	case strings.Contains(exec.Line(), "cd "+frappe.AppsDir):
		for _, app := range b.appsSorted() {
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
	password, private := s.private()[source]
	if !private || (password != "" && slices.Contains(exec.Env, frappe.CredentialPasswordVar+"="+password)) {
		return nil
	}
	fmt.Fprintf(stderr, "fatal: could not read Username for '%s': terminal prompts disabled\n", source)
	return &engine.ExitError{Container: exec.Container, Cmd: exec.Cmd, Status: 128}
}

// initialize is what leaves a bench where there was none — bench init, or a
// stored template unpacked in its place. Both put frappe in apps/ and leave
// bench's own shared config behind.
func (s *benchSim) initialize(b *benchState) {
	s.put(frappe.CommonSiteConfigPath, BenchInitConfig)
	b.addApp(frappe.FrappeApp)
}

func (b *benchState) addApp(name string) {
	if name == "" {
		return
	}
	if b.apps == nil {
		b.apps = map[string]bool{}
	}
	b.apps[name] = true
}

func (s *benchSim) addSite(b *benchState, host string) {
	if host == "" {
		return
	}
	if b.sites == nil {
		b.sites = map[string]bool{}
	}
	b.sites[host] = true
	// Site creation writes the site config — the only place the invented
	// db_name can be read from.
	s.put(frappe.SiteConfigPath(host), fmt.Sprintf(`{"db_name": %q}`, "_"+strings.ReplaceAll(host, ".", "_")))
}

func (b *benchState) appsSorted() []string  { return slices.Sorted(maps.Keys(b.apps)) }
func (b *benchState) sitesSorted() []string { return slices.Sorted(maps.Keys(b.sites)) }

// allApps and allSites collapse every bench into one answer, for the tests
// that run a single environment and just want to know what is on it.
func (s *benchSim) allApps() []string {
	seen := map[string]bool{}
	for _, b := range s.benches {
		maps.Copy(seen, b.apps)
	}
	return slices.Sorted(maps.Keys(seen))
}

func (s *benchSim) allSites() []string {
	seen := map[string]bool{}
	for _, b := range s.benches {
		maps.Copy(seen, b.sites)
	}
	return slices.Sorted(maps.Keys(seen))
}

// siteAppsOf finds the site wherever it is: hostnames are unique across the
// machine, so at most one bench has it.
func (s *benchSim) siteAppsOf(host string) []string {
	for _, b := range s.benches {
		if apps, ok := b.siteApps[host]; ok {
			return apps
		}
	}
	return nil
}

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
