# Roadmap

This file lists future work for tamp. `README.md` describes only the present.

| Trigger | Build |
|---------|-------|
| An agent or a script needs to parse the output of tamp. | **`--json` output**: machine-readable output for the commands that report state. |
| A developer needs to see the email that a site sends. | **Built-in mail**: tamp catches the email of each site and shows it. |
| A site needs a browser feature that only HTTPS allows. | **Local HTTPS**: certificates from the internal certificate authority of Caddy. |
| A team needs a site without some of the apps. | **Different apps per site**: an app list for each site in the recipe. |
| The code in the environments of teammates drifts apart. | **Pinned commits**: an exact commit for each app in the recipe. |
| A team needs the same starting data. | **Shared site data**: sites that start from a shared dataset. |
| A developer needs a desktop database tool. | **A host database port**: MariaDB published on the host. |
| An app depends on a private git package. | **SSH agent forwarding**: the SSH agent of the host, available inside the container. |
| A setup-time target is missed. | **Speed caches**: caches that make `tamp new` and `tamp start` faster. |
