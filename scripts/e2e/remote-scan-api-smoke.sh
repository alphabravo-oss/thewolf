#!/usr/bin/env bash
set -euo pipefail

# Exercise the public remote-scanning contract against an already running
# Wolf API and worker. The deployment harness owns process/container lifecycle;
# this probe owns only API-visible behavior so the same assertions can be used
# for native, Compose, and Kubernetes installations.

base_url="${WOLF_E2E_URL:-}"
source_path="${WOLF_E2E_SOURCE_PATH:-}"
email="${WOLF_E2E_ADMIN_EMAIL:-qualification@wolf.local}"
password="${WOLF_E2E_ADMIN_PASSWORD:-WolfQualification-2026!}"
timeout_seconds="${WOLF_E2E_TIMEOUT_SECONDS:-900}"
expected_backend="${WOLF_E2E_EXPECTED_BACKEND:-}"
expected_image_digest="${WOLF_E2E_EXPECTED_SCANNER_DIGEST:-}"
evidence_path="${WOLF_E2E_EVIDENCE_PATH:-}"

[[ "$base_url" =~ ^https?://[^[:space:]]+/api/v1/?$ ]] || {
  echo "WOLF_E2E_URL must be an http(s) URL ending in /api/v1" >&2
  exit 2
}
base_url="${base_url%/}"
[[ "$source_path" == /* ]] || {
  echo "WOLF_E2E_SOURCE_PATH must be the absolute path visible to the worker" >&2
  exit 2
}
[[ "$timeout_seconds" =~ ^[1-9][0-9]{0,4}$ ]] || {
  echo "WOLF_E2E_TIMEOUT_SECONDS must be an integer from 1 to 99999" >&2
  exit 2
}
if [[ -n "$expected_image_digest" ]]; then
  [[ "$expected_image_digest" =~ ^sha256:[a-f0-9]{64}$ ]] || {
    echo "WOLF_E2E_EXPECTED_SCANNER_DIGEST must be a sha256 digest" >&2
    exit 2
  }
fi
for command in curl jq; do
  command -v "$command" >/dev/null || {
    echo "required command is unavailable: $command" >&2
    exit 2
  }
done

work="$(mktemp -d "${TMPDIR:-/tmp}/wolf-remote-scan-api.XXXXXX")"
cleanup() {
  find "$work" -mindepth 1 -delete >/dev/null 2>&1 || true
  rmdir "$work" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

request() {
  method="$1"
  path="$2"
  output="$3"
  shift 3
  status="$(curl --silent --show-error --location --max-time 30 \
    --output "$output" --write-out '%{http_code}' \
    --request "$method" "$base_url$path" "$@")"
  case "$status" in
    2??) ;;
    *)
      echo "$method $path returned HTTP $status" >&2
      jq -c . "$output" >&2 2>/dev/null || sed -n '1,40p' "$output" >&2
      return 1
      ;;
  esac
}

deadline="$(( $(date +%s) + timeout_seconds ))"
until curl --silent --show-error --fail --max-time 5 \
  "$base_url/ready" >"$work/ready.json"; do
  (( $(date +%s) < deadline )) || {
    echo "Wolf API did not become ready within ${timeout_seconds}s" >&2
    exit 1
  }
  sleep 2
done
jq -e '.data.status == "ready" or .status == "ready" or .data.ready == true' \
  "$work/ready.json" >/dev/null

# Prefer login so an operator can bootstrap credentials out of band. A fresh
# qualification database permits first-user registration; retry login after a
# 403/409 registration response to keep reruns idempotent.
login_payload="$(jq -cn --arg email "$email" --arg password "$password" \
  '{email:$email,password:$password}')"
login_status="$(curl --silent --show-error --max-time 30 \
  --output "$work/login.json" --write-out '%{http_code}' \
  --request POST "$base_url/auth/login" \
  --header 'Content-Type: application/json' --data "$login_payload")"
if [[ "$login_status" != 2?? ]]; then
  register_status="$(curl --silent --show-error --max-time 30 \
    --output "$work/register.json" --write-out '%{http_code}' \
    --request POST "$base_url/auth/register" \
    --header 'Content-Type: application/json' --data "$login_payload")"
  case "$register_status" in
    2??)
      cp "$work/register.json" "$work/login.json"
      ;;
    403|409)
      request POST /auth/login "$work/login.json" \
        --header 'Content-Type: application/json' --data "$login_payload"
      ;;
    *)
      echo "registration returned HTTP $register_status after login returned HTTP $login_status" >&2
      jq -c . "$work/register.json" >&2 2>/dev/null || true
      exit 1
      ;;
  esac
fi
token="$(jq -er '.data.access_token | select(type == "string" and length > 20)' \
  "$work/login.json")"
auth=(--header "Authorization: Bearer $token" --header 'Content-Type: application/json')

run_key="$(date +%s)-$$-${RANDOM}"
repo_payload="$(jq -cn \
  --arg name "qualification-$run_key" --arg source "$source_path" \
  '{name:$name,source_type:"local",source_path:$source,default_branch:"main"}')"
request POST /repos "$work/repo.json" "${auth[@]}" --data "$repo_payload"
repo_id="$(jq -er '.data.id | select(type == "string" and length > 0)' "$work/repo.json")"

idempotency_key="deployment-qualification-$run_key"
scan_payload="$(jq -cn --arg repo "$repo_id" --arg reference "$idempotency_key" \
  '{repo_id:$repo,branch:"main",profile:"targeted",tools:["bandit"],client_reference:$reference}')"
request POST /scans "$work/scan.json" "${auth[@]}" \
  --header "Idempotency-Key: $idempotency_key" --data "$scan_payload"
scan_id="$(jq -er '.data.id | select(type == "string" and length > 0)' "$work/scan.json")"

# The retry proves the external idempotency contract, not only the DB unique
# constraint: an identical normalized request must resolve to the same scan.
request POST /scans "$work/scan-retry.json" "${auth[@]}" \
  --header "Idempotency-Key: $idempotency_key" --data "$scan_payload"
retry_id="$(jq -er .data.id "$work/scan-retry.json")"
[[ "$retry_id" == "$scan_id" ]] || {
  echo "idempotent scan retry returned a different scan ID" >&2
  exit 1
}

started_epoch="$(date +%s)"
while :; do
  request GET "/scans/$scan_id" "$work/scan-status.json" "${auth[@]}"
  state="$(jq -er .data.status "$work/scan-status.json")"
  case "$state" in
    completed) break ;;
    failed|cancelled)
      echo "remote scan entered terminal state $state" >&2
      jq -c '{status:.data.status,phase:.data.phase,failure_code:.data.failure_code,failure_message:.data.failure_message,tools_errors:.data.tools_errors}' \
        "$work/scan-status.json" >&2
      exit 1
      ;;
    pending|running) ;;
    *) echo "remote scan returned unknown state $state" >&2; exit 1 ;;
  esac
  (( $(date +%s) < deadline )) || {
    echo "remote scan did not complete within ${timeout_seconds}s" >&2
    exit 1
  }
  sleep 2
done
duration_seconds="$(( $(date +%s) - started_epoch ))"

request GET "/scans/$scan_id/result" "$work/result.json" "${auth[@]}"
jq -e '
  .data.id == $scan and .data.status == "completed" and
  .data.provenance.source_path == $source and
  (.data.provenance.commit_sha | type == "string" and length == 40) and
  (.data.provenance.tree_digest | type == "string" and test("^sha256:[a-f0-9]{64}$")) and
  (.data.scanner_scopes | type == "array" and
    any(.[]; .tool_name == "bandit" and .status == "completed"))
' --arg scan "$scan_id" --arg source "$source_path" "$work/result.json" >/dev/null

request GET "/scans/$scan_id/scanner-runs" "$work/runs.json" "${auth[@]}"
jq -e '(.data | type == "array") and any(.data[]; .tool_name == "bandit" and .status == "completed")' \
  "$work/runs.json" >/dev/null
if [[ -n "$expected_backend" ]]; then
  if ! jq -e --arg backend "$expected_backend" \
    '.data.execution_backend == $backend' "$work/scan-status.json" >/dev/null; then
    echo "scan execution backend did not match $expected_backend" >&2
    jq -c '{execution_backend:.data.execution_backend,status:.data.status}' \
      "$work/scan-status.json" >&2
    exit 1
  fi
fi
if [[ -n "$expected_image_digest" ]]; then
  if ! jq -e --arg digest "$expected_image_digest" '
      any(.data[]; .tool_name == "bandit" and .image_digest == $digest)
    ' "$work/runs.json" >/dev/null; then
    echo "Bandit scanner run did not retain exact digest $expected_image_digest" >&2
    jq -c '.data | map({tool_name,status,image,image_digest,runtime_backend})' \
      "$work/runs.json" >&2
    exit 1
  fi
fi

jq -n \
  --arg scanId "$scan_id" \
  --arg repositoryId "$repo_id" \
  --arg sourcePath "$source_path" \
  --arg backend "$expected_backend" \
  --arg scannerDigest "$expected_image_digest" \
  --argjson durationSeconds "$duration_seconds" \
  '{
    schemaVersion:"wolf.remote-scan-deployment-qualification/v1",
    scanId:$scanId,
    repositoryId:$repositoryId,
    sourcePath:$sourcePath,
    backend:(if $backend == "" then null else $backend end),
    scannerDigest:(if $scannerDigest == "" then null else $scannerDigest end),
    idempotencyVerified:true,
    gitProvenanceVerified:true,
    scannerRunVerified:true,
    durationSeconds:$durationSeconds,
    result:"passed"
  }' >"$work/evidence.json"

if [[ -n "$evidence_path" ]]; then
  mkdir -p "$(dirname "$evidence_path")"
  cp "$work/evidence.json" "$evidence_path"
else
  jq . "$work/evidence.json"
fi
