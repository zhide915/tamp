package env

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/zhide915/tamp/internal/exitcode"
	"github.com/zhide915/tamp/internal/frappe"
)

// seedSchema versions the manifest. A manifest tamp cannot read is a seed it
// cannot vouch for, so it is taken again rather than reinterpreted — the
// template store's rule, because a seed is caching too.
const seedSchema = 1

// seedManifest is what tamp records beside a stored seed: enough to tell
// that the backup really is this key's, whatever the digest says.
type seedManifest struct {
	Schema  int           `json:"schema"`
	Frappe  FrappeVersion `json:"frappe"`
	Apps    []string      `json:"apps"`
	Created time.Time     `json:"created"`
}

// seedKey names a seed's files. One per Frappe version and app set: those
// two decide everything a freshly created site holds, and the apps are
// digested because a filename cannot carry a list.
func seedKey(v FrappeVersion, apps []string) string {
	digest := sha256.Sum256([]byte(strings.Join(appSet(apps), "\n")))
	return string(v) + "-" + hex.EncodeToString(digest[:])[:12]
}

// appSet is the app list as the key sees it: order and repetition are how
// the user typed --apps, not what the site ends up with.
func appSet(apps []string) []string {
	set := slices.Clone(apps)
	slices.Sort(set)
	return slices.Compact(set)
}

func appSetText(apps []string) string { return strings.Join(appSet(apps), ", ") }

// seedPlan is what one site creation does about the seed store, settled
// before anything is created so --seed with no seed costs nothing.
type seedPlan struct {
	Key string
	// Restore is set when --seed found a seed to bring back.
	Restore bool
	// Store is set when this creation should leave a seed behind.
	Store bool
}

// planSeed decides between the two paths. Storing is unconditional on a
// miss, whether or not --seed was asked for: the first site of an app set is
// what fills the store for the next one.
func (m *Manager) planSeed(ctx context.Context, e *Environment, bench *frappe.Bench, apps []string, want bool) (seedPlan, error) {
	// A seed stands in for the app installs and nothing else — the site and
	// its database are made either way. With no apps there is nothing for one
	// to save, so tamp neither stores nor restores it.
	if len(apps) == 0 {
		if want {
			return seedPlan{}, exitcode.New(exitcode.CodeFailed,
				"--seed has no app set to restore: --apps named none",
				"name the apps with --apps, or drop --seed — a site with no apps installs nothing anyway")
		}
		return seedPlan{}, nil
	}

	key := seedKey(e.Config.Frappe.Version, apps)
	held, err := m.seedHeld(ctx, bench, key, e.Config.Frappe.Version, apps)
	if err != nil {
		return seedPlan{}, err
	}
	if want && !held {
		return seedPlan{}, exitcode.New(exitcode.CodeNotFound,
			fmt.Sprintf("this machine has no %s seed with %s", e.Config.Frappe.Version, appSetText(apps)),
			"create one site of this version and app set without --seed — tamp caches that one as the seed")
	}
	return seedPlan{Key: key, Restore: want, Store: !held}, nil
}

// steps counts what the plan adds to a site creation: one restore, or one
// install per app plus the caching of the result.
func (p seedPlan) steps(apps []string) int {
	if p.Restore {
		return 1
	}
	if p.Store {
		return len(apps) + 1
	}
	return len(apps)
}

// seedHeld reports whether the store holds a seed this environment can use.
// An unreadable manifest counts as no seed — a cache that cannot be
// understood is an empty one, and the creation that follows refills it — but
// a container tamp cannot reach is an error: answering "no seed" there would
// refuse a --seed with a sentence about the cache that is not true.
func (m *Manager) seedHeld(ctx context.Context, bench *frappe.Bench, key string, v FrappeVersion, apps []string) (bool, error) {
	held, err := bench.HasSeed(ctx, key)
	if err != nil || !held {
		return false, err
	}
	body, err := bench.ReadSeedManifest(ctx, key)
	if err != nil {
		return false, nil
	}
	var stored seedManifest
	if err := json.Unmarshal(body, &stored); err != nil {
		return false, nil
	}
	// The digest is short enough to collide, and the key format may change
	// between releases; the manifest is what actually settles the question.
	return stored.Schema == seedSchema && stored.Frappe == v &&
		slices.Equal(stored.Apps, appSet(apps)), nil
}

// restoreSeed brings a stored seed onto a site that already exists, in the
// same order a snapshot restore uses, and ends with the password this
// creation was asked for — the seed carries the one its own site had.
func (m *Manager) restoreSeed(ctx context.Context, bench *frappe.Bench, key, host, dbPassword, admin string) error {
	if err := bench.RestoreSeed(ctx, key, host); err != nil {
		return err
	}
	if err := bench.RestoreSite(ctx, host, dbPassword); err != nil {
		return err
	}
	// Absorbs whatever the apps' schemas did since the seed was taken, which
	// is why a seed needs no expiry.
	if err := bench.Migrate(ctx, host); err != nil {
		return err
	}
	if err := bench.SetAdminPassword(ctx, host, admin); err != nil {
		return err
	}
	return bench.ClearStage(ctx)
}

// storeSeed caches the site just created. Its failures are warnings: the
// site is made either way, and the only cost is that the next one pays the
// full install price too.
func (m *Manager) storeSeed(ctx context.Context, e *Environment, bench *frappe.Bench, key, host string, apps []string) {
	warn := func(err error) {
		m.Out.Warn(fmt.Sprintf("could not cache the %s seed, so the next site of this app set installs its apps too: %v", key, err))
	}

	if err := bench.ClearStage(ctx); err != nil {
		warn(err)
		return
	}
	if err := bench.StageBackup(ctx, host); err != nil {
		warn(err)
		return
	}
	// Deferred from here on: what the staging area holds now is a whole copy
	// of the site, and every path out of this function must drop it.
	defer func() {
		if err := bench.ClearStage(ctx); err != nil {
			warn(err)
		}
	}()

	if err := bench.SaveSeed(ctx, key, host); err != nil {
		warn(err)
		return
	}

	body, err := json.MarshalIndent(seedManifest{
		Schema:  seedSchema,
		Frappe:  e.Config.Frappe.Version,
		Apps:    appSet(apps),
		Created: time.Now(),
	}, "", "  ")
	if err != nil {
		warn(err)
		return
	}
	// After the tarball: a manifest must never describe a seed that is not
	// there.
	if err := bench.WriteSeedManifest(ctx, key, append(body, '\n')); err != nil {
		warn(err)
	}
	// A staging area left behind holds a whole copy of the site.
	if err := bench.ClearStage(ctx); err != nil {
		warn(err)
	}
}
