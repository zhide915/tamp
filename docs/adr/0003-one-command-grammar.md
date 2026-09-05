# ADR 0003: One command grammar — three verbs, one env flag, nothing implicit

Date: 2026-09-05
Status: accepted

## Context

A week of dogfooding showed the command surface was learned, not
guessed. Two commands made an environment (`create`, `init`); sites were
`new`, snapshots `create`d, everything `rm`d; `clean`'s four flags and
`rebuild` described tamp's layers rather than what the user wanted; the
`[env]` positional sat in front of a required host, so `tamp site rm
foo` had to be parsed by a person; and `sync`, `hosts` and `db` exposed
plumbing as product. The agent and editor surface was about to pin these
names into public JSON contracts.

tamp is pre-release with one user, so a clean break costs nothing now
and grows dearer with every release.

## Decision

1. **Full words, three verbs.** Every noun (`site`, `snapshot`) uses
   `add`, `list`, `remove`; `snapshot` alone adds `save` and `restore`.
   No shorthand aliases, hidden or otherwise. The environment is made by
   `tamp new <path>`: the path is required, the name is its last
   segment, and `tamp new .` adopts the current directory.
2. **One way to name an environment.** A global `-e/--env` flag works on
   every command. Commands whose only argument is an environment
   (`start`, `stop`, `restart`, `remove`, `status`, `open`, `context`)
   also accept it as a positional; both given and different is a usage
   error. Omitted, it resolves the way git finds a repo.
3. **Nothing implicit.** tamp never prompts. Values that decide what gets
   built — the path and `--frappe` — are required, with a usage error
   and a fix line when missing; values that describe the machine keep
   defaults. `open` among several sites, a bare noun, and `reset` with
   no target all exit 2 and say what to type. Destructive commands keep
   `--yes` and exit 5.
4. **Plumbing folds into two commands.** `status` shows containers,
   sites, sync session and database access, replacing `db` and `sync
   status`. `doctor --fix` performs every repair doctor can name,
   replacing `hosts sync` and the `sync` repairs. `reset <target>...`
   replaces `clean` and `rebuild`.
5. **`run -- <command>` replaces `exec`.** The separator stays mandatory
   so bench's flags never reach tamp. The advice step stays and
   `--force` skips it. `shell` opens an interactive shell and never
   advises.

## Alternatives considered

- **Deprecated aliases for a release** — doubles every renamed command's
  tests and help for a user base of one.
- **Interactive prompts on a terminal** — a second UI to keep agent-safe,
  and a wrong default still costs a redo of bench init.
- **Positional env everywhere** — familiar, but ambiguous before a
  required argument and impossible to make uniform.
- **Version aliases (`v15`, `15`)** — a mapping to maintain for a value
  git already accepts verbatim.

## Consequences

- `create`, `init`, `rm`, `site new`, `site rm`, `snapshot create`,
  `clean`, `rebuild`, `exec`, `db`, `sync *` and `hosts sync` go away;
  `hosts apply` stays as a hidden command for the elevated write.
- README, CI e2e and the Windows acceptance script are rewritten in the
  same change; the open agent-surface, devcontainer, JSON-contract and
  acceptance specs are amended to the new names.
- `status --json` carries the `sync` object the JSON contract pinned; the
  context block's `commands` become `logs`, `status`, `reset`.
- `tamp app add|list|remove` is deferred to its own issue.
- Exit codes are unchanged: nothing here needs a new meaning.
