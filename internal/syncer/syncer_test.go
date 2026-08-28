package syncer_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/zhide915/tamp/internal/syncer"
)

func TestAutoIsABindMountOnLinuxAndASessionEverywhereElse(t *testing.T) {
	cases := map[string]syncer.Effective{
		"linux":   syncer.UseBind,
		"windows": syncer.UseMutagen,
		"darwin":  syncer.UseMutagen,
	}
	for goos, want := range cases {
		t.Run(goos, func(t *testing.T) {
			if got := syncer.Resolve(syncer.ModeAuto, goos); got != want {
				t.Errorf("auto on %s = %s, want %s", goos, got, want)
			}
		})
	}
}

func TestAModeThatIsNotAutoIsTakenAtItsWord(t *testing.T) {
	for _, mode := range []syncer.Mode{syncer.ModeMutagen, syncer.ModeBind, syncer.ModeOff} {
		for _, goos := range []string{"linux", "windows", "darwin"} {
			if got := syncer.Resolve(mode, goos); string(got) != string(mode) {
				t.Errorf("%s on %s = %s, want %s", mode, goos, got, mode)
			}
		}
	}
}

func TestParseModeNamesTheOnesItAccepts(t *testing.T) {
	if _, err := syncer.ParseMode("rsync"); err == nil {
		t.Fatal(`ParseMode("rsync") = nil, want an error`)
	} else if !strings.Contains(err.Error(), "mutagen") {
		t.Errorf("the error does not list the modes tamp has: %v", err)
	}
	if _, err := syncer.ParseMode(""); err == nil {
		t.Error(`ParseMode("") = nil, want an error`)
	}
	for _, mode := range syncer.Modes {
		if _, err := syncer.ParseMode(string(mode)); err != nil {
			t.Errorf("ParseMode(%q) = %v", mode, err)
		}
	}
}

func TestPathWarningsCatchTheTwoPlacesSyncGoesWrong(t *testing.T) {
	cases := map[string]string{
		filepath.Join("C:", "Users", "sam", "OneDrive", "work", "erp15"): "OneDrive",
		filepath.Join("/home", "sam", "Dropbox", "erp15"):                "Dropbox",
		filepath.Join("C:", "My Projects", "erp15"):                      "space",
	}
	for dir, want := range cases {
		t.Run(want, func(t *testing.T) {
			warnings := syncer.PathWarnings(dir)
			if len(warnings) == 0 {
				t.Fatalf("PathWarnings(%q) said nothing", dir)
			}
			if !strings.Contains(strings.Join(warnings, "\n"), want) {
				t.Errorf("PathWarnings(%q) = %v, want one mentioning %q", dir, warnings, want)
			}
		})
	}

	if warnings := syncer.PathWarnings(filepath.Join("/home", "sam", "code", "erp15")); len(warnings) != 0 {
		t.Errorf("PathWarnings warned about an ordinary path: %v", warnings)
	}
}

// Syncing .git is what lets git run on the host.
func TestGitIsSyncedAndBuildOutputIsNot(t *testing.T) {
	for _, want := range []string{"env/", "node_modules/", "__pycache__/", "*.pyc"} {
		if !slices.Contains(syncer.Ignores, want) {
			t.Errorf("%s is synced, and it is the container's to regenerate", want)
		}
	}
	for _, ignore := range syncer.Ignores {
		if strings.Contains(ignore, ".git") {
			t.Errorf("%s is ignored, so git would not work on the host", ignore)
		}
	}
}
