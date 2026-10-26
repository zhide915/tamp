# Apps are cloned on the host, the way bench get-app clones

tamp clones every app, Frappe included, with the host's own git into the environment's `apps/` folder, with the flags `bench get-app` uses: `--branch <branch> --depth 1 --origin upstream`. The host already holds the user's git credentials, so private repositories work over HTTPS or SSH with nothing passed into the container, and developers keep their usual git habits.

## Considered options

- Cloning inside the container through a bridge that passed the host's credentials to each fetch: about 600 lines and a protocol to maintain.

## Consequences

- A pip or yarn dependency that comes from a private git URL fails inside the container. List it in the recipe as an app.
- On Windows, clones use `core.autocrlf=false` so line endings survive the sync.
- tamp never pulls, checks out, or resets an existing clone.
