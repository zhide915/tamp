# The router config is rebuilt from Docker labels and recipes

tamp keeps no record of environments outside Docker: each environment's containers carry the labels `tamp.env` and `tamp.path`, and after every command that changes what is routed, tamp rebuilds the whole Caddyfile from the running containers and their recipes, so no second record can drift from Docker.

## Considered options

- A router that reads routes from container labels, such as Traefik or Caddy with `caddy-docker-proxy`: labels are fixed when a container is created, so every new site would recreate the frappe container.
- A machine-wide file of environments that the Caddyfile is generated from, as the old tamp kept: it needed a lock and its own write rules, and went stale whenever it drifted from Docker.

## Consequences

- The rebuild never fails. It warns and skips a recipe it cannot read, and gives a hostname that two recipes list to the environment whose name sorts first, with a warning.
- Routes to containers deleted by hand stay until the next rebuild.
