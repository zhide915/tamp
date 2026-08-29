package env

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/router"
)

// RegistryFile indexes every environment on the machine, keyed by name.
const RegistryFile = "registry.json"

// Entry is one environment's row in the global registry.
type Entry struct {
	// Path is what makes `tamp start <name>` work from anywhere.
	Path string `json:"path"`
	// Hash is the path hash baked into the Docker resource names, stored so
	// they can be derived even after the directory moves.
	Hash string `json:"hash"`
	// DBPort is the allocator's ledger — deliberately not tamp.toml, which is
	// written only after the machine lock releases; allocation must read data
	// already on disk under the lock.
	DBPort int `json:"db_port"`
	// Sites caches the environment's hostnames so routes can be assembled
	// while it is stopped; the running bench is the authority.
	Sites []string `json:"sites"`
}

// Registry is ~/.tamp/registry.json. Keyed by name: `tamp start <name>` must
// be unambiguous from anywhere.
type Registry map[string]Entry

func RegistryPath(home string) string { return filepath.Join(home, RegistryFile) }

// LoadRegistry treats a missing file as an empty registry.
func LoadRegistry(home string) (Registry, error) {
	blob, err := os.ReadFile(RegistryPath(home))
	if errors.Is(err, fs.ErrNotExist) {
		return Registry{}, nil
	}
	if err != nil {
		return nil, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot read %s: %v", RegistryPath(home), err),
			"check the permissions on your ~/.tamp directory")
	}

	reg := Registry{}
	if err := json.Unmarshal(blob, &reg); err != nil {
		return nil, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("%s is not valid JSON: %v", RegistryPath(home), err),
			"repair or delete it — tamp rebuilds it as environments are created")
	}
	return reg, nil
}

// Names lists the registered environments, sorted.
func (r Registry) Names() []string {
	names := make([]string, 0, len(r))
	for name := range r {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// save writes via temp file and rename, so an interrupted write cannot leave
// the machine's index half-written.
func (r Registry) save(home string) error {
	blob, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot render the registry: %v", err), "")
	}
	blob = append(blob, '\n')

	path := RegistryPath(home)
	tmp, err := os.CreateTemp(home, RegistryFile+".*")
	if err != nil {
		return registryWriteError(path, err)
	}
	// A no-op on the happy path, where the rename already consumed the file.
	defer func() { _ = os.Remove(tmp.Name()) }()

	if _, err := tmp.Write(blob); err != nil {
		_ = tmp.Close()
		return registryWriteError(path, err)
	}
	if err := tmp.Close(); err != nil {
		return registryWriteError(path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return registryWriteError(path, err)
	}
	return nil
}

func registryWriteError(path string, err error) error {
	return exitcode.New(exitcode.CodeFailed,
		fmt.Sprintf("cannot write %s: %v", path, err),
		"check the permissions on your ~/.tamp directory")
}

// The registry is the machine's claims ledger: names, hostnames (sites and
// mail UIs alike — Caddy refuses a configuration holding one address twice,
// taking every site down) and DB ports are all claimed here under the lock.

// A HostClaim names what already answers to a hostname.
type HostClaim struct {
	Host  string
	Owner string
	What  string
}

// Claim registers a new environment: name, mail hostname and a fresh DB port
// decided together, so both facts are on disk when the lock releases and a
// second create can neither see the name free nor reuse the port.
func Claim(home string, name Name, dir, hash string) (int, error) {
	var port int
	err := UpdateRegistry(home, func(reg Registry) error {
		if existing, taken := reg[string(name)]; taken {
			return exitcode.New(exitcode.CodeFailed,
				fmt.Sprintf("an environment named %q is already registered, at %s", name, existing.Path),
				"pick another name, or remove the old one with 'tamp rm "+string(name)+"'")
		}
		mailHost := router.MailHost(string(name))
		if c, clash := claimant(reg, "", mailHost); clash {
			return exitcode.New(exitcode.CodeFailed,
				fmt.Sprintf("an environment named %q would take the hostname %s, which is already %s of %q",
					name, mailHost, c.What, c.Owner),
				"pick another name, or remove that site with 'tamp site rm "+c.Owner+" "+mailHost+" --yes'")
		}
		var err error
		if port, err = AllocateDBPort(reg); err != nil {
			return err
		}
		reg[string(name)] = Entry{Path: dir, Hash: hash, DBPort: port, Sites: []string{}}
		return nil
	})
	return port, err
}

// Reclaim re-registers an environment adopted in place, keeping its cached
// site list and taking back its recorded port — so a database client's saved
// connection still works — unless another environment claimed the port
// meanwhile, in which case a fresh one is allocated.
func Reclaim(home string, name Name, dir, hash string, recorded int) (int, error) {
	var port int
	err := UpdateRegistry(home, func(reg Registry) error {
		if existing, taken := reg[string(name)]; taken && !samePath(existing.Path, dir) {
			return exitcode.New(exitcode.CodeFailed,
				fmt.Sprintf("an environment named %q is already registered, at %s", name, existing.Path),
				"remove that one with 'tamp rm "+string(name)+"', or rename this directory's environment in "+ConfigFile)
		}
		// The environment's own mail hostname is not a clash — only another
		// environment holding it as a site is.
		if c, clash := claimant(reg, string(name), router.MailHost(string(name))); clash && c.Owner != string(name) {
			return exitcode.New(exitcode.CodeFailed,
				fmt.Sprintf("%s is already %s of %q", router.MailHost(string(name)), c.What, c.Owner),
				"rename this directory's environment in "+ConfigFile)
		}

		// The registry alone decides: the environment being adopted may well
		// be listening on its own recorded port right now, so a liveness check
		// would be wrong.
		port = recorded
		if portClaimedBy(reg, string(name), port) {
			var err error
			if port, err = AllocateDBPort(reg); err != nil {
				return err
			}
		}
		reg[string(name)] = Entry{Path: dir, Hash: hash, DBPort: port, Sites: sitesOf(reg, string(name))}
		return nil
	})
	return port, err
}

// Release takes an environment's entry out of the registry.
func Release(home string, name Name) error {
	return UpdateRegistry(home, func(reg Registry) error {
		delete(reg, string(name))
		return nil
	})
}

// ClaimHost records a hostname against an environment, refusing one the
// machine has already given out — as a site or a mail UI, to anyone.
func ClaimHost(home string, name Name, host string) error {
	return updateSites(home, name, func(reg Registry, sites []string) ([]string, error) {
		if c, claimed := claimant(reg, "", host); claimed {
			return nil, exitcode.New(exitcode.CodeFailed,
				fmt.Sprintf("%s is already %s of the environment %q", host, c.What, c.Owner),
				"pick another hostname — 'tamp list' shows what every environment answers to")
		}
		return append(sites, host), nil
	})
}

// ReleaseHost takes a hostname back out of the ledger.
func ReleaseHost(home string, name Name, host string) error {
	return updateSites(home, name, func(_ Registry, sites []string) ([]string, error) {
		return slices.DeleteFunc(sites, func(s string) bool { return s == host }), nil
	})
}

// RecordSites replaces the cached site list with what the bench holds,
// keeping out hostnames the machine already gave to something else — tamp did
// not create every site on a bench, and routing one would duplicate a
// Caddyfile address. Refused claims are returned for the caller to report.
func RecordSites(home string, name Name, hosts []string) ([]HostClaim, error) {
	var skipped []HostClaim
	err := updateSites(home, name, func(reg Registry, _ []string) ([]string, error) {
		kept := make([]string, 0, len(hosts))
		for _, host := range hosts {
			if c, claimed := claimant(reg, string(name), host); claimed {
				skipped = append(skipped, c)
				continue
			}
			kept = append(kept, host)
		}
		return kept, nil
	})
	return skipped, err
}

// HostConflicts reports which of these hostnames the machine has already
// given to something other than self. A restore asks before it touches
// anything: recreating a site whose hostname now belongs elsewhere would put
// one address in the Caddyfile twice and take every route down.
func HostConflicts(home string, self Name, wanted []string) ([]HostClaim, error) {
	reg, err := LoadRegistry(home)
	if err != nil {
		return nil, err
	}
	var clashes []HostClaim
	for _, host := range wanted {
		if c, claimed := claimant(reg, string(self), host); claimed {
			clashes = append(clashes, c)
		}
	}
	return clashes, nil
}

// updateSites is the single path for changing a site list: a read-modify-
// write under the machine lock, since the list is what routes are assembled
// from.
func updateSites(home string, name Name, change func(Registry, []string) ([]string, error)) error {
	return UpdateRegistry(home, func(reg Registry) error {
		entry := reg[string(name)]
		sites, err := change(reg, entry.Sites)
		if err != nil {
			return err
		}
		slices.Sort(sites)
		entry.Sites = sites
		reg[string(name)] = entry
		return nil
	})
}

// claimant reports what already answers to a hostname, ignoring self's own
// sites. Mail UIs count as claims — even against their own environment, since
// a site at that address would be a second router block for it. self is empty
// when the question is "is this free at all".
func claimant(reg Registry, self, host string) (HostClaim, bool) {
	for _, name := range reg.Names() {
		if router.MailHost(name) == host {
			return HostClaim{Host: host, Owner: name, What: "the mail UI"}, true
		}
		if name != self && slices.Contains(reg[name].Sites, host) {
			return HostClaim{Host: host, Owner: name, What: "a site"}, true
		}
	}
	return HostClaim{}, false
}

func portClaimedBy(reg Registry, self string, port int) bool {
	for name, entry := range reg {
		if name != self && entry.DBPort == port {
			return true
		}
	}
	return false
}

// sitesOf is the last-recorded site list, which a reclaim keeps until the
// bench answers.
func sitesOf(reg Registry, name string) []string {
	if entry, ok := reg[name]; ok {
		return entry.Sites
	}
	return []string{}
}

// UpdateRegistry is the single mutation path: read-modify-write under the
// machine lock, written back only if change succeeded.
func UpdateRegistry(home string, change func(Registry) error) error {
	lock, err := AcquireLock(home)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()

	reg, err := LoadRegistry(home)
	if err != nil {
		return err
	}
	if err := change(reg); err != nil {
		return err
	}
	return reg.save(home)
}
