#!/usr/bin/env bash
set -euo pipefail

[[ "${WOLF_RUN_SCANNER_COMPOSE_E2E:-}" == "1" ]] || {
  echo "SKIP: set WOLF_RUN_SCANNER_COMPOSE_E2E=1 to run the real scanner gate"
  exit 0
}
image="${WOLF_SCANNER_E2E_IMAGE:-}"
qualification_dir="${WOLF_RELEASE_QUALIFICATION_DIR:-/usr/local/libexec/wolf/release-qualification}"
[[ "$image" =~ ^[^[:space:]@]+@sha256:[a-f0-9]{64}$ ]] || {
  echo "WOLF_SCANNER_E2E_IMAGE must be an exact repository@sha256 reference" >&2
  exit 2
}
for command in docker jq python3; do
  command -v "$command" >/dev/null || {
    echo "required command is unavailable: $command" >&2
    exit 2
  }
done
[[ -x "$qualification_dir/python-parser-qualification.test" ]] || {
  echo "trusted Python parser qualification binary is unavailable in $qualification_dir" >&2
  exit 2
}
docker compose version >/dev/null

now_ms() {
  python3 -c 'import time; print(time.time_ns() // 1_000_000)'
}

work="$(mktemp -d "${TMPDIR:-/tmp}/wolf-scanner-compose.XXXXXX")"
project="wolf-scanner-quality-${RANDOM}-$$"
cleanup() {
  docker compose --project-name "$project" --file "$work/compose.yaml" down \
    --volumes --remove-orphans >/dev/null 2>&1 || true
  find "$work" -mindepth 1 -delete >/dev/null 2>&1 || true
  rmdir "$work" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

cat >"$work/compose.yaml" <<'YAML'
services:
  scanner:
    image: ${WOLF_SCANNER_E2E_IMAGE}
    network_mode: none
    read_only: true
    cap_drop: [ALL]
    security_opt: [no-new-privileges:true]
    tmpfs:
      # The scanner image is intentionally rootless. Keep the container
      # filesystem read-only while allowing its isolated fixture tmpfs to be
      # populated by the image's unprivileged runtime user.
      - /fixture:size=1m,mode=1777
    entrypoint: [/bin/sh, -c]
    # The fixture is created inside a tmpfs by the exact image. This is
    # intentionally independent of the remote Docker daemon's host paths.
    command:
      - "printf '%s\\n' 'import subprocess' 'subprocess.call(\"printf fixture\", shell=True)' > /fixture/main.py && exec bandit -r /fixture -f json --exit-zero"
YAML

resolved="$(
  WOLF_SCANNER_E2E_IMAGE="$image" \
    docker compose --project-name "$project" --file "$work/compose.yaml" \
    config --images
)"
[[ "$resolved" == "$image" ]] || {
  echo "Compose normalized away the exact scanner digest: $resolved" >&2
  exit 1
}
started_ms="$(now_ms)"
WOLF_SCANNER_E2E_IMAGE="$image" \
  docker compose --project-name "$project" --file "$work/compose.yaml" \
  run --rm --no-deps scanner >"$work/bandit.json"
duration_ms="$(( $(now_ms) - started_ms ))"
jq -e '.results | type == "array" and length > 0' "$work/bandit.json" >/dev/null
WOLF_BANDIT_E2E_OUTPUT="$work/bandit.json" \
  "$qualification_dir/python-parser-qualification.test" \
  -test.run '^TestParseBanditRealE2EOutput$' -test.count=1

jq -n \
  --arg image "$image" \
  --argjson durationMs "$duration_ms" \
  --argjson outputBytes "$(wc -c <"$work/bandit.json" | tr -d ' ')" \
  '{
    schemaVersion: "wolf.scanners/integration-evidence/v1",
    runtime: "compose",
    tool: "bandit",
    image: $image,
    durationMs: $durationMs,
    outputBytes: $outputBytes,
    parser: "wolf/plugin/bandit",
    result: "passed"
  }'
