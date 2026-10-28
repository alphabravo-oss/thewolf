#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
run=$(mktemp -d "${TMPDIR:-/tmp}/${ME:-wolf-e2e-live}.XXXXXX")
server_pid=
stop_server() {
  if [[ -n "$server_pid" ]]; then
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
    server_pid=
  fi
}
trap stop_server EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
# SQLite is gone: each journey gets its own database on a throwaway PostgreSQL
# (e.g. docker run -d --rm -p 55440:5432 -e POSTGRES_PASSWORD=test postgres:18-alpine).
admin_dsn="${WOLF_E2E_POSTGRES_DSN:?set WOLF_E2E_POSTGRES_DSN to a throwaway PostgreSQL URL (a database is created and dropped per journey)}"
command -v psql >/dev/null || { echo "psql is required" >&2; exit 2; }
echo "Live e2e artifacts: $run"
cd "$root"
export GOCACHE="${GOCACHE:-${TMPDIR:-/tmp}/wolf-e2e-go-cache}"
CGO_ENABLED=1 go build -o "$run/wolf" ./cmd/wolf
cd ui
npx --no-install vite build --outDir "$run/dist" --emptyOutDir
port=$(node -e 'const s=require("node:net").createServer();s.on("error",e=>{console.error(e.message);process.exit(1)});s.listen(Number(process.env.PORT||0),"127.0.0.1",()=>{console.log(s.address().port);s.close()})')
export WOLF_LIVE_BASE_URL="http://127.0.0.1:$port"
export WOLF_ADMIN_EMAIL=admin@wolf.local WOLF_ADMIN_PASSWORD='WolfAdmin!2026xx'
result=0
# Each new-operator journey gets an empty Home, independent of test order/failure.
for project in desktop mobile; do
  # Scanner images are the product defaults (published GHCR release) unless
  # WOLF_SCANNERS_IMAGE* / WOLF_SCANNERS_TAG are exported by the caller.
  # Vulnerability DBs persist across runs like a real install; a fresh HOME must not redownload them.
  scanner_cache="${WOLF_E2E_SCANNER_CACHE:-${XDG_CACHE_HOME:-$HOME/.cache}/wolf-e2e-scanner-cache}"
  mkdir -p "$scanner_cache"
  for spec in ease daily; do
    home="$run/$project-$spec"
    mkdir -p "$home"
    database="wolf_e2e_$(openssl rand -hex 6)"
    psql "$admin_dsn" --quiet --command "CREATE DATABASE $database"
    database_dsn=$(python3 -c 'import sys,urllib.parse as u; p=u.urlparse(sys.argv[1]); print(p._replace(path="/"+sys.argv[2]).geturl())' "$admin_dsn" "$database")
    export WOLF_LIVE_RESULTS_DIR="$home/results"
    export WOLF_LIVE_SAMPLE_DIR="$home/.wolf/sample-repo"
    (
      cd "$home"
      HOME="$home" WOLF_DB_DRIVER=postgres WOLF_DB_DSN="$database_dsn" WOLF_UI_DIR="$run/dist" \
        WOLF_MASTER_KEY="$(openssl rand -hex 32)" \
        WOLF_SCANNERS_PULL_POLICY="${WOLF_SCANNERS_PULL_POLICY:-IfNotPresent}" \
        WOLF_SCANNERS_DB_VOLUME="$scanner_cache" \
        exec "$run/wolf" serve --bind "127.0.0.1:$port"
    ) >"$home/server.log" 2>&1 &
    server_pid=$!
    ready=false
    for ((attempt=0; attempt<120; attempt++)); do
      kill -0 "$server_pid" 2>/dev/null || break
      if curl --fail --silent "$WOLF_LIVE_BASE_URL/api/v1/health" >/dev/null; then ready=true; break; fi
      sleep 1
    done
    if [[ "$ready" != true ]]; then
      tail -50 "$home/server.log"
      exit 1
    fi
    npx --no-install playwright test -c playwright.live.config.ts "$spec.spec.ts" --project "$project" "$@" || result=$?
    stop_server
    # Keep results and logs; drop the server's data (workspaces, artifacts) so runs fit a tmpfs.
    rm -rf "$home/.wolf"
    psql "$admin_dsn" --quiet --command "DROP DATABASE $database WITH (FORCE)"
  done
done
echo "Live e2e artifacts: $run"
exit "$result"
