#!/usr/bin/env bash
# Runs the built binary end to end, on ubuntu in the E2E workflow. Each slice
# of the rebuild extends it.
set -euo pipefail

TAMP=${1:?usage: e2e.sh /path/to/tamp}
# Absolute, because the script changes directory before it runs tamp.
TAMP=$(cd "$(dirname "$TAMP")" && pwd)/$(basename "$TAMP")
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

say() { printf '\n=== %s\n' "$*"; }
fail() {
  echo "e2e: $*" >&2
  exit 1
}

say "tamp version prints the version, commit, and build date"
out=$("$TAMP" version)
echo "$out"
grep -q '^tamp ' <<<"$out" || fail "no version line"
grep -q '^commit: ' <<<"$out" || fail "no commit line"
grep -q '^build date: ' <<<"$out" || fail "no build date line"

say "tamp new writes the recipe and the .gitignore"
cd "$WORK"
"$TAMP" new erp-dev --frappe version-15
[ "$(cat erp-dev/tamp.toml)" = 'frappe = "version-15"' ] || fail "unexpected recipe: $(cat erp-dev/tamp.toml)"
grep -qx 'apps/' erp-dev/.gitignore || fail ".gitignore lacks apps/"
grep -qx 'compose.yaml' erp-dev/.gitignore || fail ".gitignore lacks compose.yaml"

say "tamp new without --frappe exits 2"
code=0
"$TAMP" new shop || code=$?
[ "$code" = 2 ] || fail "exit code $code, want 2"
[ ! -e shop ] || fail "shop was written"

say "e2e passed"
