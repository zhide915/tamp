// Package frappe holds the bench scripts tamp runs inside the frappe
// container: where the bench lives, the commands that build it, and the files
// tamp owns on it.
package frappe

import (
	"encoding/json"
	"fmt"
	"maps"
)

// The bench lives in the bench volume, with apps/ synced from the host.
const (
	Home     = "/home/frappe"
	BenchDir = Home + "/frappe-bench"
	AppsDir  = BenchDir + "/apps"
	SitesDir = BenchDir + "/sites"
	// CacheDir holds the pip and yarn caches, in the volume every
	// environment shares.
	CacheDir = Home + "/.cache"

	// Procfile is tamp's: the frappe container idles until it exists, then
	// runs honcho on it.
	Procfile = BenchDir + "/Procfile"
	// CommonSiteConfig holds the bench settings every site inherits.
	CommonSiteConfig = SitesDir + "/common_site_config.json"
	// Python is the matrix's Python, as the image places it.
	Python = Home + "/.tamp/bin/python"
)

// In-container ports. Nothing is published, so nothing collides.
const (
	WebPort         = 8000
	SocketIOPort    = 9000
	FileWatcherPort = 6787
	redisPort       = 6379
)

// Passwords are fixed: a development bench keeps no secrets.
const (
	DBRootPassword = "password"
	AdminPassword  = "password"
)

// Init builds the bench around the Frappe clone in apps/: the piped answers
// keep the folder and install it, and a rollback prompt reads none and aborts.
func Init(branch string) []string {
	return []string{"bash", "-c", `printf 'n\ny\n' | bench init` +
		` --ignore-exist --no-procfile --skip-redis-config-generation --no-backups` +
		` --frappe-path "$1" --frappe-branch "$2" --python "$3" "$4"`,
		"tamp", AppsDir + "/frappe", branch, Python, BenchDir}
}

// NewSite creates a site. The login scope lets the bench reach MariaDB from
// another container.
func NewSite(host string) []string {
	return []string{"bench", "new-site", host,
		"--mariadb-user-host-login-scope=%",
		"--db-root-password", DBRootPassword,
		"--admin-password", AdminPassword}
}

// DropSite drops a site and its database. Force drops one that bench
// new-site never finished.
func DropSite(host string) []string {
	return []string{"bench", "drop-site", host,
		"--db-root-password", DBRootPassword, "--no-backup", "--force"}
}

// ListSites prints the name of each site in the bench, one per line.
func ListSites() []string {
	return []string{"bash", "-c",
		`cd "$1" && for f in */site_config.json; do if [ -e "$f" ]; then echo "${f%/site_config.json}"; fi; done`,
		"tamp", SitesDir}
}

// DeveloperMode turns on developer mode in a site's own config.
func DeveloperMode(host string) []string {
	return []string{"bench", "--site", host, "set-config", "-p", "developer_mode", "1"}
}

// SiteConfig is a site's own config. bench new-site writes it first, so a
// site can have one before it is installed.
func SiteConfig(host string) string { return SitesDir + "/" + host + "/site_config.json" }

// SiteInstalled is tamp's marker that bench new-site finished for a site.
func SiteInstalled(host string) string { return SitesDir + "/" + host + "/.tamp-installed" }

// DeveloperModeOn reports whether a site config turns developer mode on.
func DeveloperModeOn(siteConfig []byte) (bool, error) {
	var config struct {
		DeveloperMode any `json:"developer_mode"`
	}
	if err := json.Unmarshal(siteConfig, &config); err != nil {
		return false, err
	}
	return config.DeveloperMode == float64(1), nil
}

// ProcfileText is the bench's processes, without Redis, which has containers
// of its own, and without DEV_SERVER, as ADR 0004 records.
const ProcfileText = `web: bench serve --port 8000
socketio: node apps/frappe/socketio.js
watch: bench watch
schedule: bench schedule
worker: bench worker
`

// Settings are the bench settings tamp owns, keyed as Frappe reads them.
// dbHost, redisCache and redisQueue are the services' hostnames.
func Settings(dbHost, redisCache, redisQueue string) map[string]any {
	return map[string]any{
		"db_host":        dbHost,
		"redis_cache":    redisURL(redisCache),
		"redis_queue":    redisURL(redisQueue),
		"redis_socketio": redisURL(redisQueue),

		"webserver_port":    WebPort,
		"socketio_port":     SocketIOPort,
		"file_watcher_port": FileWatcherPort,
		// The socket.io server calls the web server here, inside the container.
		"webserver_host": "127.0.0.1",
	}
}

// MergeSettings sets tamp's keys in a common_site_config.json and keeps every
// other key. Empty existing means there is no file yet.
func MergeSettings(existing []byte, ours map[string]any) ([]byte, error) {
	config := map[string]any{}
	if len(existing) > 0 {
		if err := json.Unmarshal(existing, &config); err != nil {
			return nil, fmt.Errorf("%s is not valid JSON: %w", CommonSiteConfig, err)
		}
	}
	// A file holding JSON null decodes to a nil map.
	if config == nil {
		config = map[string]any{}
	}
	maps.Copy(config, ours)
	body, err := json.MarshalIndent(config, "", " ")
	if err != nil {
		return nil, err
	}
	return append(body, '\n'), nil
}

func redisURL(host string) string { return fmt.Sprintf("redis://%s:%d", host, redisPort) }
