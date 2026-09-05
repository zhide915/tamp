# ADR 0002: The host is the credential authority — tamp never stores a git credential

Date: 2026-08-28
Status: accepted

## Context

`bench get-app` clones inside the frappe container, where the host's git
credentials do not exist and git has no terminal to prompt on. A private
https source fails with `could not read Username` — late, after the
expensive bench init. Unlike WSL, the container has no host interop, so
it cannot call the host's credential helper in-band. Any fix must decide
where the credential lives while it crosses to the container.

## Decision

tamp is a conduit to the host's git credential system, never a store —
the credential bridge:

1. A create/init preflight runs `git ls-remote` in-container for every
   app source before the bench is initialized, so an unreachable source
   fails in seconds.
2. On an auth-shaped preflight failure, tamp runs `git credential fill`
   on the host, where the user's helper and its prompts work. Filled
   once per host per run; `approve`/`reject` complete the protocol.
3. The credential enters only the fetch exec's environment, through
   env-based git config naming an inline env-reading helper. It reaches
   no file and no command line.
4. Sources the bridge cannot serve are refused at parse time with a
   fix: ssh sources (R-59) and token-in-URL sources.

Host git becomes a lazy dependency: only a private fetch needs it, and
`tamp doctor` reports its absence as a warning, not a failure.

## Alternatives considered

- **Clone on the host into `apps/`** — no secret crosses the boundary,
  but it forks create into two code paths and inverts HandGitToHost:
  tamp clones in Linux because of filemode, autocrlf and longpaths.
- **A token in tamp config** — a secret at rest, serving one git host.
- **A credential proxy socket into the container** — in-band like WSL,
  but needs a host-side listener; the relay gives the same at-rest
  posture without it.

## Consequences

- Fetching stays one code path, with or without a credential.
- The bridge serves only tamp-initiated fetches: create, init, and the
  future `tamp app get` (R-55). `tamp exec` stays a passthrough. After
  create, the source layer syncs to the host and host git takes over.
- Tests keep the engine as the only fake point: host git runs real
  against a sandboxed config with a canned credential helper.
- Auth failures carry CodeFailed; no new exit code.
- `reject` is host-scoped, so it fires only when the credential worked
  nowhere on that host during the run: preflight finishes every source
  before deciding, a successful preflight retry counts as acceptance,
  and only a presented-and-refused answer (`Authentication failed`)
  indicts the credential. A refusal on a host that accepted the
  credential elsewhere is a repository-scoped denial — reported as
  such, never as a stale sign-in.
- The credential is visible in the exec's environment for its duration,
  on the user's own machine — accepted.
- If Docker grows first-class host-credential interop, revisit.
