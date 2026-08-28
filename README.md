# tamp

**tamp — environment manager for Frappe Framework.**

tamp is a single-binary CLI (Go) that creates and manages isolated Frappe bench environments in Docker containers. Each bench gets its own pinned Frappe/Python/Node/MariaDB versions, so benches on different Frappe majors (v15, v16, develop) coexist on one machine. Sites are reached by hostname (`mysite.localhost`) through a shared Caddy router — never by IP or port. Source code syncs to a native host folder, so AI coding agents like Claude Code work on it at full speed on Windows, macOS, or Linux. You install tamp and Docker — nothing else.

## Reaching an environment

One Caddy container routes for the whole machine. It takes port 80 when it can,
and 8080 when something else already has it — tamp says so and prints every URL
with the port in it.

Every environment gets a mail UI at `http://mail.<env>.localhost` the moment it
exists, and every site gets `http://<site>` once it is created. `*.localhost`
resolves to loopback in every evergreen browser with no configuration at all.

Command-line tools are the exception: `curl` does not resolve `*.localhost` on
every platform, so point it at the router yourself.

```sh
curl --resolve mail.demo.localhost:80:127.0.0.1 http://mail.demo.localhost
```

With the router on the fallback port, use that port in both places:

```sh
curl --resolve mail.demo.localhost:8080:127.0.0.1 http://mail.demo.localhost:8080
```
