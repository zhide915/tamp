package env

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/zhide915/tamp/internal/exitcode"
)

// Open hands a site's URL to the default browser: host, or the only site
// the recipe lists. With several sites and no host it guesses none.
func (m *Manager) Open(host string) error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	r, err := readRecipe(dir)
	if err != nil {
		return err
	}
	name := filepath.Base(dir)
	var hosts []string
	for _, s := range r.Sites {
		hosts = append(hosts, s.Host)
	}
	switch {
	case host != "":
		if !slices.Contains(hosts, host) {
			return exitcode.New(exitcode.CodeNotFound,
				fmt.Sprintf("%s has no site %s", name, host), siteChoice(hosts))
		}
	case len(hosts) == 0:
		return exitcode.New(exitcode.CodeFailed, name+" has no sites",
			"add one with `tamp site add <host>`")
	case len(hosts) > 1:
		return exitcode.Usage(fmt.Sprintf("%s has several sites: %s", name, strings.Join(hosts, ", ")),
			"run `tamp open <host>` with one of them")
	default:
		host = hosts[0]
	}
	url := "http://" + host
	if err := m.browser(context.Background(), url); err != nil {
		return err
	}
	m.p.OK("opened " + url)
	return nil
}

func siteChoice(hosts []string) string {
	if len(hosts) == 0 {
		return "add it with `tamp site add <host>`"
	}
	return "run `tamp open` with one of: " + strings.Join(hosts, ", ")
}
