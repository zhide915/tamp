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

	// staged is what a snapshot holds, per site: the app list a restore
	// brings back. It sits here rather than on a bench because a snapshot is
	// a file beside the environment, outliving the bench it came from.
	staged map[string][]string

	// seeds is what each stored seed holds, keyed by its path in the store.
	// Like the staging area it outlives any one bench: the store is shared by
	// every environment on the machine.
	seeds map[string][]string

	// Hooks back into the Fake: the alias, private, denied and missing tables
	// are the test's to script, and site configs land in the shared container
	// filesystem.
	aliases func() map[string]string
	private func() map[string]string
	denied  func() map[string]string
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
	// depsWiped is a deps clean not yet rebuilt: bench itself runs from the
	// virtualenv, so the deps probe must answer no until requirements return.
	depsWiped bool
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

	// The snapshot bundle moves between the host and the container as a
	// stream, so the fake answers on stdout rather than by storing a file.
	// Checked before the template store, whose scripts share the tar and
	// gzip words.
	if strings.Contains(exec.Line(), "tar -cf - .") {
		fmt.Fprintln(stdout, "tamp snapshot of "+strings.Join(s.stagedSites(), " "))
		return nil
	}
	if strings.Contains(exec.Line(), "-xzf -") {
		return nil
	}

	// The template and seed stores, modelled as the files they are: a save
	// puts the tarball there, a restore lays out what that save was taken
	// from, and the probe answers from what is actually in the store. Which
	// store a command means is its path, since both tar and gzip.
	if strings.Contains(exec.Line(), "gzip -1") {
		path := scriptArg(exec.Cmd, 0)
		if isSeed(path) {
			s.saveSeed(path, scriptArg(exec.Cmd, 2))
			return nil
		}
		s.put(path, "a tarred bench")
		return nil
	}
	if strings.Contains(exec.Line(), "-xzf") {
		path := scriptArg(exec.Cmd, 0)
		if isSeed(path) {
			s.stage(scriptArg(exec.Cmd, 2), s.seeds[path])
			return nil
		}
		s.initialize(b)
		return nil
	}
	if strings.Contains(exec.Line(), `test -f "$1"`) {
		if !s.has(scriptArg(exec.Cmd, 0)) {
			return &engine.ExitError{Container: exec.Container, Cmd: exec.Cmd, Status: 1}
		}
		return nil
	}

	// The deps probe: the virtualenv is there unless a deps clean took it.
	if strings.Contains(exec.Line(), `test -x "$1"`) {
		if b.depsWiped {
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
		delete(b.siteApps, host)
		s.drop(frappe.SiteConfigPath(host))
	// A backup is what a snapshot carries: the site's apps, kept where the
	// bench's own state cannot take them with it.
	case strings.Contains(exec.Line(), "backup --with-files"):
		host := siteArg(exec.Cmd, "backup")
		s.stage(host, b.siteApps[host])
	case strings.Contains(exec.Line(), `"$1" restore`):
		host := scriptArg(exec.Cmd, 0)
		s.addSite(b, host)
		if b.siteApps == nil {
			b.siteApps = map[string][]string{}
		}
		b.siteApps[host] = slices.Clone(s.staged[host])
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
	case strings.Contains(exec.Line(), "-name node_modules"):
		b.depsWiped = true
	case strings.Contains(exec.Line(), "setup requirements"):
		b.depsWiped = false
	case strings.Contains(exec.Line(), "cd "+frappe.AppsDir):
		for _, app := range b.appsSorted() {
			fmt.Fprintln(stdout, app)
		}
	}
	return nil
}

// remoteRefusal is git meeting the scripted repository tables. The words on
// stderr are github.com's, verified, because that is what tamp classifies.
func (s *benchSim) remoteRefusal(exec Exec, source string, stderr io.Writer) error {
	refused := &engine.ExitError{Container: exec.Container, Cmd: exec.Cmd, Status: 128}
	if s.missing()[source] {
		fmt.Fprintf(stderr, "fatal: repository '%s' not found\n", source)
		return refused
	}
	opens, private := s.private()[source]
	accepted, denied := s.denied()[source]
	if !private && !denied {
		return nil
	}
	presented, ok := presentedPassword(exec.Env)
	switch {
	case !ok:
		fmt.Fprintf(stderr, "fatal: could not read Username for '%s': terminal prompts disabled\n", source)
	case private && presented == opens:
		return nil
	case denied && presented == accepted:
		fmt.Fprintf(stderr, "remote: Repository not found.\nfatal: repository '%s/' not found\n", source)
	default:
		fmt.Fprintf(stderr, "remote: Invalid username or password.\nfatal: Authentication failed for '%s/'\n", source)
	}
	return refused
}

// presentedPassword is the credential the bridge injected into this exec, if
// any — the fake gates on the same contract the container's helper reads.
func presentedPassword(env []string) (string, bool) {
	for _, kv := range env {
		if password, ok := strings.CutPrefix(kv, frappe.CredentialPasswordVar+"="); ok && password != "" {
			return password, true
		}
	}
	return "", false
}

// isSeed tells the two stores apart by where the tarball lives.
func isSeed(path string) bool { return strings.HasPrefix(path, frappe.SeedDir+"/") }

// stage records what the staging area holds for a site: the app list a
// restore from it brings back.
func (s *benchSim) stage(host string, apps []string) {
	if host == "" {
		return
	}
	if s.staged == nil {
		s.staged = map[string][]string{}
	}
	s.staged[host] = slices.Clone(apps)
}

// saveSeed stores what the staged site holds, so restoring the seed later
// gives a new site the same apps.
func (s *benchSim) saveSeed(path, host string) {
	if s.seeds == nil {
		s.seeds = map[string][]string{}
	}
	s.seeds[path] = slices.Clone(s.staged[host])
	s.put(path, "a tarred site backup")
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

// stagedSites names what a snapshot bundle would carry, sorted.
func (s *benchSim) stagedSites() []string { return slices.Sorted(maps.Keys(s.staged)) }

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
