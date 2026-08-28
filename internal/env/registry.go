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
	// Path is the absolute environment directory. It is what makes
	// `tamp start erp15` work from anywhere.
	Path string `json:"path"`
	// Hash is the path hash baked into the environment's Docker resources,
	// stored so tamp can name them without re-deriving from a path that may
	// since have moved.
	Hash string `json:"hash"`
	// DBPort is the host port this environment's MariaDB publishes.
	//
	// tamp.toml is where the environment records its own port, and where the
	// compose generator reads it. This is the allocator's ledger, and it is
	// deliberately not the same thing: allocation has to be decided from data
	// that is already written when the machine lock is released, and an
	// environment's tamp.toml is not.
	DBPort int `json:"db_port"`
	// Sites caches the environment's site hostnames. It exists so the router
	// and the hosts block can be assembled while an environment is stopped —
	// the bench's sites/ directory is the authority whenever it is running.
	Sites []string `json:"sites"`
}

// Registry is the whole of ~/.tamp/registry.json.
//
// Names are unique in it: it is keyed by name, and `tamp start erp15`
// has to be unambiguous from any directory on the machine.
type Registry map[string]Entry

// RegistryPath is the registry inside a tamp home directory.
func RegistryPath(home string) string { return filepath.Join(home, RegistryFile) }

// LoadRegistry reads the registry. A machine that has never run tamp create
// has none, which is an empty registry rather than an error.
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

// Names lists the registered environments in the order a user reads them.
func (r Registry) Names() []string {
	names := make([]string, 0, len(r))
	for name := range r {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// save writes the registry, replacing the old file only once the new one is
// complete on disk: an interrupted write must not leave the machine's index of
// every environment half-written.
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
	// Expected to fail on the happy path, where the rename below has already
	// taken the temp file away; it is here for the paths that do not get there.
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

// The registry is the machine's claims ledger. Everything unique per machine
// is claimed here, in one pass under the machine lock: environment names,
// hostnames — a site's and every mail UI's, because the router matches one
// Host header against all of them at once, and a hostname held twice is a
// configuration Caddy refuses to load, taking every site on the machine down
// with it — and the host ports databases publish on.

// A HostClaim names what already answers to a hostname: the owning
// environment, and whether the hostname is one of its sites or its mail UI.
type HostClaim struct {
	Host  string
	Owner string
	What  string
}

// Claim registers a new environment: the name, its mail hostname, and a
// fresh database port, decided together so that both facts are on disk when
// the lock releases — a second create can neither see the name free nor
// reuse the port.
func Claim(home string, name Name, dir, hash string) (int, error) {
	var port int
	err := UpdateRegistry(home, func(reg Registry) error {
		if existing, taken := reg[string(name)]; taken {
			return exitcode.New(exitcode.CodeFailed,
				fmt.Sprintf("an environment named %q is already registered, at %s", name, existing.Path),
				"pick another name, or remove the old one with 'tamp rm "+string(name)+"'")
		}
		// The name decides a hostname too — the mail UI's — and the router
		// would refuse a configuration holding that address twice.
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

// Reclaim registers an environment being adopted back in place: same name,
// same path, cached site list kept.
//
// The port it used to publish is taken again when nothing else has claimed
// it, so a database client's saved connection still works. When something
// has, a new one is allocated and returned — the alternative is two
// environments publishing one port, of which only the first would start.
func Reclaim(home string, name Name, dir, hash string, recorded int) (int, error) {
	var port int
	err := UpdateRegistry(home, func(reg Registry) error {
		if existing, taken := reg[string(name)]; taken && !samePath(existing.Path, dir) {
			return exitcode.New(exitcode.CodeFailed,
				fmt.Sprintf("an environment named %q is already registered, at %s", name, existing.Path),
				"remove that one with 'tamp rm "+string(name)+"', or rename this directory's environment in "+ConfigFile)
		}
		// The environment's own mail hostname always looks claimed by the
		// environment itself, which is not a clash — only another environment
		// having taken it as a site is.
		if c, clash := claimant(reg, string(name), router.MailHost(string(name))); clash && c.Owner != string(name) {
			return exitcode.New(exitcode.CodeFailed,
				fmt.Sprintf("%s is already %s of %q", router.MailHost(string(name)), c.What, c.Owner),
				"rename this directory's environment in "+ConfigFile)
		}

		// The recorded port is this environment's to take back, and the
		// registry is the only thing that can say otherwise. Whether something
		// is listening on it right now is not the question: an environment
		// being adopted in place is quite likely to be listening on it itself.
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
// machine has already given out — to any environment, as a site or a mail UI.
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

// ReleaseHost takes a hostname back out of the ledger — because the site it
// was claimed for could not be made, or has just been dropped.
func ReleaseHost(home string, name Name, host string) error {
	return updateSites(home, name, func(_ Registry, sites []string) ([]string, error) {
		return slices.DeleteFunc(sites, func(s string) bool { return s == host }), nil
	})
}

// RecordSites replaces an environment's cached site list with what its bench
// actually holds, keeping out any hostname the machine has already given to
// something else — tamp did not create every site on a bench. The refused
// claims are returned for the caller to report: routing one would put the
// address in the Caddyfile twice.
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

// updateSites rewrites one environment's cached site list under the machine
// lock, keeping it sorted.
//
// Every change to that list goes through here, because the list is what the
// router's routes are assembled from: reading it, deciding, and writing it
// back is a read-modify-write cycle, and doing it anywhere else is how a
// concurrent tamp loses a route.
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

// claimant reports what on this machine already answers to a hostname,
// ignoring self's own sites.
//
// Both kinds of route count. An environment's mail UI is as much a claim on a
// hostname as a site is, and a site created at mail.demo.localhost would be a
// second block for an address the router already has — which is why a mail
// hostname is a clash even for the environment that owns it.
//
// self is empty when nothing may hold the hostname yet, which is the question
// a fresh claim asks. It names an environment when the question is instead
// "may this environment go on holding it", as it is when tamp reconciles a
// bench's own site list against the ledger.
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

// portClaimedBy reports whether an environment other than self holds a host
// port.
func portClaimedBy(reg Registry, self string, port int) bool {
	for name, entry := range reg {
		if name != self && entry.DBPort == port {
			return true
		}
	}
	return false
}

// sitesOf is the site list tamp last recorded for an environment, which a
// reclaim keeps: the bench is about to be asked anyway, and until it answers
// this is what its routes are assembled from.
func sitesOf(reg Registry, name string) []string {
	if entry, ok := reg[name]; ok {
		return entry.Sites
	}
	return []string{}
}

// UpdateRegistry runs change against the registry under the machine-global
// lock, and writes the result back only if change succeeded.
//
// Every mutation goes through here. Reading the registry, deciding, and
// writing it back is a read-modify-write cycle, and doing it outside the lock
// is exactly how a concurrent tamp loses an environment.
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
