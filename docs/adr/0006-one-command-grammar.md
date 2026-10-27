# One command grammar: full words, fixed verbs, nothing implicit

Every tamp command is a full word with fixed verbs, and none prompts. A command that acts on an environment takes it from the global `-e/--env` flag. So the same command works on a terminal, in a script, and under an agent, and one verb that you learn predicts the rest.

## Consequences

- There are no aliases and no `restart`. Sites and apps take `add`, `list`, and `remove`. An environment takes `new`, because it is created at a path, and `remove`.
- Exit code 2 means only that the command line is wrong. A conflict with the current state is exit code 1.
- The `--help` of every command ends with two examples.
- Nothing is implicit, with three stated exceptions:
  - `tamp open` with exactly one site opens that site.
  - A bare app name, such as `hrms`, means `https://github.com/frappe/hrms`.
  - Without `-e`, the working directory names the environment.
