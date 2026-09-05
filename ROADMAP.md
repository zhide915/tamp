# tamp roadmap

What tamp does next. Shipped work lives in the git log and closed issues,
not here. Each milestone below is a spec issue owning its tickets; this file
only orients and links.

Item numbers are stable IDs — retired when an item ships or drops, never
reused.

## Planned

### Hygiene & speed ([#14](https://github.com/zhide915/tamp/issues/14))

Cheaper to live with: wipeable layers, fast creates, snapshots, custom
domains.

- **R-01** Template cache: warm `create` under 2 minutes — [#15](https://github.com/zhide915/tamp/issues/15)
- **R-02** `develop` version preset (remainder) — [#16](https://github.com/zhide915/tamp/issues/16)
- **R-03** `clean` & `rebuild` — [#17](https://github.com/zhide915/tamp/issues/17)
- **R-04** Snapshots with pre-flight — [#18](https://github.com/zhide915/tamp/issues/18)
- **R-05** Seed backups: `site new --seed` — [#19](https://github.com/zhide915/tamp/issues/19)
- **R-06** hosts sync, custom domains & doctor completion — [#20](https://github.com/zhide915/tamp/issues/20)
- **R-07** `open` & `sync` subcommands — [#21](https://github.com/zhide915/tamp/issues/21)

### Private app repositories ([#29](https://github.com/zhide915/tamp/issues/29))

Private git sources: preflight, credential bridge, no stored secrets.

- **R-13** Credential bridge: source preflight, host credential relay, parse-time refusals — [#29](https://github.com/zhide915/tamp/issues/29)

### Agent & editor surface ([#22](https://github.com/zhide915/tamp/issues/22))

Nicer to drive: machine-readable output, editor wiring, HTTPS.

- **R-08** JSON contract foundation — [#23](https://github.com/zhide915/tamp/issues/23)
- **R-09** Devcontainer & editor debug config — [#24](https://github.com/zhide915/tamp/issues/24)
- **R-10** Local HTTPS via the router internal CA — [#25](https://github.com/zhide915/tamp/issues/25)
- **R-11** JSON contract completion — [#26](https://github.com/zhide915/tamp/issues/26)
- **R-12** `tamp context` & flagship agent acceptance — [#27](https://github.com/zhide915/tamp/issues/27)

## Not planned

Acknowledged ideas with no spec. Promotion into Planned requires a spec
issue.

- **R-50** `ports` router mode (deferred from [#1](https://github.com/zhide915/tamp/issues/1))
- **R-51** `~/.tamp/config.toml` machine defaults (deferred from [#1](https://github.com/zhide915/tamp/issues/1))
- **R-52** MCP server
- **R-53** Production profile (`tamp build` / `tamp deploy`)
- **R-54** `tamp update` convenience command
- **R-55** First-class `tamp app get`
- **R-56** Frappe v14 support
- **R-57** Podman engine
- **R-58** Any GUI
- **R-59** ssh app sources

Release procedure: [docs/releasing.md](docs/releasing.md)
