package frappe

import (
	"slices"
	"testing"
)

// bench has printed its app list two ways across releases, and tamp's site
// listing has to survive being pointed at either.
func TestTheAppListIsReadTheSameWayFromEitherOfBenchsFormats(t *testing.T) {
	for why, out := range map[string]string{
		"bare names":              "frappe\nerpnext\n",
		"names with versions":     "frappe 15.0.0 version-15\nerpnext 15.0.0 version-15\n",
		"blank lines around them": "\nfrappe\n\nerpnext\n\n",
		"no trailing newline":     "frappe\nerpnext",
	} {
		t.Run(why, func(t *testing.T) {
			if got := parseApps(out); !slices.Equal(got, []string{"frappe", "erpnext"}) {
				t.Errorf("parseApps(%q) = %v", out, got)
			}
		})
	}
}

// A site tamp cannot ask, or a bench with nothing to say, is an empty list
// rather than one app named "".
func TestNoAppsIsNoApps(t *testing.T) {
	if got := parseApps("\n  \n"); len(got) != 0 {
		t.Errorf("parseApps = %v, want nothing", got)
	}
}

// Where bench puts a dropped site. tamp prints this path, so a wrong one
// sends someone looking for their files in a directory that does not exist —
// which is what happened before a real drop-site was watched.
func TestTheArchivePathIsWhereBenchActuallyMovesADroppedSite(t *testing.T) {
	if got := ArchivedSitesDir; got != BenchDir+"/archived/sites" {
		t.Errorf("ArchivedSitesDir = %s, and bench moves a dropped site to <bench>/archived/sites", got)
	}
}
