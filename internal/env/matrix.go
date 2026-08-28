package env

import (
	"fmt"
	"strings"

	"github.com/zhide915/tamp/internal/exitcode"
)

// Image pins. tamp hosts no images of its own, so these four lines are
// the entire supply chain: change one deliberately, with the e2e suite green,
// and never let a generated compose file say "latest".
const (
	// BenchImage is the official frappe/bench image tamp runs the frappe
	// service from. Verified against Docker Hub on 2026-08-27.
	BenchImage = "frappe/bench:v5.31.0"
	// RedisImage backs both the cache and the queue services.
	RedisImage = "redis:7-alpine"
	// MailpitImage catches every mail the environment sends.
	MailpitImage = "axllent/mailpit:v1.31"
)

// MariaDBImage is per-environment: one bench, one MariaDB server, at the
// version its Frappe release expects.
func MariaDBImage(version string) string { return "mariadb:" + version }

// Toolchain is the pinned language and database set a Frappe version needs.
// tamp resolves it so the user never has to.
type Toolchain struct {
	Python  string `toml:"python"`
	Node    string `toml:"node"`
	MariaDB string `toml:"mariadb"`
}

// FrappeVersion is a supported value of the --frappe flag.
type FrappeVersion string

const (
	Version15 FrappeVersion = "version-15"
	Version16 FrappeVersion = "version-16"
	Develop   FrappeVersion = "develop"

	// DefaultFrappeVersion is what `tamp create` picks when told nothing.
	DefaultFrappeVersion = Version15
)

// matrix is tamp's version matrix. Adding or bumping a Frappe version is a
// change to this table and nothing else.
//
// version-14 is deliberately absent rather than commented out: ParseFrappeVersion
// rejects every unknown version by naming this table's keys, so a version tamp
// does not support gets one honest answer instead of two.
var matrix = map[FrappeVersion]Toolchain{
	Version15: {Python: "3.11", Node: "18", MariaDB: "10.11"},
	Version16: {Python: "3.14", Node: "24", MariaDB: "11.8"},
	// develop tracks v16's floors until it moves past them.
	Develop: {Python: "3.14", Node: "24", MariaDB: "11.8"},
}

// supportedVersions lists the matrix keys in the order a user thinks of them,
// which a map cannot do.
var supportedVersions = []FrappeVersion{Version15, Version16, Develop}

// ParseFrappeVersion validates a --frappe value and returns its toolchain.
func ParseFrappeVersion(s string) (FrappeVersion, Toolchain, error) {
	v := FrappeVersion(s)
	tc, ok := matrix[v]
	if !ok {
		return "", Toolchain{}, exitcode.New(exitcode.CodeFailed,
			fmt.Sprintf("Frappe %s is not supported", s),
			"supported versions: "+strings.Join(versionNames(), ", "))
	}
	return v, tc, nil
}

// ToolchainFor reports the pinned toolchain for an already-validated version.
// A version that reached tamp.toml has been through ParseFrappeVersion, so a
// miss here means the file was hand-edited.
func ToolchainFor(v FrappeVersion) (Toolchain, bool) {
	tc, ok := matrix[v]
	return tc, ok
}

func versionNames() []string {
	names := make([]string, len(supportedVersions))
	for i, v := range supportedVersions {
		names[i] = string(v)
	}
	return names
}
