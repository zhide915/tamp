# Only apps/ syncs, and the rest of the bench lives in a volume

Developers edit only app code, so only `apps/` lives on the host, and the rest of the bench lives in a Docker volume. On Windows and macOS, Docker bind mounts are too slow for the imports of Frappe, and on Windows they do not pass file-change events into the container, so Mutagen syncs `apps/` both ways and the host wins each conflict. On Linux, `apps/` is bind-mounted.

## Considered options

- The whole bench on the host through a bind mount, as the development setup of frappe_docker does: slow on Windows and macOS, and a sync would carry hundreds of thousands of virtualenv and `node_modules` files.
- The source in a volume only, edited through a devcontainer: no native editing on the host.

## Consequences

- When Mutagen cannot run, tamp stops with a fix, and never falls back to a bind mount.
