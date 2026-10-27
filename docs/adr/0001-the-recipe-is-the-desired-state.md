# The recipe is the desired state, and start never deletes

The recipe, `tamp.toml`, defines an environment, and a team commits and shares it. `tamp start` builds what the recipe lists and the environment lacks, and deletes nothing. `tamp new` writes a recipe and runs the same build, so creating an environment and sharing one take the same path.

## Considered options

- Commands with no shared file, such as `create` and `init`: a teammate cannot build the same environment again.
- A `start` that also deletes what the recipe no longer lists: one edit to the recipe could drop the database of a site. Instead, removing a site or an app is a command of its own, and it needs `--yes`.

## Consequences

- A failed build needs no rollback, because `tamp start` resumes it.
