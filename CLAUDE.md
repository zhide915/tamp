# tamp

Environment manager for Frappe Framework: a Go CLI that builds each Frappe bench in Docker from its recipe, `tamp.toml`, and serves every site by hostname. `GLOSSARY.md` is the vocabulary: use its terms exactly in code, output, and docs. `docs/adr/` records the decisions behind the design. Read the ones that touch your change.

## Layout

- `cmd/tamp`: cobra only. Each command turns its flags into one call on the manager in `internal/env`. Logic belongs in `internal/`.
- `internal/env`: the manager. It holds the recipe, the build steps that `new` and `start` share, and every command's behavior, `doctor` included.
- `internal/docker`: the Docker boundary: compose and the client, with a recording fake for tests.
- `internal/syncer`: the Mutagen boundary: the pinned download and the sync session, with its own recording fake.
- `internal/frappe`: the bench scripts tamp runs inside the frappe container.
- `internal/router`: the Caddy router and the Caddyfile it serves.
- `internal/ui`: the output layer.
- `internal/exitcode`: exit codes.
- `internal/browser`: hands a URL to the default browser.

## Conventions

- Build every error the user can act on with `exitcode.New`: an exit code and a one-line fix. An internal failure, such as a failed encode, stays a plain error and exits 1. When a step cannot do what was asked, it stops there: tamp has no fallbacks and no rollback, and `tamp start` resumes.
- Exit codes follow the contract in `internal/exitcode`, where 2 means only that the command line is wrong, as ADR 0006 records. Give no code a new meaning.
- All terminal output goes through `internal/ui`: results on stdout, diagnostics on stderr.
- The recipe is the source of truth. `start` builds what is missing and deletes nothing, as ADR 0001 records. The build rewrites `compose.yaml` from the recipe.
- Docker labels and recipes are the only record of which environments exist. Every command that changes what is routed rewrites the Caddyfile from them, as ADR 0002 records.
- Commands follow ADR 0006: full words, `add`, `list`, and `remove`, the `-e/--env` flag, and no prompts.
- Comments state only what the code cannot: intent, a constraint, a workaround. One or two lines.

## Tests

- The house style is the in-process CLI harness: commands run through the cobra root against a temp home, a temp environment directory, and the Docker and Mutagen fakes. Test names are sentences that state the rule they pin. Pin contracts: exit codes, output lines, files on disk, and the requests tamp sent to the fakes.
- Tests replace three boundaries: Docker and Mutagen with recording fakes, and the browser only so no test opens a window. Everything else runs for real. Host git clones from local bare repositories that stand in for remotes, through `file://` URLs because git ignores `--depth` on a plain local path, under a temp `GIT_CONFIG_GLOBAL` so no test opens a sign-in window.
- Test an internal function directly only when the CLI cannot reach its rule, such as the Caddyfile for two recipes that list the same hostname.
- A test walks the command tree and fails for any command whose `--help` lacks two examples.
- Real Docker runs in CI's Linux e2e and in the manual acceptance scripts for Windows and macOS, which also cover Mutagen.
- Verify with `go build ./...`, `go vet ./...`, `go test ./...`, and `gofmt -l`. CI also runs golangci-lint on ubuntu, windows, and macos.

## Agent skills

### Issue tracker

Issues live in GitHub Issues on `zhide915/tamp`, via the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

The five canonical triage roles, using their default label strings. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: `GLOSSARY.md` + `docs/adr/` at the repo root. See `docs/agents/domain.md`.
