# tamp

Environment manager for Frappe Framework: a Go CLI that runs each Frappe
bench in Docker containers and routes every site by hostname. The glossary in
`CONTEXT.md` is the vocabulary — use its terms exactly, in code, output, and
docs (an *environment* is the unit tamp manages; a *bench* is only the
Frappe-side workspace inside one).

## Layout

- `cmd/tamp` — thin cobra layer. Commands translate flags into one call on
  `env.Manager` and nothing else; logic added here belongs in `internal/`.
- `internal/env` — the core: create/start/stop/rm, sites, apps, init.
- `internal/engine` — the Docker boundary, and the **only fake point**.
  Tests use `enginetest.Fake`, a recording fake; assert on what tamp asked
  the engine to do, never by mocking anything else.
- `internal/gitcred` — the host git credential protocol (the credential
  bridge's host half, ADR 0002). A real boundary, never faked.
- `internal/frappe`, `internal/toolchain`, `internal/router`,
  `internal/syncer`, `internal/doctor`, `internal/ui`, `internal/exitcode`.

## Conventions

- Every error carries an exit code and a one-line fix: build it with
  `exitcode.New`. The code contract (0/1/2/3/4/5) lives in
  `internal/exitcode` — never invent a new meaning for a code.
- All terminal output goes through `ui.Printer`. Results belong on stdout,
  diagnostics on stderr; nothing writes past the Printer.
- Machine-unique facts — environment names, hostnames (sites *and* mail
  UIs), database ports — are claimed only through the ledger verbs in
  `internal/env/registry.go`, which run under the machine lock. Never
  read-modify-write the registry anywhere else.
- `tamp.toml` is the source of truth for an environment; generated files
  (`compose.yaml`, the Caddyfile) are rewritten from it on every start.
- Comments state only what the code cannot: intent, a constraint, a
  workaround. One or two lines.

## Tests

- The house style is the in-process CLI harness: `sandbox(t)` in
  `cmd/tamp/harness_test.go` runs commands through the cobra root against a
  temp home and the engine fake. Test names are sentences stating the rule
  they pin; pin contracts (exit codes, output, engine requests), not wiring.
- Host git runs real in tests, steered by the sandbox's `GIT_CONFIG_GLOBAL`
  at a canned credential helper (`cannedCredentials` in
  `cmd/tamp/bridge_test.go`); that isolation also keeps tests from opening
  the developer's sign-in GUI.
- Verify with `go build ./...`, `go vet ./...`, `go test ./...`, and
  `gofmt -l`. CI also runs golangci-lint on ubuntu, windows, and macos.

## Agent skills

### Issue tracker

Issues live in GitHub Issues on `zhide915/tamp`, via the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

The five canonical triage roles, using their default label strings. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: `CONTEXT.md` + `docs/adr/` at the repo root. See `docs/agents/domain.md`.
