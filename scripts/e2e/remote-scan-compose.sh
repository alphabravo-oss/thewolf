#!/usr/bin/env bash
set -euo pipefail

[[ "${WOLF_RUN_REMOTE_SCAN_COMPOSE_E2E:-}" == "1" ]] || {
  echo "SKIP: set WOLF_RUN_REMOTE_SCAN_COMPOSE_E2E=1 to run Compose qualification"
  exit 0
}

database="${WOLF_E2E_DATABASE:-sqlite}"
runtime_image="${WOLF_E2E_RUNTIME_IMAGE:-}"
scanner_image="${WOLF_E2E_SCANNER_IMAGE:-}"
postgres_image="${WOLF_E2E_POSTGRES_IMAGE:-}"
timeout_seconds="${WOLF_E2E_TIMEOUT_SECONDS:-900}"
[[ "$database" == sqlite || "$database" == postgres ]] || {
  echo "WOLF_E2E_DATABASE must be sqlite or postgres" >&2
  exit 2
}
for value_name in runtime_image scanner_image; do
  value="${!value_name}"
  [[ "$value" =~ ^[^[:space:]@]+@sha256:[a-f0-9]{64}$ ]] || {
    echo "$value_name must be an exact repository@sha256 reference" >&2
    exit 2
  }
done
if [[ -n "$postgres_image" && ! "$postgres_image" =~ ^[^[:space:]@]+@sha256:[a-f0-9]{64}$ ]]; then
  echo "WOLF_E2E_POSTGRES_IMAGE must be an exact repository@sha256 reference" >&2
  exit 2
fi
if [[ "$database" == postgres && -z "$postgres_image" ]]; then
  echo "WOLF_E2E_POSTGRES_IMAGE is required for PostgreSQL qualification" >&2
  exit 2
fi
for command in curl docker git jq python3; do
  command -v "$command" >/dev/null || {
    echo "required command is unavailable: $command" >&2
    exit 2
  }
done
docker compose version >/dev/null

project_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
probe="$project_root/scripts/e2e/remote-scan-api-smoke.sh"
work="$(mktemp -d "${TMPDIR:-/tmp}/wolf-compose-deployment.XXXXXX")"
project="wolf-remote-scan-$database-${RANDOM}-$$"
cleanup() {
  docker compose --project-directory "$project_root" --project-name "$project" \
    --file "$project_root/docker-compose.yml" --file "$work/override.yml" \
    --profile postgres down --volumes --remove-orphans >/dev/null 2>&1 || true
  find "$work" -mindepth 1 -delete >/dev/null 2>&1 || true
  rmdir "$work" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

mkdir -p "$work/repositories/fixture" "$work/workspaces" "$work/scanner-cache" \
  "$work/evidence"
git -C "$work/repositories/fixture" init --initial-branch=main --quiet
git -C "$work/repositories/fixture" config user.email qualification@wolf.local
git -C "$work/repositories/fixture" config user.name 'Wolf Qualification'
printf '%s\n' \
  'import subprocess' \
  'subprocess.call("printf compose-qualification", shell=True)' \
  >"$work/repositories/fixture/main.py"
git -C "$work/repositories/fixture" add main.py
git -C "$work/repositories/fixture" commit --quiet -m fixture

port="$(python3 - <<'PY'
import socket
with socket.socket() as sock:
    sock.bind(("127.0.0.1", 0))
    print(sock.getsockname()[1])
PY
)"
master_key="$(python3 - <<'PY'
import secrets
print(secrets.token_hex(32))
PY
)"
postgres_password="$(python3 - <<'PY'
import secrets
print(secrets.token_urlsafe(24))
PY
)"

# The base Compose model remains the artifact under test. This override only
# replaces build directives with already-qualified immutable runtime images;
# it does not alter commands, security settings, volumes, or role topology.
runtime_escaped="${runtime_image//\$/\$\$}"
{
  printf '%s\n' \
    'services:' \
    '  wolf:' \
    "    image: '$runtime_escaped'" \
    '    build: null' \
    '  scan-worker:' \
    "    image: '$runtime_escaped'" \
    '    build: null'
  if [[ -n "$postgres_image" ]]; then
    postgres_escaped="${postgres_image//\$/\$\$}"
    printf '%s\n' \
      '  postgres:' \
      "    image: '$postgres_escaped'"
  fi
} >"$work/override.yml"

compose=(docker compose --project-directory "$project_root" --project-name "$project" \
  --file "$project_root/docker-compose.yml" --file "$work/override.yml")
environment=(
  "WOLF_BIND=127.0.0.1"
  "WOLF_PORT=$port"
  "WOLF_MASTER_KEY=$master_key"
  "WOLF_DB_DRIVER=$database"
  "WOLF_SCAN_EXECUTION_MODE=queue"
  "WOLF_SCANNER_RELEASE_MODE=read_only"
  "WOLF_REPOS_ROOT=$work/repositories"
  "WOLF_HOST_REPOS_ROOT=$work/repositories"
  "WOLF_HOST_WORKSPACE_ROOT=$work/workspaces"
  "WOLF_SCANNERS_IMAGE=$scanner_image"
  "WOLF_SCANNERS_IMAGE_JVM=$scanner_image"
  "WOLF_SCANNERS_IMAGE_RUST=$scanner_image"
  "WOLF_SCANNERS_IMAGE_CODEQL=$scanner_image"
  "WOLF_SCANNERS_PULL_POLICY=IfNotPresent"
  "WOLF_SCANNERS_NETWORK=none"
  "WOLF_SCANNERS_DB_VOLUME=$work/scanner-cache"
  "WOLF_ADMIN_EMAIL="
  "WOLF_ADMIN_PASSWORD="
  "POSTGRES_PASSWORD=$postgres_password"
  "POSTGRES_PORT=0"
)
if [[ "$database" == postgres ]]; then
  environment+=("WOLF_DB_DSN=postgres://wolf:$postgres_password@postgres:5432/wolf?sslmode=disable")
else
  environment+=("WOLF_DB_DSN=")
fi

compose_env() {
  env "${environment[@]}" "${compose[@]}" "$@"
}

if [[ "$database" == postgres ]]; then
  compose_env --profile postgres up --detach --no-build postgres
  compose_env --profile postgres up --detach --no-build wolf scan-worker
else
  # SQLite migrations are intentionally initialized by the API before the
  # second process opens the shared database, avoiding an artificial startup
  # lock race while still testing two independent roles.
  compose_env up --detach --no-build wolf
  deadline="$(( $(date +%s) + 120 ))"
  until curl --silent --fail --max-time 3 \
    "http://127.0.0.1:$port/api/v1/ready" >/dev/null 2>&1; do
    (( $(date +%s) < deadline )) || {
      compose_env logs --no-color wolf >&2
      echo "Compose API did not become ready" >&2
      exit 1
    }
    sleep 2
  done
  compose_env up --detach --no-build scan-worker
fi

run_probe() {
  sequence="$1"
  env \
    WOLF_E2E_URL="http://127.0.0.1:$port/api/v1" \
    WOLF_E2E_SOURCE_PATH=/repos/fixture \
    WOLF_E2E_TIMEOUT_SECONDS="$timeout_seconds" \
    WOLF_E2E_EXPECTED_BACKEND=docker \
    WOLF_E2E_EXPECTED_SCANNER_DIGEST="${scanner_image##*@}" \
    WOLF_E2E_EVIDENCE_PATH="$work/evidence/run-$sequence.json" \
    "$probe"
}

if ! run_probe first; then
  compose_env logs --no-color >&2 || true
  exit 1
fi

# Restart both application roles without recreating their data volumes. The
# second scan verifies ordinary Compose restart recovery and DB durability.
compose_env restart wolf scan-worker
if ! run_probe after-restart; then
  compose_env logs --no-color >&2 || true
  exit 1
fi

jq -n \
  --arg database "$database" \
  --arg runtimeImage "$runtime_image" \
  --arg scannerImage "$scanner_image" \
  --arg postgresImage "$postgres_image" \
  --slurpfile first "$work/evidence/run-first.json" \
  --slurpfile restarted "$work/evidence/run-after-restart.json" \
  '{
    schemaVersion:"wolf.remote-scan-compose-qualification/v1",
    database:$database,
    runtimeImage:$runtimeImage,
    scannerImage:$scannerImage,
    postgresImage:(if $postgresImage == "" then null else $postgresImage end),
    initial:$first[0],
    afterRestart:$restarted[0],
    restartRecoveryVerified:true,
    result:"passed"
  }'
