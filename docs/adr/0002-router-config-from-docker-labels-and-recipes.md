# The router config is rebuilt from Docker labels and recipes

tamp keeps no record of environments outside Docker. The containers of each environment carry the labels `tamp.env` and `tamp.path`. After each command that changes what is routed, tamp rebuilds the whole Caddyfile from the running containers and their recipes, so no second record can drift from Docker.

## Considered options

- A router that reads its routes from container labels, such as Traefik, or Caddy with `caddy-docker-proxy`: labels are fixed when a container is created, so each new site would recreate the frappe container.
- A file on the machine that lists the environments, with the Caddyfile generated from it: it needs a lock and rules of its own for each write, and it goes stale when it drifts from Docker.

## Consequences

- The rebuild never fails. It skips a recipe that it cannot read, with a warning. It gives a hostname that two recipes list to the environment whose name sorts first, with a warning.
- Routes to containers that someone deleted by hand stay until the next rebuild.
