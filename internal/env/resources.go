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

// hashLength is how much of the path digest ends up in resource names. Six hex
// characters is enough to separate the handful of environments one machine
// holds, and short enough that a container name stays readable.
const hashLength = 6

// Resources are the Docker names one environment owns: the compose project,
// and through it the containers, volumes and network beneath it.
//
// Both halves of the name earn their place. The environment name is what
// the user typed and is unique in the registry, so it is what makes a name
// recognisable; the path hash is what stops resources left behind by a moved,
// unregistered or deleted-then-recreated folder from colliding with a live
// environment that happens to have taken the name back.
type Resources struct {
	Name Name
	// Hash is the first six hex characters of the SHA-256 of the environment's
	// absolute path.
	Hash string
}

// NewResources derives an environment's Docker names from its name and
// directory. The directory need not exist yet — create names the resources
// before it makes any of them.
func NewResources(name Name, dir string) (Resources, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Resources{}, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("cannot resolve %q to an absolute path: %v", dir, err),
			"run tamp from a directory that still exists")
	}
	return Resources{Name: name, Hash: pathHash(abs)}, nil
}

// pathHash digests the one spelling of a path that tamp considers canonical.
//
// The hash has to survive the user reaching the same directory by a different
// route, because re-adoption recognises a leftover environment by its
// name and path hash. Separators are normalised everywhere; case is folded
// only on Windows, where the filesystem itself treats C:\Work and c:\work as
// one directory and tamp would otherwise mint two sets of resources for it.
func pathHash(abs string) string {
	canonical := filepath.ToSlash(filepath.Clean(abs))
	if runtime.GOOS == "windows" {
		canonical = strings.ToLower(canonical)
	}
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])[:hashLength]
}

// Project is the compose project name, and the prefix of every resource in the
// environment. Compose derives container names from it directly, so the
// frappe container of project tamp-erp15-ab12cd is tamp-erp15-ab12cd-frappe-1.
func (r Resources) Project() string {
	return fmt.Sprintf("tamp-%s-%s", r.Name, r.Hash)
}

// Network is the environment's private network. It carries the project name
// itself: there is exactly one per environment, so a suffix would say nothing.
func (r Resources) Network() string { return r.Project() }

// The volumes one environment owns, split so that tamp can treat each of them
// as it deserves: the source is never deleted, the databases and site files are
// what a backup is for, and the virtualenv is thrown away and rebuilt.
const (
	// DataVolume holds every site's database.
	DataVolume = "db"
	// CodeVolume holds the bench, and with it the source layer under apps/.
	CodeVolume = "code"
	// DepsVolume holds the Python virtualenv.
	DepsVolume = "deps"
	// SitesVolume holds every site's files and the bench's shared config.
	SitesVolume = "sites"
)

// Volume names one of the environment's storage layers.
func (r Resources) Volume(layer string) string {
	return r.Project() + "-" + layer
}

// The compose services an environment is made of. They double as hostnames:
// inside the environment's network, each container answers to its service
// name, which is how the bench is told where its database and its Redis are.
const (
	FrappeService     = "frappe"
	MariaDBService    = "mariadb"
	RedisCacheService = "redis-cache"
	RedisQueueService = "redis-queue"
	MailpitService    = "mailpit"
)

// MailUIPort is where Mailpit serves its web UI inside the environment's
// network. Nothing publishes it to the host — the router is how it is reached,
// which is what keeps one more port off the machine.
const MailUIPort = 8025

// Container names one of the environment's containers the way compose names
// it: the project, the service, and the replica number — of which tamp's
// services only ever have one.
func (r Resources) Container(service string) string {
	return r.Project() + "-" + service + "-1"
}
