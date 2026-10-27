# Apps are cloned on the host, the way bench get-app clones

tamp clones every app, Frappe included, into the `apps/` folder of the environment with the git of the host, and with the flags that `bench get-app` uses: `--branch <branch> --depth 1 --origin upstream`. The host already holds the credentials of the user, so private repositories work over HTTPS or SSH with nothing passed into the container, and developers keep their own git habits.

## Considered options

- Cloning inside the container, through a bridge that passes the credentials of the host to each fetch: a protocol of its own to build, maintain, and secure.

## Consequences

- A pip or yarn dependency from a private git URL fails inside the container. List it in the recipe as an app.
- On Windows, clones use `core.autocrlf=false`, so line endings survive the sync.
- tamp never pulls, checks out, or resets an existing clone.
