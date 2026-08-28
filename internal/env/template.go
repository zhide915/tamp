package env

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zhide915/tamp/internal/frappe"
)

// templateSchema versions the manifest. A manifest tamp cannot read is a
// template it cannot vouch for, so it is rebuilt rather than reinterpreted.
const templateSchema = 1

// templateManifest is what tamp knows about one stored template: enough to
// decide whether it is still the right bench, and how old it is.
type templateManifest struct {
	Schema  int           `json:"schema"`
	Frappe  FrappeVersion `json:"frappe"`
	Image   string        `json:"bench_image"`
	Python  string        `json:"python"`
	Node    string        `json:"node"`
	Created time.Time     `json:"created"`
}

// templateKey names a template's files. One per Frappe version: that is what
// `bench init` clones, and the rest of a bench is added after the template.
func templateKey(v FrappeVersion) string { return string(v) }

// templateVerdict is what the create step reports about the cache.
type templateVerdict string

const (
	verdictHit     templateVerdict = "hit"
	verdictMissed  templateVerdict = "missed"
	verdictExpired templateVerdict = "expired"
	verdictStale   templateVerdict = "stale"
	verdictSkipped templateVerdict = "skipped"
)

// usable reports whether a stored template can be unpacked for this
// environment. now is passed rather than read, so the TTL is testable without
// waiting a fortnight.
func (t templateManifest) usable(want templateManifest, ttl time.Duration, now time.Time) templateVerdict {
	if t.Schema != templateSchema || t.Frappe != want.Frappe || t.Image != want.Image {
		return verdictStale
	}
	if now.Sub(t.Created) >= ttl {
		return verdictExpired
	}
	return verdictHit
}

// drifted reports whether the toolchain moved since the template was taken.
// Its virtualenv was built against those versions, so a drift costs a
// `bench setup requirements` rather than a fresh bench init.
func (t templateManifest) drifted(want templateManifest) bool {
	return t.Python != want.Python || t.Node != want.Node
}

// wantedTemplate describes the template this environment would use.
func wantedTemplate(e *Environment) templateManifest {
	return templateManifest{
		Schema: templateSchema,
		Frappe: e.Config.Frappe.Version,
		Image:  BenchImage,
		Python: e.Config.Toolchain.Python,
		Node:   e.Config.Toolchain.Node,
	}
}

// templatePolicy is what a command decided about the template store, settled
// before anything is built so a misspelled setting costs nothing.
type templatePolicy struct {
	// Use is false only for --no-cache.
	Use bool
	TTL time.Duration
}

// useCache names cachePolicy's argument for the commands that have no
// --no-cache to pass on: init and readopt always go through the store.
const useCache = true

// cachePolicy reads the machine's settings into a policy.
func (m *Manager) cachePolicy(use bool) (templatePolicy, error) {
	cfg, err := LoadGlobalConfig(m.Home)
	if err != nil {
		return templatePolicy{}, err
	}
	return templatePolicy{Use: use, TTL: cfg.TemplateTTL()}, nil
}

// materialize turns an empty bench directory into a bench, from the template
// store when it can, and reports whether it started empty — the caller's cue
// that a sync session's apps still need registering. A surviving source tree
// is already the user's bench, so it is rebuilt, never replaced.
func (m *Manager) materialize(ctx context.Context, e *Environment, bench *frappe.Bench, policy templatePolicy, log *createLog) (bool, error) {
	present, err := bench.HasApp(ctx, frappe.FrappeApp)
	if err != nil {
		return false, err
	}
	if present {
		return false, bench.Rebuild(ctx)
	}

	if !policy.Use {
		log.note(templateNote(verdictSkipped, string(e.Config.Frappe.Version)))
		return true, bench.Init(ctx)
	}
	return true, m.initFromTemplate(ctx, e, bench, policy, log)
}

// initFromTemplate unpacks a usable template, or runs bench init and stores
// the result for next time. A template is only ever speed, so every failure
// the store itself can produce costs time, never the create.
func (m *Manager) initFromTemplate(ctx context.Context, e *Environment, bench *frappe.Bench, policy templatePolicy, log *createLog) error {
	key := templateKey(e.Config.Frappe.Version)
	want := wantedTemplate(e)

	verdict, stored := m.storedTemplate(ctx, bench, key, want, policy.TTL)
	log.note(templateNote(verdict, key))

	if verdict == verdictHit {
		if err := bench.RestoreTemplate(ctx, key); err != nil {
			return err
		}
		if !stored.drifted(want) {
			return nil
		}
		log.note(fmt.Sprintf("the toolchain moved since the template was taken (python %s→%s, node %s→%s) — reinstalling requirements",
			stored.Python, want.Python, stored.Node, want.Node))
		if err := bench.SetupRequirements(ctx); err != nil {
			return err
		}
		// Stored again so the repair is paid once, not once per create — but
		// keeping the original date: the frappe checkout inside is still the
		// one that clone brought down, and the expiry is about that.
		want.Created = stored.Created
		m.storeTemplate(ctx, bench, key, want)
		return nil
	}

	if err := bench.Init(ctx); err != nil {
		return err
	}
	want.Created = time.Now()
	m.storeTemplate(ctx, bench, key, want)
	return nil
}

// storedTemplate reads the store's answer for this key. Anything unreadable
// is a miss: a cache that cannot be understood is an empty one.
func (m *Manager) storedTemplate(ctx context.Context, bench *frappe.Bench, key string, want templateManifest, ttl time.Duration) (templateVerdict, templateManifest) {
	held, err := bench.HasTemplate(ctx, key)
	if err != nil || !held {
		return verdictMissed, templateManifest{}
	}
	body, err := bench.ReadTemplateManifest(ctx, key)
	if err != nil {
		return verdictMissed, templateManifest{}
	}
	var stored templateManifest
	if err := json.Unmarshal(body, &stored); err != nil {
		return verdictMissed, templateManifest{}
	}

	return stored.usable(want, ttl, time.Now()), stored
}

// storeTemplate saves the bench in hand under the manifest given. Its failures
// are warnings: the environment is built either way, and the only cost is that
// the next create pays full price too.
func (m *Manager) storeTemplate(ctx context.Context, bench *frappe.Bench, key string, want templateManifest) {
	body, err := json.MarshalIndent(want, "", "  ")
	if err != nil {
		m.Out.Warn(fmt.Sprintf("could not record the %s template: %v", key, err))
		return
	}
	if err := bench.SaveTemplate(ctx, key); err != nil {
		m.Out.Warn(fmt.Sprintf("could not store the %s template, so the next create will build its own: %v", key, err))
		return
	}
	// After the tarball: a manifest must never describe a template that is
	// not there.
	if err := bench.WriteTemplateManifest(ctx, key, append(body, '\n')); err != nil {
		m.Out.Warn(fmt.Sprintf("could not record the %s template: %v", key, err))
	}
}

// templateNote is the one line a create says about the cache.
func templateNote(v templateVerdict, key string) string {
	switch v {
	case verdictHit:
		return "template cache hit for " + key + " — unpacking a bench instead of initializing one"
	case verdictExpired:
		return "the stored " + key + " template is past its expiry — initializing a fresh bench and re-caching it"
	case verdictStale:
		return "the stored " + key + " template was built by another tamp — initializing a fresh bench and re-caching it"
	case verdictMissed:
		return "template cache missed for " + key + " — initializing a bench and caching it for next time"
	default:
		return "template cache " + string(v) + " for " + key
	}
}
