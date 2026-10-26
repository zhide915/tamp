# The recipe is the desired state, and start never deletes

An environment is defined by its recipe, `tamp.toml`, which a team commits and shares. `tamp start` builds whatever the recipe lists that does not exist yet, and never deletes anything. `tamp new` writes a recipe and runs the same build, so creating an environment and sharing one take the same path.

## Considered options

- Commands with no shared file, such as `create` and `init`: a teammate cannot rebuild the same environment.
- A `start` that also deletes what the recipe no longer lists: one recipe edit could drop a site's database. Removing a site or an app is its own command, and it needs `--yes`.

## Consequences

- A failed build needs no rollback: `tamp start` resumes.
