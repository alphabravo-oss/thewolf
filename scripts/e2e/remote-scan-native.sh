#!/usr/bin/env bash
set -euo pipefail

[[ "${WOLF_RUN_REMOTE_SCAN_NATIVE_E2E:-}" == "1" ]] || {
  echo "SKIP: set WOLF_RUN_REMOTE_SCAN_NATIVE_E2E=1 to run native deployment qualification"
  exit 0
}

database="${WOLF_E2E_DATABASE:-sqlite}"
scanner_image="${WOLF_E2E_SCANNER_IMAGE:-}"
postgres_image="${WOLF_E2E_POSTGRES_IMAGE:-}"
binary="${WOLF_E2E_WOLF_BINARY:-}"
postgres_dsn="${WOLF_E2E_POSTGRES_DSN:-}"
[[ "$database" == sqlite || "$database" == postgres ]] || {
  echo "WOLF_E2E_DATABASE must be sqlite or postgres" >&2
  exit 2
}
[[ "$scanner_image" =~ ^[^[:space:]@]+@sha256:[a-f0-9]{64}$ ]] || {
  echo "WOLF_E2E_SCANNER_IMAGE must be an exact repository@sha256 reference" >&2
  exit 2
}
if [[ -n "$postgres_image" && ! "$postgres_image" =~ ^[^[:space:]@]+@sha256:[a-f0-9]{64}$ ]]; then
  echo "WOLF_E2E_POSTGRES_IMAGE must be an exact repository@sha256 reference" >&2
  exit 2
fi
if [[ "$database" == postgres && -z "$postgres_dsn" && -z "$postgres_image" ]]; then
  echo "PostgreSQL qualification requires WOLF_E2E_POSTGRES_DSN or WOLF_E2E_POSTGRES_IMAGE" >&2
  exit 2
fi
for command in curl docker git jq python3; do
  command -v "$command" >/dev/null || {
    echo "required command is unavailable: $command" >&2
    exit 2
  }
done

project_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
probe="$project_root/scripts/e2e/remote-scan-api-smoke.sh"
[[ -x "$probe" ]] || {
  echo "remote scan API probe is unavailable" >&2
  exit 2
}

work="$(mktemp -d "${TMPDIR:-/tmp}/wolf-native-deployment.XXXXXX")"
api_pid=""
worker_pid=""
postgres_container=""
cleanup_processes() {
  for pid in "$worker_pid" "$api_pid"; do
    [[ -n "$pid" ]] || continue
    kill -TERM "$pid" >/dev/null 2>&1 || true
  done
  for pid in "$worker_pid" "$api_pid"; do
    [[ -n "$pid" ]] || continue
    wait "$pid" >/dev/null 2>&1 || true
  done
  api_pid=""
  worker_pid=""
}
cleanup() {
  cleanup_processes
  if [[ -n "$postgres_container" ]]; then
    docker stop "$postgres_container" >/dev/null 2>&1 || true
  fi
  find "$work" -mindepth 1 -delete >/dev/null 2>&1 || true
  rmdir "$work" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

if [[ -z "$binary" ]]; then
  binary="$work/wolf"
  (
    cd "$project_root"
    go build -trimpath -o "$binary" ./cmd/wolf
  )
fi
[[ -x "$binary" ]] || {
  echo "Wolf qualification binary is not executable: $binary" >&2
  exit 2
}

mkdir -p "$work/home" "$work/repositories/fixture" "$work/workspaces" \
  "$work/artifacts" "$work/scanner-cache" "$work/evidence"
git -C "$work/repositories/fixture" init --initial-branch=main --quiet
git -C "$work/repositories/fixture" config user.email qualification@wolf.local
git -C "$work/repositories/fixture" config user.name 'Wolf Qualification'
printf '%s\n' \
  'import subprocess' \
  'subprocess.call("printf deployment-qualification", shell=True)' \
  >"$work/repositories/fixture/main.py"
git -C "$work/repositories/fixture" add main.py
git -C "$work/repositories/fixture" commit --quiet -m fixture

# Reserve a loopback port immediately before launch. This is a qualification
# runner, so there is no externally reachable listener and the API binds only
# to 127.0.0.1.
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
if [[ "$database" == postgres && -z "$postgres_dsn" ]]; then
  postgres_container="wolf-native-postgres-${RANDOM}-$$"
  postgres_password="$(python3 - <<'PY'
import secrets
print(secrets.token_hex(24))
PY
)"
  docker run --rm --detach --name "$postgres_container" \
    --tmpfs /var/lib/postgresql/data:rw,size=1g,uid=70,gid=70 \
    --tmpfs /var/run/postgresql:rw,size=16m,uid=70,gid=70 \
    -e POSTGRES_USER=wolf -e "POSTGRES_PASSWORD=$postgres_password" \
    -e POSTGRES_DB=wolf -p 127.0.0.1::5432 "$postgres_image" >/dev/null
  for attempt in {1..60}; do
    if docker exec "$postgres_container" pg_isready -U wolf -d wolf >/dev/null 2>&1; then
      break
    fi
    if [[ "$attempt" -eq 60 ]]; then
      docker logs "$postgres_container" --tail=240 >&2 || true
      echo "native qualification PostgreSQL did not become ready" >&2
      exit 1
    fi
    sleep 1
  done
  postgres_port="$(docker port "$postgres_container" 5432/tcp | sed -n 's/.*://p' | head -1)"
  postgres_dsn="postgres://wolf:$postgres_password@127.0.0.1:$postgres_port/wolf?sslmode=disable"
fi
docker_host="${DOCKER_HOST:-$(docker context inspect --format '{{.Endpoints.docker.Host}}' 2>/dev/null || true)}"
[[ -n "$docker_host" ]] || docker_host=unix:///var/run/docker.sock
scanner_digest="${scanner_image##*@}"
database_dsn="$work/wolf.db"
[[ "$database" == sqlite ]] || database_dsn="$postgres_dsn"

common_environment=(
  "HOME=$work/home"
  "WOLF_MASTER_KEY=$master_key"
  "WOLF_DB_DRIVER=$database"
  "WOLF_DB_DSN=$database_dsn"
  "WOLF_SCAN_EXECUTION_MODE=queue"
  "WOLF_SCANNER_RELEASE_MODE=read_only"
  "WOLF_SCANNERS_IMAGE=$scanner_image"
  "WOLF_SCANNERS_IMAGE_JVM=$scanner_image"
  "WOLF_SCANNERS_IMAGE_RUST=$scanner_image"
  "WOLF_SCANNERS_IMAGE_CODEQL=$scanner_image"
  "WOLF_SCANNERS_PULL_POLICY=IfNotPresent"
  "WOLF_SCANNERS_NETWORK=none"
  "WOLF_SCANNERS_DB_VOLUME=$work/scanner-cache"
  "WOLF_HOST_REPOS_ROOT=$work/repositories"
  "WOLF_IN_CONTAINER_REPOS_ROOT=$work/repositories"
  "WOLF_WORKSPACE_ROOT=$work/workspaces"
  "WOLF_HOST_WORKSPACE_ROOT=$work/workspaces"
  "WOLF_IN_CONTAINER_WORKSPACE_ROOT=$work/workspaces"
  "WOLF_ARTIFACTS_ROOT=$work/artifacts"
  "WOLF_LOG_JSON=true"
  "DOCKER_HOST=$docker_host"
)

start_processes() {
  env "${common_environment[@]}" \
    "$binary" serve --api-only --skip-scan-init --bind "127.0.0.1:$port" \
    >"$work/api.log" 2>&1 &
  api_pid="$!"
  ready_deadline="$(( $(date +%s) + 60 ))"
  until curl --silent --fail --max-time 2 \
    "http://127.0.0.1:$port/api/v1/ready" >/dev/null 2>&1; do
    kill -0 "$api_pid" >/dev/null 2>&1 || {
      echo "native Wolf API exited before readiness" >&2
      sed -n '1,240p' "$work/api.log" >&2 || true
      return 1
    }
    (( $(date +%s) < ready_deadline )) || {
      echo "native Wolf API did not become ready within 60 seconds" >&2
      return 1
    }
    sleep 1
  done
  env "${common_environment[@]}" \
    "$binary" scan-worker --backend=docker --capacity=1 --poll-interval=250ms \
    --heartbeat=1s --lease-duration=5s \
    >"$work/worker.log" 2>&1 &
  worker_pid="$!"
  sleep 1
  kill -0 "$worker_pid" >/dev/null 2>&1 || {
    echo "native Wolf worker exited during startup" >&2
    sed -n '1,240p' "$work/worker.log" >&2 || true
    return 1
  }
}

run_probe() {
  sequence="$1"
  env \
    WOLF_E2E_URL="http://127.0.0.1:$port/api/v1" \
    WOLF_E2E_SOURCE_PATH="$work/repositories/fixture" \
    WOLF_E2E_ADMIN_EMAIL=qualification@wolf.local \
    'WOLF_E2E_ADMIN_PASSWORD=WolfQualification-2026!' \
    WOLF_E2E_TIMEOUT_SECONDS="${WOLF_E2E_TIMEOUT_SECONDS:-900}" \
    WOLF_E2E_EXPECTED_BACKEND=docker \
    WOLF_E2E_EXPECTED_SCANNER_DIGEST="$scanner_digest" \
    WOLF_E2E_EVIDENCE_PATH="$work/evidence/run-$sequence.json" \
    "$probe"
}

start_processes
if ! run_probe first; then
  sed -n '1,240p' "$work/api.log" >&2 || true
  sed -n '1,240p' "$work/worker.log" >&2 || true
  exit 1
fi

# Restart both roles against the same database and storage, then execute a
# second complete API/worker scan. This proves token, repository, queue,
# artifact, and scanner-runtime recovery across an ordinary deployment restart.
cleanup_processes
start_processes
if ! run_probe after-restart; then
  sed -n '1,240p' "$work/api.log" >&2 || true
  sed -n '1,240p' "$work/worker.log" >&2 || true
  exit 1
fi

jq -n \
  --arg database "$database" \
  --arg scannerImage "$scanner_image" \
  --arg postgresImage "$postgres_image" \
  --slurpfile first "$work/evidence/run-first.json" \
  --slurpfile restarted "$work/evidence/run-after-restart.json" \
  '{
    schemaVersion:"wolf.remote-scan-native-qualification/v1",
    database:$database,
    scannerImage:$scannerImage,
    postgresImage:(if $postgresImage == "" then null else $postgresImage end),
    initial:$first[0],
    afterRestart:$restarted[0],
    restartRecoveryVerified:true,
    result:"passed"
  }'
