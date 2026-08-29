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
One of the four storage areas of an environment, and what `tamp clean` wipes one at a time: **source** (the bench's apps, wherever they live — the host `apps/` folder in synced and bind modes, the code volume when sync is off), **deps** (the virtualenv, `node_modules`, `__pycache__`), **assets** (the built JS and CSS), and **data** (every site's database, files and config). tamp never deletes source. `tamp rebuild` restores deps and assets; `tamp site new` or a snapshot restores data.
The environment's four Docker volumes (`db`, `code`, `deps`, `sites`) are a different split: assets live inside `sites`, and `code` carries source.
_Avoid_: layer (for a Docker volume)

**Router**:
The single global Caddy container routing all sites and mail UIs by Host header. There is exactly one per machine.
_Avoid_: proxy, gateway

**Registry**:
The global machine-level index (`~/.tamp/registry.json`) mapping environment names to paths; names are unique here.

**Managed block**:
The region of the operating system's hosts file tamp owns, between `# --- tamp managed block ---` and `# --- end tamp block ---`, holding every non-`.localhost` site hostname on the machine. tamp never writes outside it. Writing it is the only tamp operation that elevates.
_Avoid_: hosts file (for the block)

**Snapshot**:
A user-triggered backup of an environment's data layer (all sites, with files), stored inside the environment directory. Protection, not caching.

**Seed**:
A cached pristine site backup per (Frappe version × app set), used to make `site new --seed` fast. Caching, not protection.
_Avoid_: snapshot (for this)

**Template**:
A cached tarball of a freshly initialized bench per Frappe version, used to make `create` fast. Subject to a staleness TTL.

**Sync session**:
The Mutagen two-way sync between an environment's host `apps/` folder and its container. On Linux there is none — bind mount instead — and that is a mode, not an error.

**Credential bridge**:
The relay of the host's git credentials to a single fetch inside a container: read from the host's credential system at use time, never stored.
_Avoid_: token, credential store

**Toolchain**:
The pinned Python/Node/MariaDB set matched to a Frappe version via tamp's version matrix, provisioned into the shared `tamp-toolchain` volume.
