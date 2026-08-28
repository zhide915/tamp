# froth

**froth — environment manager for Frappe Framework.**

froth is a single-binary CLI (Go) that creates and manages isolated Frappe bench environments in Docker containers. Each bench gets its own pinned Frappe/Python/Node/MariaDB versions, so benches on different Frappe majors (v15, v16, develop) coexist on one machine. Sites are reached by hostname (`mysite.localhost`) through a shared Caddy router — never by IP or port. Source code syncs to a native host folder, so AI coding agents like Claude Code work on it at full speed on Windows, macOS, or Linux. You install froth and Docker — nothing else.
