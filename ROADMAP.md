# Roadmap

This file lists future work for tamp. `README.md` describes only the present.

| Trigger | Build |
|---------|-------|
| An agent or script needs to parse tamp's output. | **`--json` output**: machine-readable output for the commands that report state. |
| A developer needs to see the email a site sends. | **Built-in mail**: tamp catches the email sites send and shows it. |
| A site needs a browser feature that requires HTTPS. | **Local HTTPS**: certificates from Caddy's internal certificate authority. |
| A team needs a site without some of the apps. | **Different apps per site**: a per-site app list in the recipe. |
| Teammates' environments drift apart on code. | **Pinned commits**: an exact commit per app in the recipe. |
| A team needs the same starting data. | **Shared site data**: a way to start sites from a shared dataset. |
| Someone needs a desktop database tool. | **A host database port**: MariaDB published on the host. |
| An app depends on a private git package. | **SSH agent forwarding**: the host's SSH agent available inside the container. |
| A setup-time target is missed. | **Speed caches**: caches that make `tamp new` and `tamp start` faster. |
