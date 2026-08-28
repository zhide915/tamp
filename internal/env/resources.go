package env

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/zhide915/tamp/internal/exitcode"
)

// hashLength: six hex characters separate one machine's environments and keep
// container names readable.
const hashLength = 6

// Resources are the Docker names one environment owns. The name keeps them
// recognisable; the path hash keeps leftovers of a moved, unregistered or
// recreated directory from colliding with a live environment on the same
// name.
type Resources struct {
	Name Name
	// Hash is the first six hex characters of the SHA-256 of the absolute
	// path.
	Hash string
}

// NewResources works before dir exists — create names resources before it
// makes anything.
func NewResources(name Name, dir string) (Resources, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Resources{}, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot resolve %q to an absolute path: %v", dir, err),
			"run tamp from a directory that still exists")
	}
	return Resources{Name: name, Hash: pathHash(abs)}, nil
}

// pathHash canonicalises so different spellings of one directory hash the
// same — re-adoption finds volumes by name and hash. Case folds only on
// Windows, where the filesystem itself is case-insensitive.
func pathHash(abs string) string {
	canonical := filepath.ToSlash(filepath.Clean(abs))
	if runtime.GOOS == "windows" {
		canonical = strings.ToLower(canonical)
	}
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])[:hashLength]
}

// Project is the compose project name and the prefix of every resource;
// compose derives container names from it directly.
func (r Resources) Project() string {
	return fmt.Sprintf("tamp-%s-%s", r.Name, r.Hash)
}

// Network carries the project name itself: one network per environment, so a
// suffix would say nothing.
func (r Resources) Network() string { return r.Project() }

// The per-environment volumes, split so each can be treated as it deserves:
// source is never deleted, data is what backups are for, deps are disposable.
const (
	// DataVolume holds every site's database.
	DataVolume = "db"
	// CodeVolume holds the bench, including the source under apps/.
	CodeVolume = "code"
	// DepsVolume holds the Python virtualenv.
	DepsVolume = "deps"
	// SitesVolume holds site files and the bench's shared config.
	SitesVolume = "sites"
)

func (r Resources) Volume(name string) string {
	return r.Project() + "-" + name
}

// The compose services. Each container answers to its service name on the
// environment's network — how the bench is told where its database and Redis
// are.
const (
	FrappeService     = "frappe"
	MariaDBService    = "mariadb"
	RedisCacheService = "redis-cache"
	RedisQueueService = "redis-queue"
	MailpitService    = "mailpit"
)

// MailUIPort is internal to the environment's network; the router reaches it,
// no host port does.
const MailUIPort = 8025

// Container matches compose's naming: project, service, replica — always
// replica 1 here.
func (r Resources) Container(service string) string {
	return r.Project() + "-" + service + "-1"
}
