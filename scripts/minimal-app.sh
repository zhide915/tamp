#!/usr/bin/env bash
# Makes a bare repository holding the smallest Frappe app bench installs:
# repository acme-erp, package acme, default branch main. For the e2e and
# the acceptance scripts, which need an app but not GitHub.
set -euo pipefail

BARE=${1:?usage: minimal-app.sh /path/to/acme-erp.git}
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

mkdir -p "$WORK/acme/acme" "$WORK/acme/config" "$WORK/acme/public/js"
cat >"$WORK/pyproject.toml" <<'EOF'
[project]
name = "acme"
authors = [{ name = "Acme", email = "dev@acme.example" }]
description = "Acme"
requires-python = ">=3.10"
dynamic = ["version"]
dependencies = []

[build-system]
requires = ["flit_core >=3.4,<4"]
build-backend = "flit_core.buildapi"
EOF
printf '{\n  "name": "acme",\n  "private": true\n}\n' >"$WORK/package.json"
printf '__version__ = "0.0.1"\n' >"$WORK/acme/__init__.py"
cat >"$WORK/acme/hooks.py" <<'EOF'
app_name = "acme"
app_title = "Acme"
app_publisher = "Acme"
app_description = "Acme"
app_email = "dev@acme.example"
app_license = "mit"
EOF
printf 'Acme\n' >"$WORK/acme/modules.txt"
printf '[pre_model_sync]\n\n[post_model_sync]\n' >"$WORK/acme/patches.txt"
: >"$WORK/acme/acme/__init__.py"
: >"$WORK/acme/config/__init__.py"
printf 'console.log("acme");\n' >"$WORK/acme/public/js/acme.bundle.js"

git -C "$WORK" init --quiet --initial-branch main
git -C "$WORK" add --all
git -C "$WORK" -c user.name=tamp -c user.email=tamp@example.com commit --quiet -m "acme"
git -C "$WORK" checkout --quiet -b develop
git -C "$WORK" -c user.name=tamp -c user.email=tamp@example.com commit --quiet --allow-empty -m "develop"
git -C "$WORK" checkout --quiet main
git clone --quiet --bare "$WORK" "$BARE"
