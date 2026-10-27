# tamp

An environment manager for Frappe Framework: a Go CLI that builds each Frappe bench in Docker from its recipe, `tamp.toml`, and serves every site at its own hostname.

- Before you name anything in code, output, or docs, read `GLOSSARY.md`, and use its terms exactly.
- Before you change code, read the ADRs in `docs/adr/` that touch it.

## Layout

- `cmd/tamp`: the cobra layer only. Each command turns its flags into one call on the manager in `internal/env`. Put logic in `internal/`.
- `internal/env`: the manager: the recipe, the build that `new` and `start` share, and the behavior of every command, `doctor` and `context` included.
- `internal/docker`: the Docker boundary, compose and the client, with a recording fake.
- `internal/syncer`: the Mutagen boundary, the pinned download and the sync session, with a recording fake.
- `internal/frappe`: the bench scripts that tamp runs in the frappe container.
- `internal/router`: the Caddy router and the Caddyfile it serves.
- `internal/ui`: `ui.Printer`, the only writer to the terminal.
- `internal/exitcode`: the exit code contract.
- `internal/browser`: hands a URL to the default browser.

## Rules

- Build each error that a user can act on with `exitcode.New`: a code, a message, and a one-line fix. The fix starts with a verb, and each command it names works as written from where the user stands: build it with `target.command`. An internal failure, such as a failed encode, stays a plain error and exits 1.
- Keep the exit code contract in `internal/exitcode`: 2 means only that the command line is wrong, as ADR 0006 records. Give no code a new meaning.
- Write all terminal output through `ui.Printer`: results on stdout, diagnostics on stderr.
- Stop at the step that cannot do what was asked. tamp has no fallbacks and no rollback, and `tamp start` resumes.
- Treat the recipe as the source of truth. `start` builds what is missing and deletes nothing, as ADR 0001 records. The build rewrites `compose.yaml` from the recipe.
- Docker labels and recipes are the only record of which environments exist. Each command that changes what is routed rewrites the Caddyfile from them, as ADR 0002 records.
- Follow the command grammar of ADR 0006: full words, `add`, `list`, and `remove`, the `-e/--env` flag, and no prompts.
- Use each tool's default names. Compose names an environment's volumes and networks `<env>_<name>`, so the compose files set no `name:` for them. The bench volume, which tamp creates, follows the same pattern: `<env>_bench`.
- Write each message and doc in the present tense, top to bottom from shallow to deep, with no history. Human-facing text follows the Google developer documentation style guide. `tamp context`, this file, and the agent-test prompt are written for agents. Punctuate with commas, colons, and periods, and never an em dash.
- A comment states only what the code cannot: intent, a constraint, or a workaround, in one or two lines.

## Tests

- Test through the in-process CLI harness, `sandbox(t)` in `cmd/tamp/harness_test.go`: it runs commands through the cobra root against a temp home, a temp environment directory, and the Docker and Mutagen fakes. Name each test with a sentence that states the rule it pins. Pin contracts: exit codes, output lines, files on disk, and the requests tamp sent to the fakes.
- Replace only three boundaries: Docker and Mutagen with their recording fakes, and the browser, so that no test opens a window. Everything else runs for real. Host git clones from local bare repositories that stand in for remotes, through `file://` URLs, because git ignores `--depth` on a plain local path, and under a temp `GIT_CONFIG_GLOBAL`, so no test opens a sign-in window.
- The port 80 probe dials `router.Loopback`. The sandbox points it at a closed port, and port tests at a listener of their own, so no test depends on port 80 of the machine.
- Test an internal function directly only when the CLI cannot reach its rule, such as the Caddyfile for two recipes that list the same hostname.
- A test walks the command tree and fails for any command whose `--help` lacks two examples. Another fails when the command section of `README.md` is not the `--help` of each command: regenerate it with `go test ./cmd/tamp -run TestTheREADMECommandSectionIsTheHelpOfEveryCommand -update-readme`.
- Verify with `go build ./...`, `go vet ./...`, `go test ./...`, and `gofmt -l .`. CI also runs golangci-lint on ubuntu, windows, and macos, because build-tagged files are linted only on the platform that compiles them.
- Real Docker runs in the Linux e2e of CI, `scripts/e2e.sh`, and in the manual acceptance scripts for Windows and macOS, which also cover Mutagen. Each script measures the setup-time targets, and fails when it misses one: the first environment on a machine, with the image built, in 15 minutes or less, a new environment with the image cached in 8 minutes or less, and `start` on a stopped environment in 1 minute or less. The e2e always measures all three. An acceptance script measures the first only when it builds the image, and with `-FirstEnvironment` or `--first-environment` fails when the image was cached.

## Releasing

A pushed `v*` tag runs the Release workflow. It builds tamp for Windows, Linux, and macOS through `scripts/build.sh`, which stamps the version, commit, and build date, checks the `tamp version` of each binary, and publishes the three binaries on GitHub Releases. A manual run does the same build and check, and keeps the binaries as workflow artifacts only.

Before you push a tag, check each item on the commit that you tag:

1. [ ] The E2E workflow is green.
2. [ ] `scripts/acceptance-windows.ps1 -Agent` passes on Windows with Docker Desktop.
3. [ ] `scripts/acceptance-macos.sh --agent` passes on macOS with Docker Desktop.

After you push the tag:

4. [ ] The Release workflow is green on the tagged commit, and the release holds three binaries.

## Agent skills

### Issue tracker

Issues live in GitHub Issues on `zhide915/tamp`, via the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

The five canonical triage roles, using their default label strings. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: `GLOSSARY.md` + `docs/adr/` at the repo root. See `docs/agents/domain.md`.
