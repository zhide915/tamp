# ADR 0001: Live reload rides the routed /socket.io channel — no 6787 listener

Date: 2026-08-28
Status: accepted

## Context

Frappe's development mode defines a `file_watcher_port` (default 6787) in
`common_site_config.json`. If the browser connected to an asset-watcher
websocket on that port, the router would need a Host-routed 6787
listener — tamp never publishes per-environment host ports.

## Decision

Do not extend the router. Live reload reaches the browser over the
`/socket.io/*` channel the router already routes; no client connects to
port 6787.

## Evidence

Traced through frappe source on the `version-15` and `version-16` branches
(2026-08-28):

1. `esbuild/esbuild.js` — the asset watcher serves no websocket. On rebuild
   it publishes a `build_event` message to the redis `events` channel.
2. `realtime/index.js` — the socket.io server re-emits that message to
   every connected client.
3. `build_events.bundle.js` — the browser handles it on the site's own
   `/socket.io/*` connection and reloads when `live_reload` is set;
   `desk.js` loads the bundle only in developer mode.
4. `file_watcher_port` appears only in `frappe/boot.py` and bench's default
   config, with no client-side consumer on either branch.
5. Auto-reload requires `live_reload` in site config: `frappe/build.py`
   passes `--live-reload` to the watcher only when it is set. `bench init`
   writes `live_reload: true`, and tamp's config merge preserves keys it
   does not own.

## Consequences

- The router keeps exactly two concerns: sites and mail UIs.
- The hot-reload check in the Windows acceptance script asserts the
  browser-visible effect, not a connection to 6787.
- If a future Frappe major revives a direct watcher socket, revisit.
