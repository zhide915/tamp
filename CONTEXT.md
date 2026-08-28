# tamp

Glossary for tamp, an environment manager for Frappe Framework. Canonical terms for CLI output, docs, and code.

## Language

**Environment**:
tamp's unit of management — one directory (marked by `tamp.toml`) containing exactly one bench and its containers. One environment = one bench, forever (locked 2026-08-27).
_Avoid_: bench (as a synonym), project, instance

**Bench**:
The Frappe-side workspace an environment contains (apps, sites, Procfile). Only used when speaking about the Frappe layer itself, never as the tamp unit.

**Site**:
A Frappe site on a bench, named exactly as its hostname. One site = one database inside the environment's MariaDB.

**Layer**:
One of the three independently managed storage areas of an environment: source (the bench's apps, wherever they live — the host `apps/` folder in synced and bind modes, the code volume when sync is off), deps, and data (every site's database and files, built assets included). tamp never deletes source.
_Avoid_: assets (as a layer)

**Router**:
The single global Caddy container routing all sites and mail UIs by Host header. There is exactly one per machine.
_Avoid_: proxy, gateway

**Registry**:
The global machine-level index (`~/.tamp/registry.json`) mapping environment names to paths; names are unique here.

**Snapshot**:
A user-triggered backup of an environment's data layer (all sites, with files), stored inside the environment directory. Protection, not caching.

**Seed**:
A cached pristine site backup per (Frappe version × app set), used to make `site new --seed` fast. Caching, not protection.
_Avoid_: snapshot (for this)

**Template**:
A cached tarball of a freshly initialized bench per Frappe version, used to make `create` fast. Subject to a staleness TTL.

**Sync session**:
The Mutagen two-way sync between an environment's host `apps/` folder and its container. On Linux there is none — bind mount instead — and that is a mode, not an error.

**Toolchain**:
The pinned Python/Node/MariaDB set matched to a Frappe version via tamp's version matrix, provisioned into the shared `tamp-toolchain` volume.
