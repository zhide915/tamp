#!/usr/bin/env bash
# Runs tamp end to end against Docker Desktop on macOS: an environment with a
# site and an app, Mutagen syncing apps/ both ways, the setup-time targets, and
# with --agent the agent test. Any failed check or missed target exits 1.
#
# tamp runs under a temp home, so the script never reads or writes your own
# ~/.tamp. It needs Docker Desktop running, port 80 free, git, and no
# tamp-router container or running tamp environment. It removes everything it
# makes, except the bench image and the tamp-cache volume. Run it from the
# repository root:
#
#   scripts/acceptance-macos.sh --agent
#
# Options:
#   --tamp PATH          the binary under test, such as a release download;
#                        built from this checkout when omitted
#   --first-environment  fail unless the first tamp new builds the bench image,
#                        which measures the first-environment target; remove the
#                        version-15 tamp-bench image first, with `docker rmi`
#   --agent              run the agent test with the Claude Code CLI, `claude`,
#                        which needs jq
set -euo pipefail

TAMP_BIN=
FIRST=
AGENT=
while [ $# -gt 0 ]; do
  case $1 in
    --tamp) TAMP_BIN=${2:?--tamp needs a path}; shift 2 ;;
    --first-environment) FIRST=1; shift ;;
    --agent) AGENT=1; shift ;;
    *) echo "usage: acceptance-macos.sh [--tamp PATH] [--first-environment] [--agent]" >&2; exit 2 ;;
  esac
done

REPO=$(cd "$(dirname "$0")/.." && pwd)
REAL_HOME=$HOME
ENV_NAME=tamp-accept
SITE=accept.localhost
MADE=()
TIMINGS=
BROWSER_CHECK=

say() { printf '\n=== %s\n' "$*"; }
fail() {
  echo "acceptance: $*" >&2
  exit 1
}

# The pre-checks run before the cleanup is armed, so a refusal touches nothing.
say "the machine has Docker, no tamp-router, and no running tamp environment"
docker info --format '{{.ServerVersion}}' || fail "docker info failed: start Docker Desktop, and run the script again"
[ -z "$(docker ps --all --quiet --filter 'name=^tamp-router$')" ] ||
  fail "a tamp-router container exists, which the script would share: remove it with \`docker rm --force tamp-router\`, and run the script again; your next \`tamp start\` creates it again"
[ -z "$(docker ps --quiet --filter label=tamp.env)" ] ||
  fail "a tamp environment is running, which the router would serve too: find it with \`docker ps --filter label=tamp.env\`, stop it with \`tamp stop -e <name>\`, and run the script again"
for name in tamp-accept tamp-accept2; do
  [ -z "$(docker ps --all --quiet --filter "label=tamp.env=$name")" ] ||
    fail "the environment $name exists, which the cleanup would remove: remove it with \`tamp remove -e $name --yes\`, and run the script again"
done
# tamp remove finds an environment by its containers, so it cannot remove volumes a failed tamp new left alone.
left_volumes=$(docker volume ls --quiet --filter 'name=^tamp-accept2?_(db|bench)$' | tr '\n' ' ' | sed 's/ $//')
[ -z "$left_volumes" ] ||
  fail "the volumes $left_volumes, which this run would adopt and its cleanup remove, are left from a failed tamp new: remove them with \`docker volume rm $left_volumes\`, and run the script again"
if [ -n "$AGENT" ]; then
  command -v claude >/dev/null || fail "no claude on PATH: install Claude Code, or run without --agent"
  command -v jq >/dev/null || fail "no jq on PATH: install it with \`brew install jq\`, or run without --agent"
fi
NETWORK_EXISTED=$(docker network ls --quiet --filter 'name=^tamp-router_default$')

WORK=$(mktemp -d "${TMPDIR:-/tmp}/tamp-acceptance-XXXXXX")
TEMP_HOME=$WORK/home
ENV_DIR=$WORK/envs/$ENV_NAME
TAMP=$WORK/bin/tamp

cleanup() {
  local code=$?
  # Each step runs even when one fails, such as find on a folder never made.
  set +e
  cd "$REPO"
  # Bash 3.2, which macOS ships, calls an empty array unbound.
  for name in ${MADE[@]+"${MADE[@]}"}; do "$TAMP" remove -e "$name" --yes --quiet >/dev/null 2>&1 || true; done
  # tamp leaves Mutagen's daemon running.
  local mutagen
  mutagen=$(find "$TEMP_HOME/.tamp/bin" -name mutagen -type f 2>/dev/null | head -n 1 || true)
  if [ -n "$mutagen" ]; then MUTAGEN_DATA_DIRECTORY=$TEMP_HOME/.tamp/mutagen "$mutagen" daemon stop >/dev/null 2>&1 || true; fi
  # tamp never removes the router. The pre-checks made sure this run created it, and its network.
  docker rm --force tamp-router >/dev/null 2>&1 || true
  if [ -z "$NETWORK_EXISTED" ]; then docker network rm tamp-router_default >/dev/null 2>&1 || true; fi
  rm -rf "$WORK"
  printf '\n=== timings\n%s' "$TIMINGS"
  if [ -n "$BROWSER_CHECK" ]; then printf "\n=== the agent's browser check: %s\n" "$BROWSER_CHECK"; fi
  exit "$code"
}
trap cleanup EXIT

# target records a timing in seconds, and fails when it misses its target.
target() {
  local line="- $1: $3 s, target $2 s or less"
  TIMINGS+="$line"$'\n'
  echo "timing $line"
  [ "$3" -le "$2" ] || fail "$1 took $3 s, over its target of $2 s"
}

wait_until() {
  local deadline=$((SECONDS + $1))
  shift
  while [ "$SECONDS" -lt "$deadline" ]; do
    if "$@" >/dev/null 2>&1; then return 0; fi
    sleep 2
  done
  return 1
}

# through_router fetches through the router on port 80, whatever *.localhost resolves to here.
through_router() {
  local host=$1
  shift
  curl -sS --max-time 10 --resolve "$host:80:127.0.0.1" "$@"
}

# Each captures its output first: grep -q would close the pipe early, which pipefail reports.
pings() {
  local out
  out=$(through_router "$1" "http://$1/api/method/ping") && grep -q pong <<<"$out"
}
bench_images() { docker images --quiet --filter 'reference=tamp-bench:*' | sort; }
watching() {
  local out
  out=$("$TAMP" status) && grep -q '^sync: watching' <<<"$out"
}
in_container() { "$TAMP" shell -- "$@" </dev/null; }
asset_requests() {
  local out
  out=$("$TAMP" logs frappe 2>&1)
  grep -cE '"GET /assets/[^ ]+\.js[^ ]* HTTP/[0-9.]+" (200|304)' <<<"$out" || true
}

mkdir -p "$WORK/bin" "$TEMP_HOME" "$WORK/envs"
if [ -n "$TAMP_BIN" ]; then
  cp "$TAMP_BIN" "$TAMP"
  chmod +x "$TAMP"
else
  say "building tamp from $REPO"
  (cd "$REPO" && go build -o "$TAMP" ./cmd/tamp)
fi

# Docker's config stays the user's, for its contexts and the Compose plugin.
export DOCKER_CONFIG=${DOCKER_CONFIG:-$REAL_HOME/.docker}
export HOME=$TEMP_HOME
# Agents and the printed examples call tamp by name.
export PATH=$WORK/bin:$PATH
cd "$WORK/envs"

say "tamp version prints the version, commit, and build date"
out=$("$TAMP" version)
echo "$out"
grep -q '^tamp ' <<<"$out" && grep -q '^commit: ' <<<"$out" && grep -q '^build date: ' <<<"$out" ||
  fail "tamp version lacks a line"

say "tamp new builds the environment, and the timing meets its target"
before=$(bench_images)
start=$SECONDS
# Recorded first, so the cleanup removes a half-made environment.
MADE+=("$ENV_NAME")
"$TAMP" new "$ENV_NAME" --frappe version-15 --site "$SITE"
took=$((SECONDS - start))
built=
[ "$(bench_images)" = "$before" ] || built=1
if [ -n "$built" ]; then
  target "first environment on the machine (image built)" 900 "$took"
elif [ -n "$FIRST" ]; then
  fail 'tamp new found the bench image cached, so --first-environment measured nothing: remove the image with `docker rmi`, and run again'
else
  target "new environment, image cached" 480 "$took"
  TIMINGS+="- first environment on the machine (image built): not measured, as the image was cached; run with --first-environment"$'\n'
fi

if [ -n "$built" ]; then
  say "a second environment with the image cached meets its target"
  start=$SECONDS
  MADE+=(tamp-accept2)
  "$TAMP" new tamp-accept2 --frappe version-15 --site accept2.localhost
  took=$((SECONDS - start))
  target "new environment, image cached" 480 "$took"
  wait_until 60 pings accept2.localhost || fail "accept2.localhost did not answer through the router"
  "$TAMP" remove -e tamp-accept2 --yes
fi

cd "$ENV_DIR"

say "the site answers through the router"
wait_until 60 pings "$SITE" || fail "$SITE did not answer through the router"

say "tamp app add clones a local app and installs it"
bash "$REPO/scripts/minimal-app.sh" "$WORK/remotes/acme-erp.git"
"$TAMP" app add "file://$WORK/remotes/acme-erp.git"
[ -f apps/acme/acme/hooks.py ] || fail "apps/acme holds no clone"

say "apps/ syncs through Mutagen, not a bind mount"
mounts=$(docker inspect --format '{{ range .Mounts }}{{ .Type }}:{{ .Destination }} {{ end }}' "$ENV_NAME-frappe-1")
[ -n "$mounts" ] || fail "the frappe container lists no mounts"
if grep -qw 'bind:/home/frappe/frappe-bench/apps' <<<"$mounts"; then fail "apps/ is bind-mounted: $mounts"; fi
[ -n "$(find "$TEMP_HOME/.tamp/bin" -name mutagen -type f)" ] || fail "tamp did not download Mutagen"
wait_until 60 watching || fail "the sync session is not watching: $("$TAMP" status)"
"$TAMP" doctor || fail "tamp doctor failed"

say "a file written on the host reaches the container"
echo host >apps/acme/from_host.txt
arrived() {
  local out
  out=$(in_container cat apps/acme/from_host.txt) && [ "$out" = host ]
}
wait_until 30 arrived || fail "from_host.txt did not reach the container"

say "a file written in the container reaches the host"
in_container sh -c 'echo container > apps/acme/from_container.txt'
wait_until 30 test -f apps/acme/from_container.txt || fail "from_container.txt did not reach the host"

say "a file deleted on the host is deleted in the container"
rm apps/acme/from_host.txt apps/acme/from_container.txt
gone() {
  local out
  out=$(in_container ls apps/acme) && ! grep -q from_ <<<"$out"
}
wait_until 30 gone || fail "the deletions did not reach the container"

say "tamp start on a stopped environment meets its target"
"$TAMP" stop
start=$SECONDS
"$TAMP" start
target "start on an existing environment" 60 $((SECONDS - start))
wait_until 60 pings "$SITE" || fail "$SITE did not answer after stop and start"
wait_until 60 watching || fail "the sync session is not watching after start"

if [ -n "$AGENT" ]; then
  say 'the agent test: an agent given only "run tamp context" makes a change, tests it, and checks it'
  prompt='Run `tamp context`. Then make this change in the app acme: add a whitelisted method acme.api.greet that returns the text Hello from tamp, add a test for it and run that test, then check the change in the browser.'
  # The agent's scratch files, such as a browser profile, go where the cleanup deletes them.
  mkdir "$WORK/agent-temp"
  REAL_TMP=${TMPDIR:-/tmp}
  temp_before=$(ls -A "$REAL_TMP")
  assets_before=$(asset_requests)
  code=0
  # The agent keeps the user's Claude Code sign-in, but none of its MCP servers.
  CLAUDE_CONFIG_DIR=$REAL_HOME/.claude TMPDIR=$WORK/agent-temp claude -p "$prompt" --output-format stream-json --verbose --strict-mcp-config \
    --allowedTools 'Bash,Read,Edit,Write,Glob,Grep' >"$WORK/agent.jsonl" </dev/null || code=$?
  # An agent may still name the real temp folder, where other programs write too, so the script only lists what appeared.
  left=$(comm -13 <(printf '%s\n' "$temp_before") <(ls -A "$REAL_TMP"))
  [ -z "$left" ] || echo "Check and delete what appeared in $REAL_TMP while the agent ran: $(tr '\n' ' ' <<<"$left")"
  commands=$(jq -r 'select(.type == "assistant") | .message.content[] | select(.type == "tool_use" and .name == "Bash") | .input.command' "$WORK/agent.jsonl")
  echo "The agent ran:"
  sed 's/^/  $ /' <<<"$commands"
  echo "The agent said: $(jq -r 'select(.type == "result") | .result' "$WORK/agent.jsonl")"
  echo "Denied tools: $(jq -r 'select(.type == "result") | [.permission_denials[]?.tool_name] | join(", ")' "$WORK/agent.jsonl")"
  [ "$code" = 0 ] || fail "claude exited $code"
  grep -q 'tamp context' <<<"$commands" || fail "the agent never ran tamp context"
  tests=$(jq -r 'select(.type == "assistant") | .message.content[] | select(.type == "tool_use" and .name == "Bash") | .input.command | select(test("tamp shell") and test("run-tests"))' "$WORK/agent.jsonl")
  [ -n "$tests" ] || fail "the agent never ran a test through tamp shell"
  # The web server logs each request, so this holds for curl and a browser alike.
  # Captured first: grep -q would close the pipe on tamp, which pipefail reports.
  logs=$("$TAMP" logs frappe 2>&1)
  grep -qE '"(GET|POST) /api/method/acme\.api\.greet[^ ]* HTTP/[0-9.]+" 200' <<<"$logs" ||
    fail "the agent never fetched acme.api.greet from the site: the frappe logs show no request that answered 200"
  # A browser that renders a page fetches its JavaScript, which curl of a method never does.
  assets=$(($(asset_requests) - assets_before))
  if [ "$assets" -gt 0 ]; then
    BROWSER_CHECK="detected: the frappe logs show $assets JavaScript asset requests while the agent ran"
  else
    BROWSER_CHECK='not detected: no page loaded its JavaScript while the agent ran, so "in the browser" is unproven for this run'
  fi
  echo "Browser check: $BROWSER_CHECK"

  say "the agent's test module passes on the test site"
  # The module, not the agent's last command, which may break the code on purpose to see the test fail.
  modules=$(grep -oE "run-tests[^;&|]*--module[=[:space:]]+[\"']?[[:alnum:]_.]+" <<<"$tests" | sed -E "s/.*--module[=[:space:]]+[\"']?//" | sort -u)
  [ -n "$modules" ] || fail "the agent ran no run-tests --module"
  for module in $modules; do
    in_container env CI=1 bench --site "$ENV_NAME-test.localhost" run-tests --module "$module" ||
      fail "run-tests --module $module failed"
  done

  say "the change answers through the router for a logged-in user"
  through_router "$SITE" -c "$WORK/cookies" -d usr=Administrator -d pwd=password "http://$SITE/api/method/login" >/dev/null
  greet=$(through_router "$SITE" -b "$WORK/cookies" "http://$SITE/api/method/acme.api.greet")
  echo "$greet"
  grep -q 'Hello from tamp' <<<"$greet" || fail "acme.api.greet answered: $greet"
  [ -f apps/acme/acme/api.py ] || fail "the change is not on the host, in apps/acme/acme/api.py"
fi

say "acceptance passed"
